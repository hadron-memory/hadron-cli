package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const defectURL2189 = "https://github.com/acme/project/issues/17"
const defectHead2189 = "0123456789abcdef0123456789abcdef01234567"

func defectReply2189(created bool, head any) string {
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"linkDefectToPullRequest": map[string]any{"taskId": "server-task", "defect": defectURL2189, "introducedAtHead": head, "eventId": "event-17", "linkedAt": "2026-11-17T12:00:00Z", "created": created}}})
	return string(b)
}

func defectCalls2189(calls *[]attnCall) []attnCall {
	result := []attnCall{}
	for _, c := range *calls {
		if c.Op == "LinkDefectToPullRequest" {
			result = append(result, c)
		}
	}
	return result
}

func TestTaskLinkDefectWireAndReceipt2189(t *testing.T) {
	for _, created := range []bool{true, false} {
		for _, withHead := range []bool{true, false} {
			for _, asJSON := range []bool{true, false} {
				t.Run(fmt.Sprintf("created=%t/head=%t/json=%t", created, withHead, asJSON), func(t *testing.T) {
					var head any
					if withHead {
						head = defectHead2189
					}
					args := []string{"team", "task", "link-defect", "AT1.2", "--defect", defectURL2189, "--app", "acme.com:eng"}
					if withHead {
						args = append(args, "--introduced-at-head", defectHead2189)
					}
					if asJSON {
						args = append(args, "--json")
					}
					calls, out, err := runTeamTask(t, map[string]string{"LinkDefectToPullRequest": defectReply2189(created, head), "TeamAppIdentity": teamAppIdentityJSON}, args...)
					if err != nil {
						t.Fatal(err)
					}
					writes := defectCalls2189(calls)
					if len(writes) != 1 {
						t.Fatalf("writes=%d calls=%+v", len(writes), *calls)
					}
					call := writes[0]
					if call.Session != "s-new" {
						t.Fatalf("write lost worker attribution: %+v", call)
					}
					if call.Vars["prTaskRef"] != "hrn:agenttask:acme.com:eng:AT1.2" || call.Vars["defectRef"] != defectURL2189 {
						t.Fatalf("vars=%v", call.Vars)
					}
					if withHead {
						if call.Vars["introducedAtHead"] != defectHead2189 || len(call.Vars) != 3 {
							t.Fatalf("vars=%v", call.Vars)
						}
					} else if _, ok := call.Vars["introducedAtHead"]; ok || len(call.Vars) != 2 {
						t.Fatalf("absent head sent: %v", call.Vars)
					}
					if !strings.Contains(call.Query, "mutation LinkDefectToPullRequest") || strings.Contains(call.Query, "instruction") {
						t.Fatalf("unexpected document: %s", call.Query)
					}
					if asJSON {
						var dto map[string]any
						if e := json.Unmarshal([]byte(out), &dto); e != nil {
							t.Fatal(e)
						}
						if len(dto) != 6 || dto["taskId"] != "server-task" || dto["defect"] != defectURL2189 || dto["eventId"] != "event-17" || dto["linkedAt"] != "2026-11-17T12:00:00Z" || dto["created"] != created || dto["introducedAtHead"] != head {
							t.Fatalf("dto=%s", out)
						}
					} else {
						want := "linked"
						if !created {
							want = "already linked"
						}
						for _, part := range []string{want, "server-task", defectURL2189, "event-17", "2026-11-17T12:00:00Z"} {
							if !strings.Contains(out, part) {
								t.Errorf("missing %q in %q", part, out)
							}
						}
						if withHead && !strings.Contains(out, defectHead2189) {
							t.Fatalf("head missing: %s", out)
						}
						if !withHead && !strings.Contains(out, "none") {
							t.Fatalf("null head missing: %s", out)
						}
					}
				})
			}
		}
	}
}

func TestTaskLinkDefectRefusalsAndNoPersonRetry2189(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"DEFECT_LINK_FORBIDDEN", 8}, {"TASK_NOT_FOUND", 4}, {"PR_REFERENCE_MISSING", 5}, {"DEFECT_HEAD_NOT_FROZEN", 5}, {"BAD_USER_INPUT", 2}} {
		t.Run(tc.code, func(t *testing.T) {
			reply := fmt.Sprintf(`{"errors":[{"message":"refused","extensions":{"code":%q}}]}`, tc.code)
			calls, _, err := runTeamTask(t, map[string]string{"LinkDefectToPullRequest": reply, "TeamAppIdentity": teamAppIdentityJSON}, "team", "task", "link-defect", "task1", "--defect", defectURL2189, "--introduced-at-head", defectHead2189)
			if exitCodeFor(err) != tc.exit {
				t.Fatalf("exit=%d err=%v", exitCodeFor(err), err)
			}
			writes := defectCalls2189(calls)
			if len(writes) != 1 || writes[0].Session != "s-new" {
				t.Fatalf("retried or lost session: %+v", *calls)
			}
		})
	}
}

func TestTaskLinkDefectConcealmentEnvelope2189(t *testing.T) {
	var previous string
	for _, ref := range []string{"missing-task", "foreign-app-task", "unreadable-task"} {
		teamGitDir(t)
		server, calls := attnServer(t, map[string]string{"LinkDefectToPullRequest": `{"errors":[{"message":"Task not found.","extensions":{"code":"TASK_NOT_FOUND"}}]}`})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "task", "link-defect", ref, "--defect", defectURL2189, "--json", "--server", server.URL})
		err := root.Execute()
		if exitCodeFor(err) != 4 {
			t.Fatal(err)
		}
		if code := renderError(f, err); code != 4 {
			t.Fatalf("exit=%d", code)
		}
		if previous != "" && previous != out.String() {
			t.Fatalf("concealment differs: %q vs %q", previous, out.String())
		}
		previous = out.String()
		if len(defectCalls2189(calls)) != 1 {
			t.Fatalf("calls=%+v", *calls)
		}
	}
}

func TestTaskLinkDefectEmptyFlagBeforeRequests2189(t *testing.T) {
	for _, extra := range [][]string{nil, {"--defect", ""}, {"--defect", "  "}} {
		teamGitDir(t)
		server, calls := attnServer(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		args := append([]string{"team", "task", "link-defect", "task1", "--server", server.URL}, extra...)
		root.SetArgs(args)
		if err := root.Execute(); exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("err=%v calls=%+v", err, *calls)
		}
	}
}

func TestTaskLinkDefectServerOwnsValueChecks2189(t *testing.T) {
	teamGitDir(t)
	server, calls := attnServer(t, map[string]string{"LinkDefectToPullRequest": `{"errors":[{"message":"bad input","extensions":{"code":"BAD_USER_INPUT"}}]}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "link-defect", "task1", "--defect", "not-an-issue-url", "--introduced-at-head", "", "--server", server.URL})
	if err := root.Execute(); exitCodeFor(err) != 2 {
		t.Fatal(err)
	}
	writes := defectCalls2189(calls)
	if len(writes) != 1 || writes[0].Vars["defectRef"] != "not-an-issue-url" || writes[0].Vars["introducedAtHead"] != "" {
		t.Fatalf("not delegated verbatim: %+v", *calls)
	}
}

func TestTaskLinkDefectHelp2189(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "link-defect", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Proposed", "cor:agt:130:05", "already linked", "exit 8", "exit 4", "exit 5", "exit 2", "instruction"} {
		if !strings.Contains(out.String(), text) {
			t.Errorf("missing %q", text)
		}
	}
}

func TestTaskLinkDefectExistingReceiptWins2189(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"LinkDefectToPullRequest": defectReply2189(false, defectHead2189), "TeamAppIdentity": teamAppIdentityJSON}, "team", "task", "link-defect", "task1", "--defect", defectURL2189, "--introduced-at-head", strings.Repeat("f", 40), "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if e := json.Unmarshal([]byte(out), &dto); e != nil {
		t.Fatal(e)
	}
	if dto["introducedAtHead"] != defectHead2189 || dto["created"] != false || len(defectCalls2189(calls)) != 1 {
		t.Fatalf("existing receipt replaced/retried: %s calls=%+v", out, *calls)
	}
}

func TestTaskLinkDefectIncompleteReceipt2189(t *testing.T) {
	for _, reply := range []string{`{"data":null}`, `{"data":{"linkDefectToPullRequest":null}}`, `{"data":{"linkDefectToPullRequest":{}}}`, `{"data":{"linkDefectToPullRequest":{"taskId":"task","defect":"issue","eventId":"event","created":true}}}`} {
		calls, out, err := runTeamTask(t, map[string]string{"LinkDefectToPullRequest": reply, "TeamAppIdentity": teamAppIdentityJSON}, "team", "task", "link-defect", "task1", "--defect", defectURL2189, "--json")
		if exitCodeFor(err) != 1 || !strings.Contains(err.Error(), "outcome unknown") || out != "" || len(defectCalls2189(calls)) != 1 {
			t.Fatalf("err=%v out=%q calls=%+v", err, out, *calls)
		}
	}
}
