package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#914 (Holger R1/R2 over server #1156; cor:dmo:070:05): a call
// through a binding whose session ENDED warns on stderr (session, end, cause,
// rebind) and otherwise runs exactly as before.

// endedFake is a lenient fake server with one session whose end state the
// test controls. Unlisted operations answer an error without failing the test:
// the commands under test treat their best-effort reads as optional.
type endedFake struct {
	mu      sync.Mutex
	endedAt string // "" = open
	reason  string // "" = null
	legacy  bool   // the server predates endReason
	down    bool   // every request fails
	ops     []string
	stubs   map[string]string
	srv     *httptest.Server
}

func newEndedFake(t *testing.T) *endedFake {
	t.Helper()
	fk := &endedFake{stubs: map[string]string{}}
	fk.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			Op string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fk.mu.Lock()
		defer fk.mu.Unlock()
		fk.ops = append(fk.ops, body.Op)
		if fk.down {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`bad gateway`))
			return
		}
		ended := `null`
		if fk.endedAt != "" {
			ended = `"` + fk.endedAt + `"`
		}
		switch body.Op {
		case "GetBoundSessionEnd":
			if fk.legacy {
				_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"endReason\" on type \"Session\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`))
				return
			}
			reason := `null`
			if fk.reason != "" {
				reason = `"` + fk.reason + `"`
			}
			_, _ = w.Write([]byte(`{"data":{"session":{"id":"s-new","endedAt":` + ended + `,"endReason":` + reason + `}}}`))
			return
		case "GetTeamSession":
			s := strings.Replace(startedSessionJSON, `"endedAt":null`, `"endedAt":`+ended, 1)
			_, _ = w.Write([]byte(`{"data":{"session":` + s + `}}`))
			return
		case "EndTeamSession":
			fk.endedAt, fk.reason = "2026-10-17T09:00:00Z", "ended"
			s := strings.Replace(startedSessionJSON, `"endedAt":null`, `"endedAt":"2026-10-17T09:00:00Z"`, 1)
			_, _ = w.Write([]byte(`{"data":{"endSession":` + s + `}}`))
			return
		}
		if resp, ok := fk.stubs[body.Op]; ok {
			_, _ = w.Write([]byte(resp))
			return
		}
		_, _ = w.Write([]byte(`{"errors":[{"message":"not stubbed"}]}`))
	}))
	t.Cleanup(fk.srv.Close)
	return fk
}

// checks counts the notice's own read.
func (fk *endedFake) checks() int {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	n := 0
	for _, o := range fk.ops {
		if o == "GetBoundSessionEnd" {
			n++
		}
	}
	return n
}

// bindWorktree writes a modern binding (it records its server) into a fresh
// git dir, the way `session start` does.
func bindEndedWorktree(t *testing.T, server string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(team.GitDirEnv, dir)
	b := `{"sessionId":"s-new","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
		"agentId":"agt1","appId":"capp100000000000000000000","startedAt":"2026-08-11T10:00:00Z","appBound":true,
		"server":"` + server + `","tool":"claude-code","repo":"hadron-memory/hadron-cli","prNumbers":[]}`
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runBound(t *testing.T, fk *endedFake, args ...string) (string, string, error) {
	t.Helper()
	f, out := testFactory(t)
	errOut := f.IOStreams.ErrOut.(*strings.Builder)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", fk.srv.URL))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

// The issue's acceptance case: two worktrees legitimately share one session
// (a no---as resume binds the same session in a second tree). One ends it with
// a handoff; the other's NEXT call through its binding warns, and still runs.
func TestEndedBindingSharedResumeTheOtherTreeWarns(t *testing.T) {
	fk := newEndedFake(t)
	treeA := bindEndedWorktree(t, fk.srv.URL)
	treeB := t.TempDir()
	data, _ := os.ReadFile(filepath.Join(treeA, "hadron-team-session.json"))
	if err := os.WriteFile(filepath.Join(treeB, "hadron-team-session.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Tree A ends the shared session, with a handoff.
	if _, _, err := runBound(t, fk, "team", "session", "end", "--handoff", "all landed"); err != nil {
		t.Fatalf("end: %v", err)
	}

	// Tree B's next ordinary call through its binding.
	t.Setenv(team.GitDirEnv, treeB)
	before, _ := os.ReadFile(filepath.Join(treeB, "hadron-team-session.json"))
	out, errOut, err := runBound(t, fk, "team", "session", "whoami", "--json")
	if err != nil {
		t.Fatalf("the call must still run: %v", err)
	}
	for _, want := range []string{"warning:", "s-new", "Iris", "2026-10-17T09:00:00Z", "cause: ended", "hadron team session start --as wkr1"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr must contain %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "--force") && !strings.Contains(errOut, "no --force") {
		t.Errorf("own recovery must never need --force:\n%s", errOut)
	}
	var dto map[string]any
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
		t.Fatalf("stdout must stay parseable JSON: %v\n%s", jerr, out)
	}
	if strings.Contains(out, "warning:") {
		t.Error("the notice belongs on stderr, never stdout")
	}
	after, _ := os.ReadFile(filepath.Join(treeB, "hadron-team-session.json"))
	if string(before) != string(after) {
		t.Errorf("the binding must not be rewritten:\nbefore %s\nafter  %s", before, after)
	}
}

// Every closed cause is shown as the server recorded it; a legacy end (null
// reason, or an older server) is "unknown", never guessed.
func TestEndedBindingShowsTheRecordedCause(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		legacy bool
		want   string
	}{
		{"ended", "ended", false, "cause: ended"},
		{"superseded", "superseded", false, "cause: superseded"},
		{"expired", "expired", false, "cause: expired"},
		{"legacy unknown", "unknown", false, "cause: unknown"},
		{"null reason", "", false, "cause: unknown"},
		{"older server", "", true, "cause: unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fk := newEndedFake(t)
			fk.endedAt, fk.reason, fk.legacy = "2026-10-17T09:00:00Z", c.reason, c.legacy
			bindEndedWorktree(t, fk.srv.URL)
			_, errOut, err := runBound(t, fk, "team", "session", "whoami")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(errOut, c.want) || !strings.Contains(errOut, "2026-10-17T09:00:00Z") {
				t.Errorf("want %q with the end time:\n%s", c.want, errOut)
			}
		})
	}
}

// An open session (live or lapsed) gets no notice at all.
func TestEndedBindingSilentWhileTheSessionIsOpen(t *testing.T) {
	fk := newEndedFake(t)
	bindEndedWorktree(t, fk.srv.URL)
	_, errOut, err := runBound(t, fk, "team", "session", "whoami")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "warning:") || strings.Contains(errOut, "could not check") {
		t.Errorf("an open session must get no notice:\n%s", errOut)
	}
	if fk.checks() != 1 {
		t.Errorf("the check runs once: %d", fk.checks())
	}
}

// A check that cannot complete says so, never infers an end, and leaves the
// command's own result alone.
func TestEndedBindingUnreachableServerSaysItCouldNotCheck(t *testing.T) {
	fk := newEndedFake(t)
	fk.down = true
	bindEndedWorktree(t, fk.srv.URL)
	_, errOut, err := runBound(t, fk, "team", "session", "whoami", "--json")
	if err != nil {
		t.Fatalf("whoami is local and must still answer: %v", err)
	}
	if !strings.Contains(errOut, "could not check") || strings.Contains(errOut, "warning:") {
		t.Errorf("want the could-not-check note and no end claimed:\n%s", errOut)
	}
}

// A binding for another server, or a legacy one that recorded none, is never
// asked about: its session id means nothing here.
func TestEndedBindingNeverAsksAboutAnotherServersSession(t *testing.T) {
	fk := newEndedFake(t)
	bindEndedWorktree(t, "https://elsewhere.example")
	_, errOut, _ := runBound(t, fk, "team", "session", "whoami")
	if fk.checks() != 0 {
		t.Error("another server's session must not be looked up here")
	}
	if !strings.Contains(errOut, "could not check") {
		t.Errorf("say the check was not possible:\n%s", errOut)
	}
}

// The original command's failure and exit code are its own: the notice
// changes neither. A refused work record still exits as it would have.
func TestEndedBindingKeepsTheCommandsOwnExit(t *testing.T) {
	refused := `{"errors":[{"message":"session ended","extensions":{"code":"SESSION_ENDED"}}]}`
	run := func(ended bool) (int, string) {
		fk := newEndedFake(t)
		if ended {
			fk.endedAt, fk.reason = "2026-10-17T09:00:00Z", "ended"
		}
		fk.stubs["UpdateTeamSession"] = refused
		fk.stubs["RecordTeamWork"] = refused
		bindEndedWorktree(t, fk.srv.URL)
		_, errOut, err := runBound(t, fk, "team", "session", "log", "--pr", "371")
		return exitCodeFor(err), errOut
	}
	openCode, _ := run(false)
	endedCode, errOut := run(true)
	if openCode != endedCode {
		t.Errorf("exit with the notice = %d, without = %d: the notice must not change it", endedCode, openCode)
	}
	if openCode == exitcode.OK {
		t.Fatalf("premise: the refused record must fail (exit %d)", openCode)
	}
	if !strings.Contains(errOut, "warning:") {
		t.Errorf("the refused call still gets the notice:\n%s", errOut)
	}
	// The notice lands BEFORE the call's own refusal.
	if i, j := strings.Index(errOut, "warning:"), strings.Index(errOut, "session ended"); j >= 0 && i > j {
		t.Errorf("the notice must precede the refusal:\n%s", errOut)
	}
}

// An authored write through the binding (not a team command) warns too.
func TestEndedBindingAuthoredWriteWarns(t *testing.T) {
	fk := newEndedFake(t)
	fk.endedAt, fk.reason = "2026-10-17T09:00:00Z", "superseded"
	fk.stubs["CreateComment"] = `{"errors":[{"message":"refused"}]}`
	bindEndedWorktree(t, fk.srv.URL)
	_, errOut, _ := runBound(t, fk, "comment", "create", "hrn:node:acme.com:eng:a", "--body", "hi")
	if !strings.Contains(errOut, "cause: superseded") {
		t.Errorf("an authored write through the binding must warn:\n%s", errOut)
	}
	if fk.checks() != 1 {
		t.Errorf("once per command: %d", fk.checks())
	}
}

// A plain read outside the team group never consumes the binding, so it pays
// no check.
func TestEndedBindingPlainReadPaysNothing(t *testing.T) {
	fk := newEndedFake(t)
	fk.endedAt = "2026-10-17T09:00:00Z"
	fk.stubs["GetNodeByLoc"] = `{"errors":[{"message":"nope"}]}`
	bindEndedWorktree(t, fk.srv.URL)
	_, errOut, _ := runBound(t, fk, "node", "get", "hrn:node:acme.com:eng:a")
	if fk.checks() != 0 || strings.Contains(errOut, "warning:") {
		t.Errorf("a read that sends no session must not check: %v\n%s", fk.ops, errOut)
	}
}

// The commands that judge the session themselves add no notice of their own.
func TestEndedBindingExemptCommandsDoNotDoubleTheirAnswer(t *testing.T) {
	cases := [][]string{
		{"team", "session", "whoami", "--check"},
		{"team", "session", "end", "--handoff", "x"},
		{"team", "session", "start", "--as", "wkr1"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args[2:], " "), func(t *testing.T) {
			fk := newEndedFake(t)
			fk.endedAt, fk.reason = "2026-10-17T09:00:00Z", "ended"
			fk.stubs["GetWorker"] = `{"data":{"worker":` + irisWorkerJSON + `}}`
			bindEndedWorktree(t, fk.srv.URL)
			t.Setenv("HADRON_LAUNCH_GRANT", "")
			_, errOut, _ := runBound(t, fk, args...)
			if fk.checks() != 0 || strings.Contains(errOut, "warning: this worktree") {
				t.Errorf("%v must not add the notice: ops=%v\n%s", args, fk.ops, errOut)
			}
		})
	}
}
