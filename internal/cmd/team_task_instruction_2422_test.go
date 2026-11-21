package cmd

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2422 (child of #1617; hadron-server#3323 / #3376, cor:dmo:050:12):
// --instruction <kind>:<ref> on the twelve `team task` writes. The instruction
// rides only the mutation, in its own document, so the no-flag documents and
// requests stay as they were; the worker session is whatever the authored-write
// middleware already attaches. The server decides admission: a refusal is shown
// once and never retried.

const taskInstructionRef2422 = "hrn:node:acme.com:chat:messages:m42"

type taskInstructionCase2422 struct {
	name     string
	args     []string
	plainOp  string
	instrOp  string
	response string // the answer, for both documents
}

func taskAnswer2422(field string) string {
	return `{"data":{"` + field + `":` + taskFieldsJSON + `}}`
}

func taskInstructionCases2422() []taskInstructionCase2422 {
	return []taskInstructionCase2422{
		{"create", []string{"create", "Fix the build", "--source", "https://github.com/acme/app/issues/12"}, "AgentTaskCreate", "AgentTaskCreateInstructed", taskAnswer2422("createAgentTask")},
		{"update", []string{"update", "task1", "--title", "New title"}, "AgentTaskUpdate", "AgentTaskUpdateInstructed", taskAnswer2422("updateAgentTask")},
		{"set-priority", []string{"set-priority", "task1", "3"}, "AgentTaskSetPriority", "AgentTaskSetPriorityInstructed", taskAnswer2422("setAgentTaskPriority")},
		{"add-blocker", []string{"add-blocker", "task1", "task2"}, "AgentTaskAddBlocker", "AgentTaskAddBlockerInstructed", taskAnswer2422("addAgentTaskBlocker")},
		{"remove-blocker", []string{"remove-blocker", "task1", "task2"}, "AgentTaskRemoveBlocker", "AgentTaskRemoveBlockerInstructed", taskAnswer2422("removeAgentTaskBlocker")},
		{"assign", []string{"assign", "task1", "--assignee", "worker:Rufus"}, "AgentTaskAssign", "AgentTaskAssignInstructed", taskAnswer2422("assignAgentTask")},
		{"reassign", []string{"reassign", "task1", "--assignee", "worker:Rufus"}, "AgentTaskReassign", "AgentTaskReassignInstructed", taskAnswer2422("reassignAgentTask")},
		{"cancel", []string{"cancel", "task1"}, "AgentTaskCancel", "AgentTaskCancelInstructed", taskAnswer2422("cancelAgentTask")},
		{"park", []string{"park", "task1"}, "AgentTaskPark", "AgentTaskParkInstructed", taskAnswer2422("parkAgentTask")},
		{"ready", []string{"ready", "task1"}, "AgentTaskReady", "AgentTaskReadyInstructed", taskAnswer2422("makeAgentTaskReady")},
		{"dispose-finding", []string{"dispose-finding", "task1", "F1", "--disposition", "fixed"}, "AgentTaskDisposeFinding", "AgentTaskDisposeFindingInstructed", dispositionResponse1556("fixed", nil)},
		{"release", []string{"release", "task1"}, "AgentTaskRelease", "AgentTaskReleaseInstructed", taskAnswer2422("releaseAgentTaskExternalId")},
	}
}

func (c taskInstructionCase2422) responses() map[string]string {
	r := taskResponses()
	r[c.plainOp] = c.response
	r[c.instrOp] = c.response
	r["IntakeTaskExternalId"] = `{"data":{"agentTask":{"externalId":"https://github.com/acme/app/issues/9"}}}`
	return r
}

func (c taskInstructionCase2422) argv(extra ...string) []string {
	return append(append([]string{"team", "task"}, c.args...), extra...)
}

// mutation returns the one call with the given operation, failing unless
// there is exactly one.
func oneCall2422(t *testing.T, calls *[]attnCall, op string) attnCall {
	t.Helper()
	var found []attnCall
	for _, c := range *calls {
		if c.Op == op {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %s call, got %d (calls %v)", op, len(found), opsOf2422(calls))
	}
	return found[0]
}

func opsOf2422(calls *[]attnCall) []string {
	var ops []string
	for _, c := range *calls {
		ops = append(ops, c.Op)
	}
	return ops
}

func TestTaskInstructionRidesOnlyTheMutation2422(t *testing.T) {
	for _, c := range taskInstructionCases2422() {
		t.Run(c.name, func(t *testing.T) {
			// The same command without the flag: the unchanged document.
			plain, _, err := runTeamTask(t, c.responses(), c.argv()...)
			if err != nil {
				t.Fatal(err)
			}
			base := oneCall2422(t, plain, c.plainOp)
			if _, has := base.Vars["instruction"]; has {
				t.Fatalf("no flag must send no instruction: %v", base.Vars)
			}
			for _, call := range *plain {
				if call.Op == c.instrOp {
					t.Fatalf("no flag must not use the instructed document")
				}
			}

			calls, _, err := runTeamTask(t, c.responses(), c.argv("--instruction", "team-chat-message:"+taskInstructionRef2422)...)
			if err != nil {
				t.Fatal(err)
			}
			got := oneCall2422(t, calls, c.instrOp)
			for _, call := range *calls {
				if call.Op == c.plainOp {
					t.Fatalf("an instructed run must not also send the plain document")
				}
			}
			want := map[string]any{"kind": "TEAM_CHAT_MESSAGE", "ref": taskInstructionRef2422}
			if !reflect.DeepEqual(got.Vars["instruction"], want) {
				t.Fatalf("instruction = %#v, want %#v", got.Vars["instruction"], want)
			}
			// Everything else is what the plain run sent.
			rest := map[string]any{}
			for k, v := range got.Vars {
				if k != "instruction" {
					rest[k] = v
				}
			}
			if !reflect.DeepEqual(rest, base.Vars) {
				t.Fatalf("variables other than instruction changed:\n got %v\nwant %v", rest, base.Vars)
			}
			if got.Session != "s-new" {
				t.Fatalf("the bound session must still attribute the write, got %q", got.Session)
			}
			if !strings.Contains(got.Query, "instruction: $instruction") {
				t.Fatalf("the instructed document must pass the instruction: %s", got.Query)
			}
			if strings.Contains(base.Query, "instruction") {
				t.Fatalf("the plain document must be unchanged: %s", base.Query)
			}
			// Every other request (a pre-read) carries neither the claim nor changes.
			for _, call := range *calls {
				if call.Op != c.instrOp {
					if _, has := call.Vars["instruction"]; has {
						t.Fatalf("%s must not carry the instruction", call.Op)
					}
				}
			}
		})
	}
}

func TestTaskInstructionBadFlagSendsNothing2422(t *testing.T) {
	for _, c := range taskInstructionCases2422() {
		for _, bad := range []string{"nokind", "bogus:ref", "task:", "task:   "} {
			t.Run(c.name+"/"+bad, func(t *testing.T) {
				calls, _, err := runTeamTask(t, c.responses(), c.argv("--instruction", bad)...)
				if code := exitCodeFor(err); code != exitcode.Usage {
					t.Fatalf("exit %d (%v), want usage", code, err)
				}
				if strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("the flag must exist: %v", err)
				}
				if len(*calls) != 0 {
					t.Fatalf("a bad instruction must send nothing, sent %v", opsOf2422(calls))
				}
			})
		}
	}
}

func TestTaskInstructionRefusalIsShownOnceAndNotRetried2422(t *testing.T) {
	for _, c := range taskInstructionCases2422() {
		t.Run(c.name, func(t *testing.T) {
			r := c.responses()
			r[c.instrOp] = `{"errors":[{"message":"the instruction was refused","extensions":{"code":"FORBIDDEN"}}],"data":null}`
			calls, out, err := runTeamTask(t, r, c.argv("--instruction", "task:hrn:agenttask:acme.com:eng:AT0.1")...)
			if code := exitCodeFor(err); code != exitcode.Forbidden {
				t.Fatalf("exit %d (%v), want forbidden", code, err)
			}
			oneCall2422(t, calls, c.instrOp)
			for _, call := range *calls {
				if call.Op == c.plainOp {
					t.Fatalf("a refusal must not be retried without the instruction")
				}
			}
			if strings.Contains(out, "✓") {
				t.Fatalf("no success receipt on a refusal: %q", out)
			}
		})
	}
}

func TestTaskCreateInstructionRefusesTheBatchPath2422(t *testing.T) {
	path := t.TempDir() + "/batch.json"
	if err := os.WriteFile(path, []byte(`[{"task":{"title":"A"}}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "create", "--batch-file", path, "--instruction", "task:x")
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit %d (%v), want usage", code, err)
	}
	if strings.Contains(err.Error(), "unknown flag") || !strings.Contains(err.Error(), "--instruction") || !strings.Contains(err.Error(), "--batch-file") || !strings.Contains(err.Error(), "takes no instruction") {
		t.Fatalf("the refusal must name both flags and say the batch write takes no instruction: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("sent %v", opsOf2422(calls))
	}
}

// The instruction travels in its own request variable and not as free text:
// a ref with colons and spaces is sent byte for byte.
func TestTaskInstructionRefIsSentAsWritten2422(t *testing.T) {
	c := taskInstructionCases2422()[7] // cancel
	ref := "client:abc: with spaces"
	calls, _, err := runTeamTask(t, c.responses(), c.argv("--instruction", "live-chat:"+ref)...)
	if err != nil {
		t.Fatal(err)
	}
	got := oneCall2422(t, calls, c.instrOp)
	b, _ := json.Marshal(got.Vars["instruction"])
	if string(b) != `{"kind":"LIVE_CHAT","ref":"`+ref+`"}` {
		t.Fatalf("instruction = %s", b)
	}
}

func TestTaskInstructionHelpNamesTheFlagOnEveryCommand2422(t *testing.T) {
	for _, c := range taskInstructionCases2422() {
		t.Run(c.name, func(t *testing.T) {
			_, out, err := runTeamTask(t, map[string]string{}, "team", "task", c.args[0], "--help")
			if err != nil {
				t.Fatal(err)
			}
			h := strings.Join(strings.Fields(out), " ")
			for _, want := range []string{"--instruction string", "team-chat-message, task or live-chat", "Needs server 2026-11-04 or later"} {
				if !strings.Contains(h, want) {
					t.Errorf("help lacks %q:\n%s", want, h)
				}
			}
		})
	}
}

// With no worker-session binding the write is the signed-in person's, as it is
// without the flag: the claim still goes out (the server checks the reference
// and records no claim) and no worker session is borrowed. Same as every earlier
// --instruction door (#1592, #1594, #1614): a person write, never a refusal by
// the CLI.
func TestTaskInstructionWithoutABindingIsThePersonsWrite2422(t *testing.T) {
	for _, c := range taskInstructionCases2422() {
		t.Run(c.name, func(t *testing.T) {
			teamGitDir(t) // a worktree with no binding file
			srv, calls := attnServer(t, c.responses())
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"--server", srv.URL, "--app", "acme.com:eng-team"}, c.argv("--instruction", "task:hrn:agenttask:acme.com:eng:AT0.1")...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			got := oneCall2422(t, calls, c.instrOp)
			if got.Session != "" {
				t.Fatalf("no binding: no worker session may ride, got %q", got.Session)
			}
			if got.Vars["instruction"] == nil {
				t.Fatalf("the claim must still be sent: %v", got.Vars)
			}
		})
	}
}
