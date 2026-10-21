package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// cli#962 (CHECKED T2, gate 6311828784): the four task mutations surface the
// typed warnings server#2207 (eb2cb49a) returns in the response's
// `extensions.warnings` — on stderr in text mode, as an always-present
// `warnings` array in --json — while the stdout receipt and the exit code stay
// exactly what they were.

// c962Mutations names each command, its argv, its operation and the response
// key its warnings are filed under (we send no aliases).
var c962Mutations = []struct {
	name string
	argv []string
	op   string
	key  string
}{
	{"create", []string{"team", "task", "create", "T"}, "AgentTaskCreate", "createAgentTask"},
	{"assign", []string{"team", "task", "assign", "task1", "--assignee", "worker:Rufus"}, "AgentTaskAssign", "assignAgentTask"},
	{"reassign", []string{"team", "task", "reassign", "task1", "--assignee", "worker:Rufus"}, "AgentTaskReassign", "reassignAgentTask"},
	{"finish", []string{"team", "task", "finish", "task1", "--outcome", "succeeded"}, "AgentTaskFinish", "finishAgentTask"},
}

// c962Warnings is a response's extensions.warnings: two of this operation's
// own (one mapped, one an unknown code with no register), one malformed entry
// (a numeric code) and one filed under another response key.
func c962Warnings(key string) string {
	return `[` +
		`{"path":"` + key + `","code":"task.assign.assignee-busy","text":"The assignee already has an open task.","register":{"state":"mapped","taskLoc":"tasks:rebalance","taskUrn":"hrn:node:acme.com:eng-system:tasks:rebalance"},"extra":1},` +
		`{"path":"` + key + `","code":"task.future.unknown","text":"A code this CLI has never seen."},` +
		`{"path":"` + key + `","code":7,"text":"malformed"},` +
		`{"path":"someOtherMutation","code":"task.create.no-source","text":"Not this operation's."}` +
		`]`
}

// c962With splices extensions.warnings into op's standard response.
func c962With(op, warnings string) map[string]string {
	responses := taskResponses()
	body := responses[op]
	responses[op] = strings.TrimSuffix(body, "}") + `,"extensions":{"warnings":` + warnings + `}}`
	return responses
}

// runTeamTaskStreams is runTeamTask returning stderr too: the warning lines
// render there.
func runTeamTaskStreams(t *testing.T, responses map[string]string, argv ...string) (string, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, _ := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	errOut := f.IOStreams.ErrOut.(*strings.Builder)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestC962TextWarningsOnStderrReceiptUnchanged(t *testing.T) {
	for _, m := range c962Mutations {
		t.Run(m.name, func(t *testing.T) {
			baseOut, _, err := runTeamTaskStreams(t, taskResponses(), m.argv...)
			if err != nil {
				t.Fatal(err)
			}
			out, errOut, err := runTeamTaskStreams(t, c962With(m.op, c962Warnings(m.key)), m.argv...)
			if err != nil {
				t.Fatalf("a warning changed the exit: %v", err)
			}
			if out != baseOut {
				t.Fatalf("stdout receipt changed:\n got %q\nwant %q", out, baseOut)
			}
			want := "warning: [task.assign.assignee-busy] The assignee already has an open task. → hrn:node:acme.com:eng-system:tasks:rebalance\n" +
				"warning: [task.future.unknown] A code this CLI has never seen.\n"
			if !strings.Contains(errOut, want) {
				t.Fatalf("stderr %q\nwant it to contain %q", errOut, want)
			}
			for _, leak := range []string{"malformed", "Not this operation's"} {
				if strings.Contains(errOut, leak) {
					t.Fatalf("stderr shows a filtered entry (%q): %q", leak, errOut)
				}
			}
		})
	}
}

func TestC962TextUnmappedAndUnresolved(t *testing.T) {
	warnings := `[{"path":"createAgentTask","code":"task.create.no-source","text":"No source.","register":{"state":"unmapped"}},` +
		`{"path":"createAgentTask","code":"task.assign.assignee-busy","text":"Busy.","register":{"state":"unresolved"}}]`
	_, errOut, err := runTeamTaskStreams(t, c962With("AgentTaskCreate", warnings), "team", "task", "create", "T")
	if err != nil {
		t.Fatal(err)
	}
	want := "warning: [task.create.no-source] No source. (no task mapped)\nwarning: [task.assign.assignee-busy] Busy.\n"
	if !strings.Contains(errOut, want) {
		t.Fatalf("stderr %q\nwant %q", errOut, want)
	}
}

type c962WarningJSON struct {
	Code     string         `json:"code"`
	Text     string         `json:"text"`
	Register map[string]any `json:"register"`
}

func TestC962JSONWarningsArray(t *testing.T) {
	for _, m := range c962Mutations {
		t.Run(m.name, func(t *testing.T) {
			out, _, err := runTeamTaskStreams(t, c962With(m.op, c962Warnings(m.key)), append(m.argv, "--json")...)
			if err != nil {
				t.Fatalf("a warning changed the exit: %v", err)
			}
			var dto struct {
				ID       string            `json:"id"`
				Warnings []c962WarningJSON `json:"warnings"`
			}
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out)
			}
			if dto.ID == "" {
				t.Fatalf("the task object is gone: %s", out)
			}
			want := []c962WarningJSON{
				{Code: "task.assign.assignee-busy", Text: "The assignee already has an open task.", Register: map[string]any{
					"state": "mapped", "taskLoc": "tasks:rebalance", "taskUrn": "hrn:node:acme.com:eng-system:tasks:rebalance",
				}},
				{Code: "task.future.unknown", Text: "A code this CLI has never seen."},
			}
			if !reflect.DeepEqual(dto.Warnings, want) {
				t.Fatalf("warnings %+v\nwant %+v", dto.Warnings, want)
			}
			// The unknown code's register is an explicit null, not omitted.
			if !strings.Contains(out, `"register": null`) {
				t.Fatalf("register absent rather than null: %s", out)
			}
		})
	}
}

func TestC962AbsentWarningsAreEmpty(t *testing.T) {
	for _, m := range c962Mutations {
		t.Run(m.name, func(t *testing.T) {
			out, errOut, err := runTeamTaskStreams(t, taskResponses(), append(m.argv, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out)
			}
			list, ok := dto["warnings"].([]any)
			if !ok || len(list) != 0 {
				t.Fatalf("warnings must be [] when absent, got %v in %s", dto["warnings"], out)
			}
			if strings.Contains(errOut, "warning:") {
				t.Fatalf("stderr %q", errOut)
			}
		})
	}
}

// A malformed carrier (warnings not a list) never fails the command.
func TestC962MalformedCarrierTolerated(t *testing.T) {
	for _, warnings := range []string{`{"path":"finishAgentTask"}`, `"nope"`, `null`, `[null, 3, "x"]`} {
		out, errOut, err := runTeamTaskStreams(t, c962With("AgentTaskFinish", warnings),
			"team", "task", "finish", "task1", "--outcome", "succeeded", "--json")
		if err != nil {
			t.Fatalf("%s: %v", warnings, err)
		}
		if !strings.Contains(out, `"warnings": []`) {
			t.Fatalf("%s: %s", warnings, out)
		}
		if strings.Contains(errOut, "warning:") {
			t.Fatalf("%s: stderr %q", warnings, errOut)
		}
	}
}

// A failed invocation keeps today's error and exit, and prints no warnings
// (the gate: failed invocations do not print success warnings), even when the
// server sends one beside data:null.
func TestC962FailedResponseUnchanged(t *testing.T) {
	failed := `{"data":null,"errors":[{"message":"Task not found.","path":["finishAgentTask"],"extensions":{"code":"NOT_FOUND"}}]}`
	withWarning := `{"data":null,"errors":[{"message":"Task not found.","path":["finishAgentTask"],"extensions":{"code":"NOT_FOUND"}}],` +
		`"extensions":{"warnings":[{"path":"finishAgentTask","code":"task.finish.open-children","text":"This task still has open child tasks."}]}}`
	argv := []string{"team", "task", "finish", "task1", "--outcome", "succeeded"}

	base := taskResponses()
	base["AgentTaskFinish"] = failed
	baseOut, baseErrOut, baseErr := runTeamTaskStreams(t, base, argv...)
	if baseErr == nil {
		t.Fatal("the fixture should fail")
	}

	warned := taskResponses()
	warned["AgentTaskFinish"] = withWarning
	out, errOut, err := runTeamTaskStreams(t, warned, argv...)
	if err == nil || err.Error() != baseErr.Error() {
		t.Fatalf("error changed: %v (want %v)", err, baseErr)
	}
	if out != baseOut || errOut != baseErrOut {
		t.Fatalf("streams changed:\nout %q / %q\nerr %q / %q", out, baseOut, errOut, baseErrOut)
	}
	if strings.Contains(errOut, "open child tasks") {
		t.Fatalf("a failed invocation printed a warning: %q", errOut)
	}
}

// Other task verbs install no sink: a warning on their response is ignored
// and their JSON gains no field.
func TestC962OtherVerbsUnchanged(t *testing.T) {
	responses := c962With("AgentTaskStart", `[{"path":"startAgentTask","code":"x","text":"y"}]`)
	out, errOut, err := runTeamTaskStreams(t, responses, "team", "task", "start", "task1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"warnings"`) || strings.Contains(errOut, "warning:") {
		t.Fatalf("start gained warnings:\n%s\n%s", out, errOut)
	}
}
