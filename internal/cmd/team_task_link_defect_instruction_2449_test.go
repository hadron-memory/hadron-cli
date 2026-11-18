package cmd

import (
	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"reflect"
	"strings"
	"testing"
)

func TestLinkDefectInstruction2449Wire(t *testing.T) {
	for _, instructed := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "instructed"}[instructed], func(t *testing.T) {
			r := taskResponses()
			op := "TaskLinkDefect"
			if instructed {
				op += "Instructed"
			}
			r[op] = linkResponse2189(true, head2189)
			args := []string{"team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--introduced-at-head", head2189}
			if instructed {
				args = append(args, "--instruction", "live-chat:opaque:ref")
			}
			calls, _, err := runTeamTask(t, r, args...)
			if err != nil {
				t.Fatal(err)
			}
			c := oneCall2422(t, calls, op)
			want := map[string]any{"prTaskRef": "AT1.2", "defectRef": defect2189, "introducedAtHead": head2189}
			if instructed {
				want["instruction"] = map[string]any{"kind": "LIVE_CHAT", "ref": "opaque:ref"}
			}
			if !reflect.DeepEqual(c.Vars, want) || c.Session != "s-new" {
				t.Fatalf("request vars=%v session=%q", c.Vars, c.Session)
			}
			if strings.Contains(c.Query, "instruction: $instruction") != instructed {
				t.Fatalf("wrong document: %s", c.Query)
			}
			mutations := 0
			for _, call := range *calls {
				if strings.HasPrefix(call.Op, "TaskLinkDefect") {
					mutations++
				} else if call.Session != "" {
					t.Fatalf("lookup carries session: %+v", call)
				}
			}
			if mutations != 1 {
				t.Fatalf("mutations=%d calls=%v", mutations, *calls)
			}
		})
	}
}

func TestLinkDefectInstruction2449BadClaim(t *testing.T) {
	for _, claim := range []string{"", "bad:ref", "task:   "} {
		t.Run(claim, func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--instruction", claim)
			if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("err=%v requests=%v", err, *calls)
			}
		})
	}
}

func TestLinkDefectInstruction2449Refusal(t *testing.T) {
	r := taskResponses()
	r["TaskLinkDefectInstructed"] = `{"errors":[{"message":"denied","extensions":{"code":"DEFECT_LINK_FORBIDDEN"}}]}`
	calls, _, err := runTeamTask(t, r, "team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--instruction", "task:ref")
	if exitCodeFor(err) != exitcode.Forbidden {
		t.Fatalf("err=%v", err)
	}
	oneCall2422(t, calls, "TaskLinkDefectInstructed")
	for _, c := range *calls {
		if c.Op == "TaskLinkDefect" {
			t.Fatal("retried plain")
		}
	}
}

func TestLinkDefectInstruction2449PersonFallback(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  run1592
	}{
		{"unbound", run1592{}}, {"no-worktree", run1592{noWorktree: true}}, {"corrupt", run1592{bound: true, corrupt: true}},
		{"wrong-server", run1592{bound: true, mutate: func(b map[string]any) { b["server"] = "https://other.invalid" }}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := taskResponses()
			r["TaskLinkDefectInstructed"] = linkResponse2189(false, nil)
			tc.run.response = r
			calls, res := tc.run.exec(t, "team", "task", "link-defect", "task-id", "--defect", defect2189, "--instruction", "task:ref")
			if res.err != nil {
				t.Fatal(res.err)
			}
			c := oneCall2422(t, calls, "TaskLinkDefectInstructed")
			if c.Session != "" || c.Vars["instruction"] == nil {
				t.Fatalf("person fallback lost claim or borrowed session: %+v", c)
			}
			if !strings.Contains(res.out, "already linked") {
				t.Fatalf("idempotent output=%s", res.out)
			}
		})
	}
}

func TestLinkDefectInstruction2449UnknownOutcome(t *testing.T) {
	r := taskResponses()
	r["TaskLinkDefectInstructed"] = `{"data":{"linkDefectToPullRequest":null}}`
	calls, _, err := runTeamTask(t, r, "team", "task", "link-defect", "task-id", "--defect", defect2189, "--instruction", "task:ref")
	if exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "read the task's events before retrying") || !strings.HasSuffix(err.Error(), "task-id") {
		t.Fatalf("err=%v", err)
	}
	oneCall2422(t, calls, "TaskLinkDefectInstructed")
}

func TestLinkDefectInstruction2449BindingChangesBeforeSend(t *testing.T) {
	writeTeamBinding(t)
	r := taskResponses()
	r["TaskLinkDefectInstructed"] = linkResponse2189(true, nil)
	srv, calls := attnServer(t, r)
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	t.Setenv(store.EnvToken, "")
	f.TokenStoreFn = func() store.Store {
		rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "replacement-session" })
		return memStore{auth.Host(srv.URL): "hdr_user_test"}
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "task", "link-defect", "task-id", "--defect", defect2189, "--instruction", "task:ref"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	c := oneCall2422(t, calls, "TaskLinkDefectInstructed")
	if c.Session != "" || c.Vars["instruction"] == nil {
		t.Fatalf("changed binding must act as person with claim: %+v", c)
	}
}
