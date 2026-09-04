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

// withLiveness's own contract, pinned (@copilot): it SETS liveness or it
// panics — it never hands back a row it failed to change.
//
// A fixture helper earns a test when a silent no-op in it would make other
// tests assert the wrong branch, which is this whole PR's subject. The
// whitespace case is the one that was live: `"hasLiveSession": false` contains
// the key, so the helper entered its replace path, matched nothing, and
// returned the original.
func TestWithLivenessSetsOrPanics(t *testing.T) {
	for _, tc := range []struct {
		name, in, live, want, gone string
	}{
		{"inserts into a fixture without the key", irisWorkerJSON, "true", `"hasLiveSession":true`, ""},
		{"overwrites an existing value", heldBy("u-dara"), "true", `"hasLiveSession":true`, `"hasLiveSession":false`},
		{"overwrites through whitespace", `{"memoryId":"mw1","hasLiveSession": false}`, "null", `"hasLiveSession":null`, `"hasLiveSession": false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := withLiveness(tc.in, tc.live)
			if !strings.Contains(got, tc.want) {
				t.Errorf("withLiveness did not set %s: %s", tc.want, got)
			}
			// SET, not add. Presence alone cannot tell overwriting from
			// inserting a SECOND key, and Go's decoder takes the LAST — so a
			// helper that fell through to the insert path would satisfy the
			// assertion above while the fixture meant the opposite. Found by
			// mutating this test's own subject: dropping the whitespace
			// tolerance left this case green.
			if tc.gone != "" && strings.Contains(got, tc.gone) {
				t.Errorf("withLiveness left the old value in place — a duplicate key, and the decoder takes the last: %s", got)
			}
		})
	}

	// The two shapes it must refuse rather than pass through.
	for _, tc := range []struct{ name, in string }{
		{"a key it cannot set", `{"memoryId":"mw1","hasLiveSession":"yes"}`},
		{"nowhere to insert", `{"id":"wkr1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("withLiveness must panic rather than return the row unchanged")
				}
			}()
			_ = withLiveness(tc.in, "true")
		})
	}

	// And an unusable `live` argument is refused, not written into the JSON.
	t.Run("rejects a non-JSON liveness value", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("withLiveness must panic on a live value that is not true/false/null")
			}
		}()
		_ = withLiveness(irisWorkerJSON, "TRUE")
	})
}

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
	// LIVENESS LAPSES, and the refusal must say so (@codex P2 on this PR).
	// Derived liveness is recency of driving, so waiting for the window IS a
	// remedy — the opposite of the open-session row, which nothing clears. The
	// first draft carried the row's conclusion over to liveness and told the
	// reader waiting could not help, pushing them at an unnecessary takeover.
	if !strings.Contains(err.Error(), "lapses on its own") {
		t.Errorf("the refusal must say liveness clears by itself: %v", err)
	}
	// Every retired justification, in the spellings it has had. None may come
	// back: each explains the refusal by something that no longer causes it.
	for _, forbidden := range []string{
		"still open, which a closed chat session",
		"worker session is still open",
		"nothing frees this name by waiting",
	} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("the refusal must not carry the retired claim %q: %v", forbidden, err)
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
	//
	// The decode error is CHECKED, unlike the repo's usual `_ = json.Unmarshal`
	// (@copilot). The idiom is safe for a positive assertion — a nil map fails
	// it — and unsafe for this one, which asserts an ABSENCE: a decode that
	// stopped working would leave `Input` nil, the key absent, and the test
	// green on a payload nobody read. A guard whose failure mode is passing.
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("decoding the captured StartTeamSession variables: %v (raw: %s)", err, raw)
	}
	if vars.Input == nil {
		t.Fatalf("no input variable captured — the absence check below would pass vacuously: %s", raw)
	}
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
	// AND IT MUST SAY THE SAME THING THE CLIENT'S DOES (@codex P2 on this PR).
	// This path is NEWLY REACHABLE because masked liveness defers here, so a
	// caller outside the worker read gate meets this wording every time — and
	// it was the one refusal left explaining itself by the session being OPEN,
	// which is the conflation the change exists to remove. One explanation in
	// two places, edited in one: #549's round 6, inside the PR that cites it.
	for _, want := range []string{"is live", "DRIVEN RECENTLY", "lapses on its own"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the server's refusal must be phrased in liveness too (%q): %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "still open, which closing a chat session") {
		t.Errorf("the server's refusal must not explain itself with an OPEN session: %v", err)
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
//
// #553 sharpened what the refusal SAYS without changing that it refuses: an
// empty answer against a live name is the permission case, not the race, so it
// is named as withheld rather than as unknown.
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
	// NAMED AS MASKED, not as unknown — #553's coordinator ruling, and this
	// assertion INVERTED to carry it.
	//
	// It used to require the server's own "an unknown driver", so that the
	// client and server refusals would not describe one situation in two
	// vocabularies. That reasoning was right and its premise has moved: these
	// are TWO situations. The server says "an unknown driver" when IT cannot
	// name the driver; this fixture is a read that ANSWERED against a name the
	// server calls live, which means the row exists and this caller was not
	// shown it. Two vocabularies for two situations is the point, not the
	// defect — the defect was one word for both.
	//
	// The forbidden direction is the load-bearing half: collapsing back to
	// "an unknown driver" is the regression, and it is the comfortable
	// direction to drift in, because the shorter sentence reads fine.
	if !strings.Contains(err.Error(), "not permitted to see") {
		t.Errorf("a driver withheld from this caller must be named as withheld: %v", err)
	}
	if strings.Contains(err.Error(), "an unknown driver") {
		t.Errorf("a masked driver is not an unknown one — the server would name this driver to someone: %v", err)
	}
	if _, called := captured["StartTeamSession"]; called {
		t.Error("a live worker must not reach the server without --force")
	}
}
