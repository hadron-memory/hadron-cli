package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
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

// The fallback note (cli#2504, Jonas BLIND F2/P2-2→P3): a binding that exists
// but can't carry the claim says WHY on stderr; a worktree with no binding at
// all has nothing to explain and stays quiet.
func TestLinkDefectInstruction2449PersonFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		run      run1592
		wantNote string
	}{
		{"unbound", run1592{}, ""},
		{"no-worktree", run1592{noWorktree: true}, ""},
		{"corrupt", run1592{bound: true, corrupt: true}, "note: this worktree's worker session is not used (its session binding can't be read ("},
		{"wrong-server", run1592{bound: true, mutate: func(b map[string]any) { b["server"] = "https://other.invalid" }}, "note: this worktree's worker session is not used (its session binding belongs to"},
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
			if tc.wantNote == "" && res.errOut != "" {
				t.Fatalf("no binding means no note, but stderr says: %s", res.errOut)
			}
			if tc.wantNote != "" && !strings.Contains(res.errOut, tc.wantNote) {
				t.Fatalf("stderr lacks %q: %s", tc.wantNote, res.errOut)
			}
		})
	}
}

func TestLinkDefectInstruction2449UnknownOutcome(t *testing.T) {
	r := taskResponses()
	r["TaskLinkDefectInstructed"] = `{"data":{"linkDefectToPullRequest":null}}`
	calls, _, err := runTeamTask(t, r, "team", "task", "link-defect", "task-id", "--defect", defect2189, "--instruction", "task:ref")
	if exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "read the task's events before retrying") || strings.Contains(err.Error(), "task-id") {
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
	if note := "note: this worktree's worker session is not used (its session binding changed while the target was looked up)"; !strings.Contains(f.IOStreams.ErrOut.(*strings.Builder).String(), note) {
		t.Fatalf("a binding that changed mid-command must say why on stderr: %s", f.IOStreams.ErrOut.(*strings.Builder).String())
	}
}

// cli#2504 F3/F7: the command's own help names the server minimum its
// instructed path needs (2026-11-16, not the shared 2026-11-04), and the synopsis
// shows the optional --instruction.
func TestLinkDefectInstruction2449Help(t *testing.T) {
	f, _ := testFactory(t)
	out := &strings.Builder{}
	f.IOStreams.Out = output.Tracked(out)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "link-defect", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	if !namesServerFloor2849(help, "2026-11-16") {
		t.Fatalf("help must state this door's own floor, \"server 2026-11-16 or later\":\n%s", help)
	}
	if namesServerFloor2849(help, "2026-11-04") {
		t.Fatalf("help must not state the shared task-write floor, \"server 2026-11-04 or later\":\n%s", help)
	}
	if !strings.Contains(help, "link-defect <pr-build-task> --defect <issue URL> [--introduced-at-head <sha>] [--instruction <kind>:<ref>]") {
		t.Fatalf("the synopsis must show the optional --instruction:\n%s", help)
	}
}

// cli#2504 F7/F8: the agentic contract's synopsis shows the optional
// --instruction, and its no-retry sentence covers BOTH paths — "never retried
// without the flag" read as scoping no-retry to the instructed call.
func TestLinkDefectInstruction2449AgenticContract(t *testing.T) {
	doc := string(readContract(t))
	line := ""
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, "hadron team task link-defect ") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("the link-defect synopsis line is gone — this test is checking nothing")
	}
	if !strings.Contains(line, "[--instruction <kind>:<ref>]") {
		t.Fatalf("the contract synopsis must show the optional --instruction:\n  %s", line)
	}
	if flat := strings.Join(strings.Fields(doc), " "); !strings.Contains(flat, "A refusal is never retried on either path: no retry after a plain refusal, and no plain retry after an instructed one.") {
		t.Fatal("the no-retry sentence must cover both the plain and the instructed path")
	}
	if strings.Contains(doc, "Refusals are never retried without the flag.") {
		t.Fatal("the ambiguous no-retry sentence is still there")
	}
}

// cli#2504 F11: every typed refusal keeps its own exit code, is shown once and
// is never retried — not even by dropping to the plain document.
func TestLinkDefectInstruction2449RefusalCodes(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{
		{"TASK_NOT_FOUND", exitcode.NotFound},
		{"PR_REFERENCE_MISSING", exitcode.Conflict},
		{"DEFECT_HEAD_NOT_FROZEN", exitcode.Conflict},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := taskResponses()
			r["TaskLinkDefectInstructed"] = `{"errors":[{"message":"refused","extensions":{"code":"` + tc.code + `"}}]}`
			calls, _, err := runTeamTask(t, r, "team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--instruction", "task:ref")
			if exitCodeFor(err) != tc.want || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("err=%v want exit %d with the code visible", err, tc.want)
			}
			oneCall2422(t, calls, "TaskLinkDefectInstructed")
			for _, c := range *calls {
				if c.Op == "TaskLinkDefect" {
					t.Fatal("retried plain after a typed refusal")
				}
			}
		})
	}
}

// cli#2504 F6: an older server (before 2026-11-16) refuses the instructed
// document; the refusal is passed through, once, with no plain retry.
func TestLinkDefectInstruction2449OldServerRefusal(t *testing.T) {
	r := taskResponses()
	r["TaskLinkDefectInstructed"] = `{"errors":[{"message":"Field 'instruction' is not defined (server too old)","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	calls, _, err := runTeamTask(t, r, "team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--instruction", "task:ref")
	if err == nil || !strings.Contains(err.Error(), "server too old") {
		t.Fatalf("the old server's refusal must pass through: %v", err)
	}
	oneCall2422(t, calls, "TaskLinkDefectInstructed")
	for _, c := range *calls {
		if c.Op == "TaskLinkDefect" {
			t.Fatal("retried plain after an old-server refusal")
		}
	}
}

// cli#2504 F5: a config the CLI cannot read fails the command before any
// request, on the instructed path exactly as on the plain one — f.GraphQLClient
// resolves the config before the instructed helper runs, so the helper's own
// mapping is unreachable here and the pin holds the observable contract.
func TestLinkDefectInstruction2449ConfigFailure(t *testing.T) {
	broken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(broken, "hadron"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "hadron", "config.toml"), []byte("not = [valid toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := taskResponses()
	r["TaskLinkDefectInstructed"] = linkResponse2189(true, nil)
	srv, calls := attnServer(t, r)
	f, _ := testFactory(t)
	t.Setenv("XDG_CONFIG_HOME", broken) // after testFactory: its temp dir must not mask the broken config
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "link-defect", "task-id", "--defect", defect2189, "--instruction", "task:ref"})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "reading ") || !strings.Contains(err.Error(), "config.toml") {
		t.Fatalf("a broken config must fail with the config path before any request: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("no request may be sent: %+v", *calls)
	}
	_ = srv.URL
}
