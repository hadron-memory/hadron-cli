package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #487 — HELD and TAKEN are two different refusals and only one of them is
// forceable (cor:agt:020:09). Before this, `session start` knew only the
// second: WORKER_HELD had no extractor, no message and no exit-code mapping,
// so a driver who met a hold got a generic exit 1 and a --help that spends a
// paragraph recommending the one flag the spec says cannot work.
//
// Every fixture here carries an EXPLICIT hold. The shared irisWorkerJSON has
// no heldByUserId at all, so a test built on it exercises the unheld branch
// while appearing to test holding — the same shape as the omitempty fixture
// that made six of last stint's guards unable to fail.

// searchUser renders a user row for the holder lookup (decoration only: the
// refusal must stand when this read fails, which the reduced-payload case
// below covers).
func heldHolderUserJSON(id, name, handle string) string {
	return `{"data":{"user":{"id":"` + id + `","name":"` + name + `","email":"` + handle +
		`@example.com","handle":"` + handle + `","githubUsername":null,"roles":["MEMBER"]}}}`
}

// The pre-flight case: the name is provably somebody ELSE's, so the refusal
// must not send the driver at --force. This is the misdirection #487 reports
// and #492 recorded as an incident.
func TestTeamSessionStartHeldByAnotherRefusesWithoutOfferingForce(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + heldBy("u-dara") + `}}`,
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"AuthContext":  authContextHolgerJSON, // I am u-holger; the hold is u-dara's
		"GetUser":      heldHolderUserJSON("u-dara", "Dara", "dara"),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()

	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	if err == nil {
		t.Fatal("a name held by someone else must refuse")
	}
	for _, want := range []string{"@dara", "held", "cor:agt:020:09", "worker cast", "worker release"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry %q: %v", want, err)
		}
	}
	// The mutation guard for the whole change: the TAKEN wording must NOT be
	// what a held name gets. Asserting on the absence of the substring
	// "--force" alone would be wrong — the refusal names the flag in order to
	// rule it OUT — so this pins the offer, not the mention.
	for _, forbidden := range []string{"--force takes over", "--force replaces"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("a held name must not be offered a takeover (%q): %v", forbidden, err)
		}
	}
	if !strings.Contains(err.Error(), "--force does NOT apply") {
		t.Errorf("the refusal must rule --force out explicitly: %v", err)
	}
	if _, called := captured["StartTeamSession"]; called {
		t.Error("StartTeamSession must not run for a name held by someone else")
	}
}

// PR #511 review, Codex P2 and Copilot independently: a remedy is only a
// remedy if the caller can RUN it as written. `--as hrn:worker:…` with no
// ambient App is a supported path — the one `--as`'s own help recommends for
// scripts — and in it a bare NAME resolves to nothing (cor:agt:020:02), so
// `worker release <name>` would answer not-found and `worker cast` would
// refuse "no team App". The reader most likely to hit this refusal is the
// one the advice would have failed.
func TestTeamSessionStartHeldRemedyIsRunnableWithoutAppScope(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + heldBy("u-dara") + `}}`,
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"AuthContext":  authContextHolgerJSON,
		"GetUser":      heldHolderUserJSON("u-dara", "Dara", "dara"),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "hrn:worker:acme.com:eng-team:iris", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a name held by someone else must refuse")
	}
	// The release command carries the App-independent URN, never the name.
	if !strings.Contains(err.Error(), "worker release hrn:worker:acme.com:eng-team:iris") {
		t.Errorf("release must be spelled with an App-independent ref: %v", err)
	}
	if strings.Contains(err.Error(), "worker release Iris") {
		t.Errorf("release must not be spelled with a bare name: %v", err)
	}
	// `cast` has no App-independent spelling — a worker that does not exist
	// yet cannot be addressed — so it must name --app explicitly instead.
	if !strings.Contains(err.Error(), "worker cast --app <app>") {
		t.Errorf("cast must name the App scope it requires: %v", err)
	}
}

// A worker whose App URN predates grammar v2 has NO urn, and the remedy still
// has to be runnable — so the ref falls back to the id, which also resolves
// with no App scope. Without this the nil URN would render "release <nil>" or
// silently fall back to the name the case above rules out.
func TestTeamSessionStartHeldRemedyFallsBackToIDWhenURNIsNull(t *testing.T) {
	teamGitDir(t)
	noURN := strings.Replace(heldBy("u-dara"),
		`"urn":"hrn:worker:acme.com:eng-team:iris"`, `"urn":null`, 1)
	if !strings.Contains(noURN, `"urn":null`) {
		t.Fatal("fixture did not lose its urn — the test would prove nothing")
	}
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + noURN + `}}`,
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"AuthContext":  authContextHolgerJSON,
		"GetUser":      heldHolderUserJSON("u-dara", "Dara", "dara"),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a name held by someone else must refuse")
	}
	if !strings.Contains(err.Error(), "worker release wkr1") {
		t.Errorf("a URN-less worker must fall back to its id: %v", err)
	}

	// An EMPTY urn is the other absent shape, and it needs its own case: a
	// nil-only guard passes the null test above and then renders
	// "worker release " — a remedy with no argument. Absent is absent
	// whichever way the server spells it.
	emptyURN := strings.Replace(heldBy("u-dara"),
		`"urn":"hrn:worker:acme.com:eng-team:iris"`, `"urn":""`, 1)
	if !strings.Contains(emptyURN, `"urn":""`) {
		t.Fatal("fixture did not get an empty urn — the test would prove nothing")
	}
	gql2, _ := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + emptyURN + `}}`,
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"AuthContext":  authContextHolgerJSON,
		"GetUser":      heldHolderUserJSON("u-dara", "Dara", "dara"),
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql2.URL})
	err2 := root2.Execute()
	if err2 == nil {
		t.Fatal("a name held by someone else must refuse")
	}
	if !strings.Contains(err2.Error(), "worker release wkr1") {
		t.Errorf("an empty urn must fall back to the id, not render a bare command: %v", err2)
	}
}

// The other side of the same branch, and the reason classifyHold is not a
// bool: a hold that is YOUR OWN leaves --force exactly as correct as it was.
// Without this, "never mention --force when heldByUserId is set" would pass
// the test above and silently break the ordinary self-takeover.
func TestTeamSessionStartHeldByMeStillOffersForce(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + heldBy("u-holger") + `}}`,
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"AuthContext":  authContextHolgerJSON, // the hold is mine
		"GetUser":      heldHolderUserJSON("u-holger", "Holger", "holger"),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()

	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	if err == nil || !strings.Contains(err.Error(), "--force takes over") {
		t.Errorf("your own held name is a plain TAKEN refusal, force and all: %v", err)
	}
	// Case-insensitive on purpose: this is a NEGATIVE assertion, and pinning
	// the capitalisation would let the same sentence sail past it in the one
	// spelling the refusal actually uses.
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "cast your own worker") {
		t.Errorf("your own name needs no cast-your-own remedy: %v", err)
	}
	if _, called := captured["StartTeamSession"]; called {
		t.Error("StartTeamSession must not run without --force")
	}
}

// Identity unreadable: the name IS held (that is on the row), but whether it
// is yours is the open part. Unknown must not collapse into either certainty
// — claiming "held by someone else" would refuse a legitimate self-takeover,
// and staying silent would re-offer the flag that cannot work.
func TestTeamSessionStartHeldUnknownWhoseHedges(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + heldBy("u-dara") + `}}`,
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"AuthContext":  `{"errors":[{"message":"nope"}]}`,
		"GetUser":      heldHolderUserJSON("u-dara", "Dara", "dara"),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()

	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	if err == nil {
		t.Fatal("an active session still refuses")
	}
	for _, want := range []string{"@dara", "could not read your own identity", "--force takes over", "cast your own worker"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the hedged refusal must carry %q: %v", want, err)
		}
	}
}

// The server is the authority and closes the race the pre-flight cannot: the
// hold can be claimed between the worker read and the bind. Rendered from the
// EXTENSIONS payload, not the message narration.
func TestTeamSessionStartServerHeldRefusalRenders(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + irisWorkerJSON + `}}`, // unheld at pre-flight
		"TeamSessions": `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"errors":[{"message":"That name is held.",
			"extensions":{"code":"WORKER_HELD","workerId":"wkr1","heldBy":"u-dara",
			"heldByName":"dara","heldAt":"2026-08-20T09:00:00Z"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()

	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	if err == nil {
		t.Fatal("WORKER_HELD must refuse")
	}
	for _, want := range []string{"dara", "since 2026-08-20T09:00:00Z", "Cast your own worker", "--force does NOT apply"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry %q from the extensions payload: %v", want, err)
		}
	}
}

// The SECOND server path raising this code — the compare-and-set inside the
// session-creating transaction — carries only workerId and heldBy: no
// heldByName, no heldAt. A renderer that assumes the richer payload prints a
// half-empty sentence on an ordinary concurrent bind.
func TestTeamSessionStartServerHeldReducedPayloadDegrades(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions": `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"errors":[{"message":"That name is held.",
			"extensions":{"code":"WORKER_HELD","workerId":"wkr1","heldBy":"u-dara"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()

	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	if err == nil {
		t.Fatal("WORKER_HELD must refuse on the race path too")
	}
	// The holder falls back to the raw id — actionable, and honest about what
	// the server sent.
	if !strings.Contains(err.Error(), "u-dara") {
		t.Errorf("the holder must fall back to heldBy: %v", err)
	}
	// No heldAt means no "since" clause at all, rather than an empty one.
	if strings.Contains(err.Error(), "since ") {
		t.Errorf("an absent heldAt must not render a since clause: %v", err)
	}
}

// --force skips the client pre-flight by design, so this is the path an agent
// reaching for the flag actually takes. The server refuses anyway, and what
// comes back must be the held refusal — not a takeover narration.
func TestTeamSessionStartForceDoesNotDefeatAHold(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + heldBy("u-dara") + `}}`,
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"AuthContext":  authContextHolgerJSON,
		"GetUser":      heldHolderUserJSON("u-dara", "Dara", "dara"),
		"StartTeamSession": `{"errors":[{"message":"That name is held.",
			"extensions":{"code":"WORKER_HELD","workerId":"wkr1","heldBy":"u-dara","heldByName":"dara"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--server", gql.URL})
	err := root.Execute()

	if _, called := captured["StartTeamSession"]; !called {
		t.Fatal("--force must still reach the server, which is the authority on a hold")
	}
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	if err == nil || !strings.Contains(err.Error(), "Cast your own worker") {
		t.Errorf("forcing a hold must land on the cast-your-own remedy: %v", err)
	}
}
