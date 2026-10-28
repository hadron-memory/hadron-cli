package cmd

// hadron-cli#1131: task handles (AT<n>) and `task create --project`. The
// handle and taskNumber are SERVER-derived and additive: printed before the
// existing full id in human output, added beside the id in JSON, and every
// task reference the CLI takes is passed to the server byte for byte.

import (
	"encoding/json"
	"strings"
	"testing"
)

// withHandle1131 gives one fixture task a handle (and the number 405).
func withHandle1131(fields, handle string) string {
	return strings.Replace(fields, `"id":"task1",`, `"id":"task1","handle":"`+handle+`","taskNumber":"405",`, 1)
}

func handleResponses1131(handle string) map[string]string {
	r := taskResponses()
	pending := withHandle1131(taskFieldsJSON, handle)
	started := withHandle1131(startedTaskFieldsJSON, handle)
	finished := withHandle1131(finishedTaskFieldsJSON, handle)
	backlog := withHandle1131(backlogTaskFieldsJSON, handle)
	r["AgentTaskCreate"] = `{"data":{"createAgentTask":` + pending + `}}`
	r["AgentTaskAssign"] = `{"data":{"assignAgentTask":` + started + `}}`
	r["AgentTaskReassign"] = `{"data":{"reassignAgentTask":` + started + `}}`
	r["AgentTaskStart"] = `{"data":{"startAgentTask":` + started + `}}`
	r["AgentTaskProgress"] = `{"data":{"recordAgentTaskProgress":` + started + `}}`
	r["AgentTaskFinish"] = `{"data":{"finishAgentTask":` + finished + `}}`
	r["AgentTaskCancel"] = `{"data":{"cancelAgentTask":` + finished + `}}`
	r["AgentTaskPark"] = `{"data":{"parkAgentTask":` + backlog + `}}`
	r["AgentTaskReady"] = `{"data":{"makeAgentTaskReady":` + pending + `}}`
	r["AgentTaskGet"] = `{"data":{"agentTask":` + pending + `}}`
	r["AgentTaskList"] = `{"data":{"agentTasks":[` + pending + `]}}`
	r["AgentTaskTree"] = `{"data":{"agentTaskTree":[` + pending + `]}}`
	return r
}

func TestTeamTaskHandleRendersBeforeTheIDOnEveryRead1131(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"get", []string{"team", "task", "get", "AT3.405"}, "AT3.405  task1  Fix the flaky CI build  (PENDING)"},
		{"tree", []string{"team", "task", "tree", "AT3.405"}, "• AT3.405 task1 ["},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := runTeamTask(t, handleResponses1131("AT3.405"), tc.argv...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("want %q in:\n%s", tc.want, out)
			}
		})
	}
	for _, verb := range []string{"list", "mine"} {
		t.Run(verb, func(t *testing.T) {
			_, out, err := runTeamTask(t, handleResponses1131("AT3.405"), "team", "task", verb)
			if err != nil {
				t.Fatal(err)
			}
			header := ""
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(line, "HANDLE") {
					header = line
				}
			}
			if h, i := strings.Index(header, "HANDLE"), strings.Index(header, "ID"); h < 0 || i < 0 || h > i {
				t.Fatalf("HANDLE column must precede ID: %q", header)
			}
			if !strings.Contains(out, "AT3.405") || strings.Index(out, "AT3.405") > strings.Index(out, "task1") {
				t.Fatalf("handle must precede id in the row:\n%s", out)
			}
		})
	}
}

func TestTeamTaskHandleOnMutationReceipts1131(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"create", []string{"team", "task", "create", "T"}, "✓ created task AT3.405 task1 ("},
		{"assign", []string{"team", "task", "assign", "AT3.405", "--assignee", "worker:Rufus"}, "AT3.405 task1 ("},
		{"reassign", []string{"team", "task", "reassign", "AT3.405", "--assignee", "worker:Rufus"}, "AT3.405 task1 ("},
		{"start", []string{"team", "task", "start", "AT3.405"}, "✓ started task AT3.405 task1 ("},
		{"progress", []string{"team", "task", "progress", "AT3.405"}, "✓ recorded progress on task AT3.405 task1 ("},
		{"finish", []string{"team", "task", "finish", "AT3.405", "--outcome", "done"}, "✓ finished task AT3.405 task1 ("},
		{"cancel", []string{"team", "task", "cancel", "AT3.405"}, "AT3.405 task1 ("},
		{"park", []string{"team", "task", "park", "AT3.405"}, "AT3.405 task1 ("},
		{"ready", []string{"team", "task", "ready", "AT3.405"}, "✓ made task AT3.405 task1 ready ("},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := runTeamTask(t, handleResponses1131("AT3.405"), tc.argv...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("want %q in:\n%s", tc.want, out)
			}
		})
	}
}

func TestTeamTaskHandleJSONIsAdditive1131(t *testing.T) {
	for _, argv := range [][]string{
		{"team", "task", "get", "task1", "--json"},
		{"team", "task", "start", "task1", "--json"},
		{"team", "task", "create", "T", "--json"},
	} {
		_, out, err := runTeamTask(t, handleResponses1131("AT3.405"), argv...)
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
		if dto["handle"] != "AT3.405" || dto["taskNumber"] != "405" || dto["id"] != "task1" {
			t.Fatalf("%v: handle/taskNumber/id = %v/%v/%v", argv, dto["handle"], dto["taskNumber"], dto["id"])
		}
		if tags, _ := dto["tags"].([]any); tags == nil {
			t.Fatalf("existing [] contract broken: %v", dto["tags"])
		}
	}
	_, out, err := runTeamTask(t, handleResponses1131("AT3.405"), "team", "task", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	if json.Unmarshal([]byte(out), &list) != nil || len(list) != 1 || list[0]["handle"] != "AT3.405" || list[0]["id"] != "task1" {
		t.Fatalf("list JSON %s", out)
	}
}

// A handle is server text: terminal controls are sanitised wherever it is shown.
func TestTeamTaskHandleIsSanitised1131(t *testing.T) {
	evil := "AT3\\u001b[31m\\u202e.1"
	for _, argv := range [][]string{
		{"team", "task", "get", "task1"}, {"team", "task", "tree", "task1"},
		{"team", "task", "list"}, {"team", "task", "mine"}, {"team", "task", "start", "task1"},
		{"team", "task", "create", "T"},
	} {
		_, out, err := runTeamTask(t, handleResponses1131(evil), argv...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(out, "\x1b\u202e") {
			t.Fatalf("%v: unsanitised handle in %q", argv, out)
		}
		if !strings.Contains(out, "AT3") {
			t.Fatalf("%v: handle missing: %q", argv, out)
		}
	}
}

// An older server that returns no handle keeps today's output exactly.
func TestTeamTaskWithoutHandleIsUnchanged1131(t *testing.T) {
	_, out, err := runTeamTask(t, taskResponses(), "team", "task", "get", "task1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "task1  Fix the flaky CI build  (PENDING)") {
		t.Fatalf("no-handle detail line changed: %q", out)
	}
}

// Every id-taking verb passes the reference through untouched, bare or
// qualified: the CLI neither parses nor composes a handle.
func TestTeamTaskReferencesPassThroughByteForByte1131(t *testing.T) {
	for _, ref := range []string{"AT3.405", "at3.405", "hrn:agenttask:acme.com:eng-team:AT3.405"} {
		for _, tc := range []struct {
			op   string
			argv []string
		}{
			{"AgentTaskGet", []string{"get", ref}},
			{"AgentTaskTree", []string{"tree", ref}},
			{"AgentTaskStart", []string{"start", ref}},
			{"AgentTaskProgress", []string{"progress", ref}},
			{"AgentTaskFinish", []string{"finish", ref, "--outcome", "done"}},
			{"AgentTaskCancel", []string{"cancel", ref}},
			{"AgentTaskAssign", []string{"assign", ref, "--assignee", "worker:Rufus"}},
			{"AgentTaskReassign", []string{"reassign", ref, "--assignee", "worker:Rufus"}},
			{"AgentTaskPark", []string{"park", ref}},
			{"AgentTaskReady", []string{"ready", ref}},
		} {
			t.Run(tc.op+"/"+ref, func(t *testing.T) {
				calls, _, err := runTeamTask(t, handleResponses1131("AT3.405"), append([]string{"team", "task"}, tc.argv...)...)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range *calls {
					if c.Op == tc.op {
						got := c.Vars["taskId"]
						if got == nil {
							got = c.Vars["id"]
						}
						if got == nil {
							got = c.Vars["root"]
						}
						if got != ref {
							t.Fatalf("%s sent %v, want %q", tc.op, got, ref)
						}
						return
					}
				}
				t.Fatalf("no %s call", tc.op)
			})
		}
		t.Run("create --parent/"+ref, func(t *testing.T) {
			calls, _, err := runTeamTask(t, handleResponses1131("AT3.405"), "team", "task", "create", "T", "--parent", ref)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range *calls {
				if c.Op == "AgentTaskCreate" {
					input, _ := c.Vars["input"].(map[string]any)
					if input["parentTaskId"] != ref {
						t.Fatalf("parentTaskId %v, want %q", input["parentTaskId"], ref)
					}
					return
				}
			}
			t.Fatal("no create call")
		})
		t.Run("list --parent/"+ref, func(t *testing.T) {
			calls, _, err := runTeamTask(t, handleResponses1131("AT3.405"), "team", "task", "list", "--parent", ref)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range *calls {
				if c.Op == "AgentTaskList" {
					if c.Vars["parentTaskId"] != ref {
						t.Fatalf("list parentTaskId %v, want %q", c.Vars["parentTaskId"], ref)
					}
					return
				}
			}
			t.Fatal("no list call")
		})
	}
}

func TestTeamTaskCreateProjectFlag1131(t *testing.T) {
	create := func(t *testing.T, argv ...string) map[string]any {
		t.Helper()
		calls, _, err := runTeamTask(t, handleResponses1131("AT3.405"), append([]string{"team", "task", "create", "T"}, argv...)...)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range *calls {
			if c.Op == "AgentTaskCreate" {
				input, _ := c.Vars["input"].(map[string]any)
				return input
			}
		}
		t.Fatal("no create call")
		return nil
	}
	if input := create(t); func() bool { _, ok := input["project"]; return ok }() {
		t.Fatalf("project must be OMITTED when the flag is unset: %v", input)
	}
	if input := create(t, "--project"); input["project"] != true {
		t.Fatalf("--project must send true: %v", input)
	}
	if input := create(t, "--project=false"); input["project"] != false {
		t.Fatalf("--project=false must send false: %v", input)
	}
	// --project with --parent: the server refuses; the CLI adds no check of its own.
	if input := create(t, "--project", "--parent", "AT3"); input["project"] != true || input["parentTaskId"] != "AT3" {
		t.Fatalf("both must reach the server: %v", input)
	}
}

func TestTeamTaskCreateProjectHelp1131(t *testing.T) {
	_, out, err := runTeamTask(t, taskResponses(), "team", "task", "create", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--project") || !strings.Contains(out, "AT0") {
		t.Fatalf("help must name --project and AT0:\n%s", out)
	}
	// The root definition is Proposed (cor:agt:050:01): not written into help.
	if strings.Contains(strings.ToLower(out), "topmost") {
		t.Fatalf("help must not ratify the Proposed root definition:\n%s", out)
	}
}

// The server's refusal for a bare handle held by several Apps keeps its code
// and meaning, and its text reaches the REAL error sink terminal-safe: the
// refusal quotes a server-supplied handle, so ESC, bidi and newlines are
// neutralised and the length bounded (the --json envelope keeps the original).
func TestTeamTaskQualifyRefusalIsSafeAtTheRealSink1131(t *testing.T) {
	r := handleResponses1131("AT3.405")
	hostile := "Qualify the handle \u001b[31m\u202e\nFORGED as hrn:agenttask:<app-urn>:AT3.405" + strings.Repeat("x", 1000)
	msg, _ := json.Marshal(hostile)
	r["AgentTaskGet"] = `{"errors":[{"message":` + string(msg) + `,"extensions":{"code":"TASK_HANDLE_QUALIFY"}}]}`
	writeTeamBinding(t)
	srv, _ := attnServer(t, r)
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "task", "get", "AT3.405"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a refusal must fail")
	}
	if !strings.Contains(err.Error(), "Qualify the handle") || !strings.Contains(err.Error(), "AT3.405") {
		t.Fatalf("refusal meaning lost: %q", err.Error())
	}
	// Raw JSON semantics: the envelope message is the original, unbounded text.
	if !strings.Contains(err.Error(), "\x1b") || len([]rune(err.Error())) < 1000 {
		t.Fatalf("err.Error() must stay raw for --json: %d runes", len([]rune(err.Error())))
	}
	renderError(f, err)
	got := f.IOStreams.ErrOut.(*strings.Builder).String()
	for _, bad := range []string{"\x1b", "\u202e", "\nFORGED"} {
		if strings.Contains(got, bad) {
			t.Fatalf("control %q reached the terminal: %q", bad, got)
		}
	}
	if n := len([]rune(got)); n > 1100 {
		t.Fatalf("refusal not length-bounded: %d runes", n)
	}
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("refusal must be one line: %q", got)
	}
}

// --json keeps the raw refusal text in the envelope (stdout), unsanitised.
func TestTeamTaskQualifyRefusalJSONStaysRaw1131(t *testing.T) {
	r := handleResponses1131("AT3.405")
	msg, _ := json.Marshal("Qualify the handle \u001b[31m AT3.405" + strings.Repeat("x", 1500))
	r["AgentTaskGet"] = `{"errors":[{"message":` + string(msg) + `,"extensions":{"code":"TASK_HANDLE_QUALIFY"}}]}`
	writeTeamBinding(t)
	srv, _ := attnServer(t, r)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "--json", "team", "task", "get", "AT3.405"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a refusal must fail")
	}
	renderError(f, err)
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
		t.Fatalf("envelope: %v: %q", jerr, out.String())
	}
	if !strings.Contains(env.Error.Message, "\x1b[31m") || len([]rune(env.Error.Message)) < 1500 {
		t.Fatalf("json message was altered: %d runes", len([]rune(env.Error.Message)))
	}
}

// A server that lacks the handle fields rejects the whole operation, so no
// fallback is possible: the command refuses with the schema-skew line naming
// the server as the side to update (a real unknown-field response).
func TestTeamTaskUnknownHandleFieldIsASkewRefusal1131(t *testing.T) {
	r := taskResponses()
	r["AgentTaskGet"] = `{"errors":[{"message":"Cannot query field \"handle\" on type \"AgentTask\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	_, out, err := runTeamTask(t, r, "team", "task", "get", "task1")
	if err == nil {
		t.Fatalf("an unknown-field response must fail: %q", out)
	}
	if !strings.Contains(err.Error(), "out of sync") || !strings.Contains(err.Error(), "handle") {
		t.Fatalf("skew refusal not actionable: %q", err.Error())
	}
}

// With no handle anywhere the tables keep the columns they had; with one, the
// HANDLE column leads.
func TestTeamTaskTablesOmitHandleColumnWhenNoHandles1131(t *testing.T) {
	for _, argv := range [][]string{{"team", "task", "list"}, {"team", "task", "mine"}, {"team", "task", "team"}} {
		_, out, err := runTeamTask(t, taskResponses(), argv...)
		if err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		if strings.Contains(out, "HANDLE") {
			t.Errorf("%v: HANDLE column shown with no handles:\n%s", argv, out)
		}
	}
}

func TestTeamTaskListTeamShowsTheHandleFirst1131(t *testing.T) {
	pages := [][]task1044{{
		{id: "t1", handle: "AT1.1", owner: worker1044("Marco"), assignee: worker1044("Jonas")},
		{id: "t2", handle: "AT1.1.2", owner: worker1044("Jonas"), assignee: worker1044("Cedar"), parent: "t1"},
	}}
	_, out, _, err := run1044(t, roster1044(), pages, "team", "task", "list", "--team=worker:Marco")
	if err != nil {
		t.Fatal(err)
	}
	header := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "HANDLE") {
			header = line
		}
	}
	if h, i := strings.Index(header, "HANDLE"), strings.Index(header, "ID"); h < 0 || h > i {
		t.Fatalf("HANDLE must precede ID: %q", header)
	}
	_, tree, _, err := run1044(t, roster1044(), pages, "team", "task", "list", "--team=worker:Marco", "--tree")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tree, "• AT1.1 t1 [") || !strings.Contains(tree, "• AT1.1.2 t2 [") {
		t.Fatalf("tree:\n%s", tree)
	}
	_, js, _, err := run1044(t, roster1044(), pages, "team", "task", "list", "--team=worker:Marco", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := decode1044(t, js)
	if got.Tasks[0]["handle"] != "AT1.1" || got.Tasks[0]["id"] != "t1" {
		t.Fatalf("json %v", got.Tasks[0])
	}
}
