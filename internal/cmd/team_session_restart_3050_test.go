package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3050 — `team session restart` pins. The restart door (cor:agt:070:08
// S4a) acts only through the calling worker's own bound session, so the request
// must ride X-Hadron-Session with the binding's session id; the input carries
// the App ref the binding supplies, the required reason and the target exactly
// as passed (all three required — an explicit null or an omission must never
// reach the wire); every documented refusal exits as its code's class; the JSON
// envelope renders the SDL result with endedSessionIds never null and taskId
// null when the worker has no open task.

const restartSuccess3050 = `{"data":{"restartWorkerSession":{"endedSessionIds":["sess-a","sess-b"],"hostId":"host-7","operationId":"op-42","taskId":"task-9"}}}`

const restartTaskless3050 = `{"data":{"restartWorkerSession":{"endedSessionIds":[],"hostId":"host-7","operationId":"op-43","taskId":null}}}`

func restartResponses3050(result string) map[string]string {
	return map[string]string{
		"TeamSessionRestart": result,
		// The scope line's decorating read (review:ambient-scope-…): answered
		// happily; the degraded path is pinned by the --json test, which never
		// issues it.
		"TeamAppIdentity": teamAppIdentityJSON,
	}
}

// restartInput3050 decodes the recorded wire variables. Every field of the
// mutation's input is required (no omitempty): a nil here catches both an
// omitted key and an explicit null — the shape that would silently clear.
func restartInput3050(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var v struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode request variables %s: %v", raw, err)
	}
	if v.Input == nil {
		t.Fatalf("no input object on the wire: %s", raw)
	}
	return v.Input
}

func TestSessionRestart3050Success(t *testing.T) {
	r := runTeamTaskFactory(t, restartResponses3050(restartSuccess3050),
		"team", "session", "restart", "Kit", "--reason", "unanswered 40m, no prompt, idle terminal")
	if r.Err != nil {
		t.Fatalf("success refused: %v", r.Err)
	}
	var call *attnCall
	for i := range *r.Calls {
		if (*r.Calls)[i].Op == "TeamSessionRestart" {
			call = &(*r.Calls)[i]
		}
	}
	if call == nil {
		t.Fatal("the restart operation was not called")
	}
	if call.Session != "s-new" {
		t.Fatalf("the restart must act through the bound session, got %q", call.Session)
	}
	in := restartInput3050(t, call.RawVars)
	if in["appRef"] != "capp100000000000000000000" {
		t.Fatalf("appRef must come from the binding, got %v", in["appRef"])
	}
	if in["reason"] != "unanswered 40m, no prompt, idle terminal" {
		t.Fatalf("reason must be passed exactly as typed, got %v", in["reason"])
	}
	if in["worker"] != "Kit" {
		t.Fatalf("worker must be the ref as passed, got %v", in["worker"])
	}
	stdout := r.Out.String()
	for _, want := range []string{
		// The scope line lands BEFORE the receipt and names the branch that
		// answered — the binding, here.
		"app: hrn:app:acme.com:eng-team — Eng Team (from the worktree binding)",
		"✓ restarted Kit: ended 2 session(s), start issued as operation op-42",
		"  host host-7",
		"  task task-9 — the start carries the worker's oldest open task",
		"  ended session sess-a",
		"  ended session sess-b",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("receipt missing %q:\n%s", want, stdout)
		}
	}
	if strings.Index(stdout, "app: ") > strings.Index(stdout, "✓ restarted") {
		t.Fatalf("the scope line must land before the receipt:\n%s", stdout)
	}
}

func TestSessionRestart3050TasklessRendersNullAndEmpty(t *testing.T) {
	r := runTeamTaskFactory(t, restartResponses3050(restartTaskless3050),
		"team", "session", "restart", "hrn:worker:acme.com:eng:kit", "--reason", "unanswered 40m", "--json")
	if r.Err != nil {
		t.Fatalf("taskless restart refused: %v", r.Err)
	}
	stdout := r.Out.String()
	// Pretty-printed (two-space indent), so the literal includes the space.
	if !strings.Contains(stdout, `"taskId": null`) || !strings.Contains(stdout, `"endedSessionIds": []`) {
		t.Fatalf("--json must render null taskId and empty (never null) endedSessionIds:\n%s", stdout)
	}
	var dto struct {
		Worker          string   `json:"worker"`
		EndedSessionIds []string `json:"endedSessionIds"`
		HostID          string   `json:"hostId"`
		OperationID     string   `json:"operationId"`
		TaskID          *string  `json:"taskId"`
	}
	if err := json.Unmarshal([]byte(stdout), &dto); err != nil {
		t.Fatalf("stdout is not the DTO: %v\n%s", err, stdout)
	}
	if dto.TaskID != nil {
		t.Fatalf("taskId must be null when the worker has no open task: %v", *dto.TaskID)
	}
	if dto.EndedSessionIds == nil || len(dto.EndedSessionIds) != 0 {
		t.Fatalf("endedSessionIds must be [], not null: %v", dto.EndedSessionIds)
	}
	if dto.Worker != "hrn:worker:acme.com:eng:kit" || dto.HostID != "host-7" || dto.OperationID != "op-43" {
		t.Fatalf("wrong DTO: %+v", dto)
	}
	// The scope line is render-only: --json never issues the decorating read.
	for _, c := range *r.Calls {
		if c.Op == "TeamAppIdentity" {
			t.Fatal("--json must not issue the app-label decorating read")
		}
	}
}

func TestSessionRestart3050ReceiptSaysTaskless(t *testing.T) {
	r := runTeamTaskFactory(t, restartResponses3050(restartTaskless3050),
		"team", "session", "restart", "Kit", "--reason", "unanswered 40m")
	if r.Err != nil {
		t.Fatalf("taskless restart refused: %v", r.Err)
	}
	if !strings.Contains(r.Out.String(), "  task none — the worker has no open task; the start is taskless") {
		t.Fatalf("receipt must say the start is taskless:\n%s", r.Out.String())
	}
}

func TestSessionRestart3050RefusalExits(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{
		// The grant refusal is the authority class (8), like the other
		// _GRANT_REQUIRED doors; it reads nothing about the target.
		{"RESTART_GRANT_REQUIRED", exitcode.Forbidden},
		// The gone target rides _NOT_FOUND (4), both spellings.
		{"WORKER_NOT_FOUND", exitcode.NotFound},
		{"APP_NOT_FOUND", exitcode.NotFound},
		// The target's and the App's state: conflict (5), never the default 1.
		{"WORKER_LIVE", exitcode.Conflict},
		{"NO_OPEN_SESSION", exitcode.Conflict},
		{"WORKER_NOT_HELD", exitcode.Conflict},
		{"WORKER_RETIRED", exitcode.Conflict},
		{"WORKER_START_PENDING", exitcode.Conflict},
		{"NO_LIVE_HOST", exitcode.Conflict},
		{"TEAM_PAUSED", exitcode.Conflict},
		{"WORKER_STOPPED", exitcode.Conflict},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := runTeamTaskFactory(t, restartResponses3050(gqlErrorJSON(tc.code)),
				"team", "session", "restart", "Kit", "--reason", "unanswered 40m, no prompt, idle terminal")
			if got := exitOf(r.Err); got != tc.want {
				t.Fatalf("%s exits %d, want %d (err %v)", tc.code, got, tc.want, r.Err)
			}
			// The refusal's message is the server's sentence (rendered by
			// renderFailure in the binary; asserted on the error here).
			if r.Err == nil || !strings.Contains(r.Err.Error(), tc.code) {
				t.Fatalf("%s must surface the server's own sentence, got %v", tc.code, r.Err)
			}
		})
	}
}

func TestSessionRestart3050RequiresReason(t *testing.T) {
	r := runTeamTaskFactory(t, restartResponses3050(restartSuccess3050),
		"team", "session", "restart", "Kit")
	if got := exitOf(r.Err); got != exitcode.Usage {
		t.Fatalf("missing --reason exits %d, want usage 2 (err %v)", got, r.Err)
	}
	if r.Err == nil || !strings.Contains(r.Err.Error(), "--reason is required") {
		t.Fatalf("the refusal must say what is missing: %v", r.Err)
	}
	if len(*r.Calls) != 0 {
		t.Fatalf("a refused input must not reach the server: %v", *r.Calls)
	}
}

// An empty git dir with no binding file: there is no unbound spelling (S4a),
// and the refusal points at `team session start`, not at a --session escape.
func TestSessionRestart3050NeedsABoundSession(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, restartResponses3050(restartSuccess3050))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "session", "restart", "Kit", "--reason", "unanswered 40m"})
	err := root.Execute()
	if got := exitOf(err); got != exitcode.NotFound {
		t.Fatalf("no binding exits %d, want 4 (err %v)", got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "no active session in this worktree") || !strings.Contains(err.Error(), "team session start") {
		t.Fatalf("the refusal must point at the bind: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must be sent without a bound session: %v", *calls)
	}
}

// --app wins over the binding's recorded App id, like every App-addressed
// team command.
func TestSessionRestart3050ExplicitAppWins(t *testing.T) {
	r := runTeamTaskFactory(t, restartResponses3050(restartSuccess3050),
		"team", "session", "restart", "Kit", "--reason", "r", "--app", "acme.com:eng")
	if r.Err != nil {
		t.Fatalf("explicit --app restart refused: %v", r.Err)
	}
	for _, c := range *r.Calls {
		if c.Op != "TeamSessionRestart" {
			continue
		}
		in := restartInput3050(t, c.RawVars)
		if in["appRef"] != "hrn:app:acme.com:eng" {
			t.Fatalf("--app must win over the binding's app id (canonicalized, as every team command sends it), got %v", in["appRef"])
		}
		return
	}
	t.Fatal("the restart operation was not called")
}

// The GATE's rule (hadron-cli#3050, from Vera's FIT): help and agentic-usage
// name "the worker a team dedicates to manage its agents" or "a worker
// holding the grant", never a team role name (Holger, 2026-11-22).
func TestSessionRestart3050HelpNamesNoRole(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	var out strings.Builder
	// Help goes to the command's own streams, not the factory's, so the
	// assertion reads exactly what cobra rendered (the 1054 harness's rule).
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"team", "session", "restart", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, banned := range []string{"coordinator", "cli-engineer", "manager role"} {
		if strings.Contains(strings.ToLower(text), banned) {
			t.Fatalf("help names a team role (%q); the rule says the dedicated worker or the grant holder:\n%s", banned, text)
		}
	}
	for _, want := range []string{"DARK", "--reason", "RESTART_GRANT_REQUIRED", "worker.session.restart"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help missing %q:\n%s", want, text)
		}
	}
}

// ---- hadron-cli#3058: the follow-up pins on #3056's branches ---------------

// restartCalls3058 counts the restart mutation among the recorded calls.
func restartCalls3058(calls []attnCall) int {
	n := 0
	for _, c := range calls {
		if c.Op == "TeamSessionRestart" {
			n++
		}
	}
	return n
}

// A blank --reason or a blank worker is refused locally with the usage class
// BEFORE any request, and the refusal names only the flag: it discloses nothing
// about the target (gate decision 1). The reason that is sent is the trimmed one.
func TestSessionRestart3058BlankInputRefusedLocally(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"whitespace reason", []string{"Kit", "--reason", "   \t "}, "--reason is required"},
		{"empty reason", []string{"Kit", "--reason", ""}, "--reason is required"},
		{"blank worker", []string{"  ", "--reason", "unanswered 40m"}, "worker"},
		{"empty worker", []string{"", "--reason", "unanswered 40m"}, "worker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runTeamTaskFactory(t, restartResponses3050(restartSuccess3050),
				append([]string{"team", "session", "restart"}, tc.argv...)...)
			if got := exitOf(r.Err); got != exitcode.Usage {
				t.Fatalf("exits %d, want usage 2 (err %v)", got, r.Err)
			}
			if r.Err == nil || !strings.Contains(r.Err.Error(), tc.want) {
				t.Fatalf("the refusal must name %q: %v", tc.want, r.Err)
			}
			if n := len(*r.Calls); n != 0 {
				t.Fatalf("a blank input must reach the server zero times, got %d calls: %v", n, *r.Calls)
			}
			if strings.Contains(r.Out.String(), "restarted") {
				t.Fatalf("a refused input must print no receipt:\n%s", r.Out.String())
			}
		})
	}
}

func TestSessionRestart3058ReasonIsSentTrimmed(t *testing.T) {
	r := runTeamTaskFactory(t, restartResponses3050(restartSuccess3050),
		"team", "session", "restart", "Kit", "--reason", "  unanswered 40m \n")
	if r.Err != nil {
		t.Fatalf("refused: %v", r.Err)
	}
	for _, c := range *r.Calls {
		if c.Op == "TeamSessionRestart" {
			if got := restartInput3050(t, c.RawVars)["reason"]; got != "unanswered 40m" {
				t.Fatalf("the reason sent must be the trimmed one, got %q", got)
			}
			return
		}
	}
	t.Fatal("the restart operation was not called")
}

// The documented contract is taskId null in --json when the worker has no open
// task. A server that answers an empty string must not leak "" onto the wire
// (the text receipt already treats "" as none).
func TestSessionRestart3058EmptyTaskIdRendersNullInJSON(t *testing.T) {
	empty := `{"data":{"restartWorkerSession":{"endedSessionIds":[],"hostId":"host-7","operationId":"op-44","taskId":""}}}`
	r := runTeamTaskFactory(t, restartResponses3050(empty),
		"team", "session", "restart", "Kit", "--reason", "unanswered 40m", "--json")
	if r.Err != nil {
		t.Fatalf("refused: %v", r.Err)
	}
	if !strings.Contains(r.Out.String(), `"taskId": null`) {
		t.Fatalf("an empty taskId must render null in --json:\n%s", r.Out.String())
	}
	// Text keeps the "task none" line (gate decision 2: do not change it).
	rt := runTeamTaskFactory(t, restartResponses3050(empty),
		"team", "session", "restart", "Kit", "--reason", "unanswered 40m")
	if rt.Err != nil || !strings.Contains(rt.Out.String(), "  task none — the worker has no open task; the start is taskless") {
		t.Fatalf("text must keep the task none line (err %v):\n%s", rt.Err, rt.Out.String())
	}
}

// The request was sent, so a null result is an UNKNOWN outcome: exit 1, never a
// success receipt, never a second request.
func TestSessionRestart3058NullResultIsOutcomeUnknown(t *testing.T) {
	r := runTeamTaskFactory(t, restartResponses3050(`{"data":{"restartWorkerSession":null}}`),
		"team", "session", "restart", "Kit", "--reason", "unanswered 40m")
	if got := exitOf(r.Err); got != exitcode.Error {
		t.Fatalf("a null result exits %d, want 1 (err %v)", got, r.Err)
	}
	if r.Err == nil || !strings.Contains(r.Err.Error(), "outcome unknown") {
		t.Fatalf("the refusal must say the outcome is unknown: %v", r.Err)
	}
	if strings.Contains(r.Out.String(), "restarted") {
		t.Fatalf("an unknown outcome must print no success receipt:\n%s", r.Out.String())
	}
	if n := restartCalls3058(*r.Calls); n != 1 {
		t.Fatalf("the restart must be sent exactly once, never retried: %d (%v)", n, *r.Calls)
	}
}

// After a successful restart the scope line's decorating read is best-effort: a
// failing read degrades to the bare App ref and the receipt still prints, exit 0.
func TestSessionRestart3058DegradedScopeReadStillPrintsReceipt(t *testing.T) {
	r := runTeamTaskFactory(t, map[string]string{
		"TeamSessionRestart": restartSuccess3050,
		"TeamAppIdentity":    gqlErrorJSON("INTERNAL"),
	}, "team", "session", "restart", "Kit", "--reason", "unanswered 40m")
	if r.Err != nil {
		t.Fatalf("a failed decorating read must not fail a restart that happened: %v", r.Err)
	}
	out := r.Out.String()
	if !strings.Contains(out, "app: capp100000000000000000000 (from the worktree binding)") {
		t.Fatalf("the scope line must degrade to the bare ref:\n%s", out)
	}
	if !strings.Contains(out, "✓ restarted Kit: ended 2 session(s), start issued as operation op-42") {
		t.Fatalf("the receipt must still print:\n%s", out)
	}
}

// A binding made against ANOTHER server is refused with the usage class before
// anything is sent: its session id means nothing there.
func TestSessionRestart3058BindingServerMismatchRefused(t *testing.T) {
	dir := teamGitDir(t)
	other := strings.Replace(bindingFixture, `"startedAt"`, `"server":"https://elsewhere.example","startedAt"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, calls := attnServer(t, restartResponses3050(restartSuccess3050))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "session", "restart", "Kit", "--reason", "unanswered 40m"})
	err := root.Execute()
	if got := exitOf(err); got != exitcode.Usage {
		t.Fatalf("a cross-server binding exits %d, want usage 2 (err %v)", got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "https://elsewhere.example") || !strings.Contains(err.Error(), "--server") {
		t.Fatalf("the refusal must name the bound server and the --server fix: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must be sent on a cross-server binding: %v", *calls)
	}
	if strings.Contains(out.String(), "restarted") {
		t.Fatalf("no receipt on a refusal:\n%s", out.String())
	}
}
