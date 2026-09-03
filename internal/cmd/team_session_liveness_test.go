package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #550 — `session start`'s TAKEN pre-flight reads DERIVED LIVENESS, never an
// open session row.
//
// The defect it closes is a refusal that CANNOT BE SATISFIED. The pre-flight
// keyed on `endedAt == nil`, and hadron-server#1114 retired the inactivity
// reaper: nothing ends an abandoned DEVELOPER session but an explicit
// `endSession`, so that condition is PERMANENT. The refusal could never clear,
// waiting could never help, and the server's own derived gate — which would
// have admitted the bind, because the name is no longer live — was never
// reached, because the client returned first. The user was pushed to --force
// for a takeover that takes nothing from anybody.
//
// Three cases, and they are three because liveness has three answers. The
// third is the one that needed deciding rather than coding.

// LIVE → refuse, and say LIVE.
//
// The wording is asserted, not just the exit code, because the wording is half
// the bug: a refusal explaining itself with "its worker session is still open"
// teaches exactly the conflation #549 spent seven review rounds removing from
// this repo's prose, and it would have taught it from the one surface a driver
// meets at the moment they are trying to act.
func TestTeamSessionStartLiveRefusesAndNamesLiveness(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + withLiveness(irisWorkerJSON, "true") + `}}`,
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()

	if code := exitCodeFor(err); code != exitcode.Conflict {
		t.Fatalf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	for _, want := range []string{"is live", "DRIVEN RECENTLY", "--force takes over", "u-holger"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry %q: %v", want, err)
		}
	}
	// The retired justification, in the two spellings it had. Neither may come
	// back: both explain the refusal by the fact that no longer causes it.
	for _, forbidden := range []string{"still open, which a closed chat session", "worker session is still open"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("the refusal must not explain itself with an OPEN session (%q): %v", forbidden, err)
		}
	}
	if _, called := captured["StartTeamSession"]; called {
		t.Error("a live worker must be refused client-side, without reaching the server")
	}
}

// NOT LIVE with an OPEN session → BIND. This is the regression itself.
//
// The fixture is the exact post-#1114 shape the old pre-flight refused forever:
// `hasLiveSession: false` beside a session row with `endedAt: null`. Under the
// old condition this exited 5 and no amount of waiting changed it.
func TestTeamSessionStartNotLiveBindsOverAnOpenSession(t *testing.T) {
	dir := teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + withLiveness(irisWorkerJSON, "false") + `}}`,
		"TeamSessions":     `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	stderr := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a name that is not live must bind without --force: %v", err)
	}
	raw, called := captured["StartTeamSession"]
	if !called {
		t.Fatal("the bind must reach the server")
	}
	// NO force in the input. Binding a free name is not an override, and
	// sending one would relabel the ordinary case as a takeover in the
	// provenance record — the server reads this field, not our narration.
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(raw, &vars)
	if _, present := vars.Input["force"]; present {
		t.Errorf("binding a name that is not live must not send force: %v", vars.Input)
	}
	// Nothing was taken over, and --json says so rather than inheriting the
	// open row's answer.
	if !strings.Contains(out.String(), `"tookOver": false`) {
		t.Errorf("tookOver must be false when the name was not live: %s", out.String())
	}
	// The open row is REPORTED — it is somebody's unfinished stint and this
	// bind leaves it alone — but not as a takeover.
	note := stderr()
	for _, want := range []string{"not live", "still open", "does not end it", "s-old"} {
		if !strings.Contains(note, want) {
			t.Errorf("stderr must account for the open session (%q): %s", want, note)
		}
	}
	if strings.Contains(note, "taking over") {
		t.Errorf("binding a name that is not live is not a takeover: %s", note)
	}
	if _, err := os.Stat(filepath.Join(dir, "hadron-team-session.json")); err != nil {
		t.Errorf("binding not written: %v", err)
	}
}

// MASKED → DEFER TO THE SERVER. The decision the issue asked for, and the only
// one of the three that is a choice rather than a consequence.
//
// `hasLiveSession: null` means the caller did not pass the worker read gate, so
// liveness was WITHHELD — not answered "no". Refusing on that absence would
// refuse somebody the server would have admitted, on a fact they were never
// shown, and would make --force the routine path for everyone outside the gate,
// which is how an override stops meaning anything. Nothing is unguarded: the
// bind that follows meets the same predicate, atomically. (Holger, 2026-09-03.)
func TestTeamSessionStartMaskedLivenessDefersToTheServer(t *testing.T) {
	teamGitDir(t)
	// The BARE fixture is the masked row — irisWorkerJSON carries no
	// hasLiveSession at all, which is how the seven tests #550 broke came to
	// test this branch while believing they tested TAKEN.
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	stderr := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("masked liveness must defer to the server, not refuse: %v", err)
	}
	if _, called := captured["StartTeamSession"]; !called {
		t.Fatal("masked liveness must reach the server — it is the gate")
	}
	// NULL, not false. `tookOver: false` here would be a claim about a fact the
	// server declined to show us, which is the shape releaseResultDTO's WasHeld
	// and Forced already refuse to make.
	if !strings.Contains(out.String(), `"tookOver": null`) {
		t.Errorf("tookOver must be null when liveness was masked: %s", out.String())
	}
	// And the note may not assert either way.
	note := stderr()
	if !strings.Contains(note, "not visible to you") {
		t.Errorf("stderr must say liveness was withheld: %s", note)
	}
	for _, forbidden := range []string{"not live", "taking over"} {
		if strings.Contains(note, forbidden) {
			t.Errorf("a masked row must not be described as %q: %s", forbidden, note)
		}
	}
}

// Masked and the name IS live: the server refuses and the CLI renders it. The
// deferral's safety argument in one test, since the paragraph above is only
// true if this path actually produces the refusal it promises.
func TestTeamSessionStartMaskedLivenessRendersServerTakenRefusal(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions": `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"errors":[{"message":"That worker is taken.",
			"extensions":{"code":"WORKER_TAKEN","workerId":"wkr1","sessionId":"s-old",
			"lastDriver":"u-dara","lastSeenAt":"2026-09-03T08:00:00Z"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()

	if code := exitCodeFor(err); code != exitcode.Conflict {
		t.Fatalf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	for _, want := range []string{"u-dara", "s-old", "--force takes over"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the server's refusal must carry %q: %v", want, err)
		}
	}
}

// A live name whose session list is UNREADABLE still refuses, and the refusal
// still names a driver rather than dereferencing a nil row.
//
// Liveness and the session list are two separate reads, so they can disagree —
// by permission (the sessions query is gated separately from the worker's
// working state) or by a race (the row ended between them). The decision
// belongs to liveness; the session row is narration, and missing narration is
// not a reason to admit a bind.
func TestTeamSessionStartLiveWithNoReadableSessionStillRefuses(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + withLiveness(irisWorkerJSON, "true") + `}}`,
		"TeamSessions": `{"data":{"sessions":[]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()

	if code := exitCodeFor(err); code != exitcode.Conflict {
		t.Fatalf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	// The server's own wording for this, so the two refusals do not describe
	// one situation in two vocabularies.
	if !strings.Contains(err.Error(), "an unknown driver") {
		t.Errorf("an unnameable driver must still be named: %v", err)
	}
	if _, called := captured["StartTeamSession"]; called {
		t.Error("a live worker must not reach the server without --force")
	}
}
