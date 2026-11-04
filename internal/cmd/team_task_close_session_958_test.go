package cmd

// hadron-cli#958 — the task's effective close-session-when-done value
// (cor:agt:050:10) is shown in the text receipts of create, assign and
// reassign and in `task get`, not only in --json. The value shown is the one
// the server RETURNED: the default and both explicit flags are rendered from
// the response, never from what the caller passed.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const (
	closeSessionYes = "close session when done: yes"
	closeSessionNo  = "close session when done: no"
)

// withCloseSession rewrites a fixture's closeSessionWhenDone to v.
func withCloseSession(fixture string, v bool) string {
	if v {
		return fixture
	}
	return strings.Replace(fixture, `"closeSessionWhenDone":true`, `"closeSessionWhenDone":false`, 1)
}

// closeSessionResponses answers every verb under test with closeSessionWhenDone=v.
func closeSessionResponses(v bool) map[string]string {
	r := taskResponses()
	r["AgentTaskCreate"] = `{"data":{"createAgentTask":` + withCloseSession(taskFieldsJSON, v) + `}}`
	r["AgentTaskAssign"] = `{"data":{"assignAgentTask":` + withCloseSession(startedTaskFieldsJSON, v) + `}}`
	r["AgentTaskReassign"] = `{"data":{"reassignAgentTask":` + withCloseSession(startedTaskFieldsJSON, v) + `}}`
	r["AgentTaskGet"] = `{"data":{"agentTask":` + withCloseSession(taskFieldsJSON, v) + `}}`
	return r
}

func TestC958TextShowsServerReturnedCloseSession(t *testing.T) {
	verbs := []struct {
		name string
		argv []string
	}{
		{"create default", []string{"team", "task", "create", "Fix it"}},
		{"create --no-close-session", []string{"team", "task", "create", "Fix it", "--no-close-session"}},
		{"assign default", []string{"team", "task", "assign", "task1", "--assignee", "worker:Rufus"}},
		{"assign --close-session", []string{"team", "task", "assign", "task1", "--assignee", "worker:Rufus", "--close-session"}},
		{"assign --no-close-session", []string{"team", "task", "assign", "task1", "--assignee", "worker:Rufus", "--no-close-session"}},
		{"reassign default", []string{"team", "task", "reassign", "task1", "--assignee", "worker:Rufus"}},
		{"reassign --no-close-session", []string{"team", "task", "reassign", "task1", "--assignee", "worker:Rufus", "--no-close-session"}},
		{"get", []string{"team", "task", "get", "task1"}},
	}
	// Both server answers for every invocation: the line follows the
	// response even where it contradicts the flag the caller passed.
	for _, served := range []bool{true, false} {
		want, notWant := closeSessionYes, closeSessionNo
		if !served {
			want, notWant = closeSessionNo, closeSessionYes
		}
		for _, v := range verbs {
			t.Run(fmt.Sprintf("%s served=%v", v.name, served), func(t *testing.T) {
				_, out, err := runTeamTask(t, closeSessionResponses(served), v.argv...)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out, want) || strings.Contains(out, notWant) {
					t.Fatalf("server returned %v; want %q in:\n%s", served, want, out)
				}
				// yes explains what finishing requests, hedged on the host (the
				// platform ends a session only through a live host); no is a
				// terse key: value like the lines around it. Neither cites a
				// spec at runtime — the help does (Uma's P3s on #979).
				if served && !strings.Contains(out, closeSessionYes+" — finishing or cancelling asks the assignee to end its worker session; the platform may end it after a grace period\n") {
					t.Fatalf("true does not say what finishing does:\n%s", out)
				}
				if !served && !strings.Contains(out, "  "+closeSessionNo+"\n") {
					t.Fatalf("false is not the plain line:\n%s", out)
				}
				if strings.Contains(out, "cor:agt") {
					t.Fatalf("runtime line cites a spec:\n%s", out)
				}

				// --json carries the same boolean the text showed.
				_, jout, err := runTeamTask(t, closeSessionResponses(served), append(v.argv, "--json")...)
				if err != nil {
					t.Fatal(err)
				}
				var dto map[string]any
				if err := json.Unmarshal([]byte(jout), &dto); err != nil {
					t.Fatalf("not JSON: %v\n%s", err, jout)
				}
				if dto["closeSessionWhenDone"] != served {
					t.Fatalf("json closeSessionWhenDone %v, text said %v", dto["closeSessionWhenDone"], served)
				}
				if strings.Contains(jout, "close session when done") {
					t.Fatalf("text line leaked into --json:\n%s", jout)
				}
			})
		}
	}
}

// The line belongs to the verbs that set or read the choice; the lifecycle
// receipts that share writeTaskReceipt (start, cancel) stay as they were.
func TestC958LifecycleReceiptsUnchanged(t *testing.T) {
	for _, verb := range []string{"start", "cancel"} {
		_, out, err := runTeamTask(t, taskResponses(), "team", "task", verb, "task1")
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		if strings.Contains(out, "close session when done") {
			t.Fatalf("%s receipt grew the close-session line:\n%s", verb, out)
		}
	}
}

// The help says what the receipt says — finishing OR cancelling — and tells a
// caller who wants the assignee's session kept to opt out at create (#958
// follow-up, Ada #29181); since #988 the opt-in is --close-session.
func TestC958HelpMatchesReceipt(t *testing.T) {
	for _, verb := range []string{"create", "assign", "reassign"} {
		_, out, err := runTeamTask(t, taskResponses(), "team", "task", verb, "--help")
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		if strings.Contains(out, "finishing ends") || strings.Contains(out, "finishing does not") || strings.Contains(out, "finishing asks") {
			t.Errorf("%s help says only finishing:\n%s", verb, out)
		}
		if !strings.Contains(out, "finishing or cancelling") {
			t.Errorf("%s help omits cancelling:\n%s", verb, out)
		}
	}
	_, out, err := runTeamTask(t, taskResponses(), "team", "task", "create", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(out), " "), "pass --close-session at create") {
		t.Errorf("create help does not tell callers to opt in at create:\n%s", out)
	}
}
