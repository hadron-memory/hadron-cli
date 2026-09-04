package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #553 — `session start`'s sessions read is NARRATION, so it degrades. The one
// exception is `--force`, where the narration IS the decision.
//
// #552 re-pointed the TAKEN gate at derived liveness, off the worker row, and
// demoted this read to supplying the driver/tool/host a takeover has to show.
// The error handling did not move with it: a failed `sessions` read still
// failed the whole command. That refused a caller who can read the worker but
// not its session list — refused by a dependency that had been removed, which
// is #550 wearing a different hat.

// abortSessionsGraphQL answers `responses` normally, but HANGS UP on the
// `TeamSessions` read without writing a byte — a real transport failure rather
// than a GraphQL error body.
//
// The failing operation is fixed rather than a parameter: every test here is
// about that ONE read losing its answer, and a parameter with a single caller
// invites a reader to assume the helper is general when nothing exercises it
// that way (and golangci-lint's `unparam` says so).
//
// The distinction is the test's subject, not set dressing. Ada's ruling carves
// out the TRANSPORT case, and @Dara measured that `sessions(workerRef:)` has no
// authorization-error path at all: an invisible worker yields `[]`, deliberately,
// so it cannot be used to probe which workers exist. A fixture returning
// `UNAUTHENTICATED` here would exercise a branch the server cannot produce, and
// would pass while proving nothing about the case that actually reaches users.
//
// It records which operations were reached, so a guard that is supposed to
// refuse BEFORE the mutation can be asserted on directly rather than inferred
// from an error string. The record is returned as a FUNCTION, not a map, and
// that is not ceremony: the handler runs on the server's goroutine, and hanging
// up mid-request means the client can observe EOF and the test can march on
// while that goroutine is still writing. `captureGraphQL` gets away with a bare
// map because completing a response orders the two; an aborted one does not.
// `go test -race` fails on the map version — measured, not argued.
func abortSessionsGraphQL(t *testing.T, responses map[string]string) (*httptest.Server, func(string) bool) {
	t.Helper()
	var mu sync.Mutex
	reached := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		// The decode error is CHECKED, unlike the repo's usual `_ = Decode(…)`
		// (@copilot). That idiom is safe where the decoded value feeds a
		// POSITIVE assertion — a zero value fails it — and unsafe here, because
		// this map feeds an ABSENCE assertion: a decode that stopped working
		// would record the empty operation name, leave "StartTeamSession"
		// unreached-looking, and pass the very check that is supposed to catch
		// a bind slipping through. A guard whose failure mode is passing. The
		// same rule is already written a file away, on #552's force-absence
		// check; it did not get applied to this new double.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding the request body: %v — the reached-operation record would be wrong", err)
			return
		}
		mu.Lock()
		reached[body.OperationName] = true
		mu.Unlock()
		if body.OperationName == "TeamSessions" {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Errorf("test server cannot hijack, so the transport failure cannot be simulated")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijacking to drop the connection: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(server.Close)
	return server, func(op string) bool {
		mu.Lock()
		defer mu.Unlock()
		return reached[op]
	}
}

// THE REGRESSION: an unreadable session list must not fail the bind.
//
// `hasLiveSession: false` is a complete answer to the only question the command
// asks, and it arrived on the worker row a line earlier. Nothing downstream of
// it needs the session list.
func TestTeamSessionStartBindsWhenSessionDetailIsUnreadable(t *testing.T) {
	teamGitDir(t)
	gql, _ := abortSessionsGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + withLiveness(irisWorkerJSON, "false") + `}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	stderr := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a failed NARRATION read must not fail the bind: %v", err)
	}
	// Degraded, not silent. The caller is told they got less than usual —
	// otherwise the missing takeover detail later reads as "nobody was there".
	note := stderr()
	for _, want := range []string{"could not read", "session detail", "continuing"} {
		if !strings.Contains(note, want) {
			t.Errorf("stderr must account for the degraded read (%q): %s", want, note)
		}
	}
}

// --force FAILS CLOSED, and this is the one thing here allowed to be fatal.
//
// cor:agt:020:03 makes the override informed or nothing, and after #552
// `--force` is reached ONLY when somebody genuinely is driving — so naming them
// stopped being colour and became the whole content of the decision. A takeover
// that cannot see whom it displaces has nothing left to be informed by.
func TestTeamSessionStartForceRefusesWhenItCannotNameTheDriver(t *testing.T) {
	teamGitDir(t)
	// StartTeamSession is STOCKED, and answers successfully, on purpose.
	//
	// Leaving it out looks stricter and is weaker: the guard's absence would
	// then surface as the fake server's "unexpected operation" error, so the
	// command still fails and `err == nil` — the assertion carrying this test's
	// actual claim — passes vacuously. Found by mutation: deleting the carve-out
	// reddened this test only through the exit-code and wording checks, i.e. for
	// a reason unrelated to the guard. With the bind stocked, removing the guard
	// produces a clean successful takeover, which is the real regression and the
	// thing the assertions below now catch directly.
	gql, reached := abortSessionsGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + withLiveness(irisWorkerJSON, "true") + `}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("--force must refuse when it cannot name the driver it takes over from")
	}
	// Refused BEFORE the write, not after it. The bind is the irreversible half;
	// a guard that lets it through and then reports a failure has taken the name
	// over anyway, which is the outcome cor:agt:020:03 is about.
	if reached("StartTeamSession") {
		t.Error("the refusal must land before the bind, not after it")
	}
	// The EXIT CODE IS PRESERVED, not collapsed to a generic 1 because we
	// wrapped the error in a sentence. 7 is the documented "ask again" class
	// (#394) and a script branching on 1 vs 7 is branching on "the server
	// refused this" vs "the answer never arrived" — which is exactly what
	// happened here. This assertion is the reason the fixture drops the
	// connection instead of returning an error body: a GraphQL error would map
	// to 1, where preserving and collapsing look identical.
	if code := exitCodeFor(err); code != exitcode.Unavailable {
		t.Errorf("exit code = %d, want %d (Unavailable) — the mapped transport code must survive the wrapping; err: %v",
			code, exitcode.Unavailable, err)
	}
	// It must say WHY the override was refused, in terms of the override.
	// "could not reach the server" alone is true and useless: it does not tell
	// the driver that dropping --force may well work.
	for _, want := range []string{"--force", "takes over", "cor:agt:020:03"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry %q: %v", want, err)
		}
	}
	// And it must not have invented a driver to satisfy the clause.
	for _, forbidden := range []string{"an unknown driver", "not permitted to see"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("a failed read must not be reported as a driver it could not identify (%q): %v", forbidden, err)
		}
	}
}

// A SUCCESSFUL read that returns nothing, against a name the server calls LIVE,
// is the MASKED case — and it must say so rather than borrowing the wording for
// a driver who could not be identified.
//
// This is the inference the transport carve-out above pays for. `hasLiveSession`
// is "not ended AND driven inside the window", so when it is true an unended row
// exists; a read that answered and returned none of it is a read the caller was
// not shown. That only follows because the failed read has already been carved
// off — which is why these two tests are a pair, and why deleting either one
// leaves the other asserting something weaker than it appears to.
func TestTeamSessionStartTakeoverNamesMaskingNotAnUnknownDriver(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker": `{"data":{"worker":` + withLiveness(irisWorkerJSON, "true") + `}}`,
		// The read ANSWERS. Empty is a perfectly good answer, and the expected
		// one for a caller outside the session read scope.
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	stderr := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an empty session list must not refuse — it is the normal answer for a masked caller: %v", err)
	}
	if _, called := captured["StartTeamSession"]; !called {
		t.Fatal("the forced bind must reach the server")
	}
	note := stderr()
	if !strings.Contains(note, "taking over") {
		t.Errorf("a forced bind over a live name is a takeover and must say so: %s", note)
	}
	// "Never silently" is satisfied by telling the caller the truth INCLUDING
	// the limit of what they can be told. Naming the masking is not silence;
	// dressing it as an unidentifiable driver is, because it invites the reader
	// to conclude there was nothing to see.
	if !strings.Contains(note, "not permitted to see") {
		t.Errorf("a masked driver must be named as masked: %s", note)
	}
	if strings.Contains(note, "an unknown driver") {
		t.Errorf("a masked driver is not an unknown one — the caller is not permitted to see a driver who IS identified: %s", note)
	}
}

// --force over a NOT-LIVE name still degrades, and this is the case the first
// version of this change got wrong (@codex P2, @copilot, independently).
//
// **`--force` is not only a takeover flag.** It is also what replaces an
// abandoned local worktree binding (`alreadyBoundError` refuses without it), and
// that path has ALREADY ENDED the previous session by the time the narration
// read runs. So gating the fatal read on `force` alone stranded a caller who was
// displacing nobody: old session ended, local binding intact, and no way
// forward — retrying without `--force` hits the already-bound guard, and
// retrying with it hits the abort again until the transport recovers.
//
// A refusal that cannot be satisfied by doing what it implies is the #550 shape,
// and this change is the one that exists to remove it. The gate is now derived
// liveness: the worker row PROVES nobody is being displaced, so there is nothing
// an override would have to name.
func TestTeamSessionStartForceOverANotLiveNameStillDegrades(t *testing.T) {
	teamGitDir(t)
	gql, reached := abortSessionsGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + withLiveness(irisWorkerJSON, "false") + `}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	stderr := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("--force over a name the worker row proves is not live must still bind: %v", err)
	}
	if !reached("StartTeamSession") {
		t.Error("the bind must reach the server — nothing here is being displaced")
	}
	if note := stderr(); !strings.Contains(note, "could not read") {
		t.Errorf("the degraded read must still be reported: %s", note)
	}
}

// MASKED liveness under --force is FATAL, and the direction is the point.
//
// Degrading needs POSITIVE evidence that nobody is displaced. `liveUnknown` is
// the server declining to answer, and "it did not tell me somebody is there" is
// not "nobody is there" — the same rule `workerLiveness` exists to enforce
// three-valuedly. So the gate is `live != liveNo`, not `live == liveYes`: a
// forced bind that MIGHT displace a driver it cannot see is the case
// cor:agt:020:03 refuses.
func TestTeamSessionStartForceWithMaskedLivenessFailsClosed(t *testing.T) {
	teamGitDir(t)
	gql, reached := abortSessionsGraphQL(t, map[string]string{
		// hasLiveSession absent entirely — the masked shape.
		"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a forced bind that may displace a driver it cannot see must refuse")
	}
	if reached("StartTeamSession") {
		t.Error("the refusal must land before the bind")
	}
	if code := exitCodeFor(err); code != exitcode.Unavailable {
		t.Errorf("exit code = %d, want %d (Unavailable); err: %v", code, exitcode.Unavailable, err)
	}
}

// WITHOUT --force, a live name refuses with an answer about the WORKER, not one
// about the network — even when the narration read failed.
//
// The fail-closed gate takes `force` AND `live != liveNo`, and the `force` half
// is not redundant. This bind was going to be refused anyway, with exit 5 and a
// sentence naming liveness; letting the transport error win would hand the
// caller an exit 7 about their connection for a situation that is entirely about
// the worker. That is the mirror of the defect the gate exists to fix, and it is
// the shape #556 fixed once already by hoisting a pure guard above the client
// build — a caller who mistyped a flag should not be told about their session.
//
// Nothing caught this when the `force` condition was briefly dropped while
// restructuring the gate: the suite had no non-force + live + failed-read case.
// It does now.
func TestTeamSessionStartLiveWithoutForceRefusesAboutLivenessNotTransport(t *testing.T) {
	teamGitDir(t)
	gql, reached := abortSessionsGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + withLiveness(irisWorkerJSON, "true") + `}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()

	if code := exitCodeFor(err); code != exitcode.Conflict {
		t.Fatalf("exit code = %d, want %d (Conflict) — a live name is a state conflict, not a transport failure; err: %v",
			code, exitcode.Conflict, err)
	}
	if reached("StartTeamSession") {
		t.Error("a live worker must be refused client-side without --force")
	}
	// The refusal is about the worker, and it names the degraded read honestly
	// rather than inventing a driver for it.
	for _, want := range []string{"is live", "could not be read"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "an unknown driver") {
		t.Errorf("a failed read is not an unidentifiable driver: %v", err)
	}
}

// --force over a name the client PROVED free does not waive the server's atomic
// gate, and the race it protects is surfaced rather than steamrolled.
//
// @codex P1 on #558, and it is round 1's finding one level deeper. `--force` is
// also how an abandoned worktree binding is replaced, so a caller can be holding
// the flag for a reason that has nothing to do with taking over. Forwarding it
// on a `liveNo` read waives `WORKER_TAKEN` for somebody who was told there was
// nothing to waive — and if a driver binds in the window between the worker read
// and `startSession`, the waiver takes the name out from under a driver who
// arrived AFTER our evidence, silently and without naming them.
//
// That is the silence cor:agt:020:03 forbids, reached through a flag passed for
// another purpose. Withholding the override costs one round trip in that race
// and buys the informed path: the server refuses, this command renders the
// refusal WITH the driver, and the retry is an override that knows whom it
// displaces.
func TestTeamSessionStartForceOverAProvenFreeNameDoesNotWaiveTheServerGate(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + withLiveness(irisWorkerJSON, "false") + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("binding a name proven free must succeed: %v", err)
	}
	// The decode error is checked because this asserts an ABSENCE: a decode
	// that stopped working leaves Input nil, the key absent, and the test green
	// on a payload nobody read.
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(captured["StartTeamSession"], &vars); err != nil {
		t.Fatalf("decoding StartTeamSession variables: %v (raw: %s)", err, captured["StartTeamSession"])
	}
	if vars.Input == nil {
		t.Fatalf("no input captured — the absence check would pass vacuously: %s", captured["StartTeamSession"])
	}
	if _, present := vars.Input["force"]; present {
		t.Errorf("a bind over a name the client proved free must not waive the server's atomic gate: %v", vars.Input)
	}
}

// MASKED liveness still forwards the override, and that asymmetry is deliberate.
//
// A masked caller has no evidence either way, and refusing them the override is
// #550's mistake — it makes the flag useless for exactly the population that
// cannot see enough to know whether they need it. `liveNo` is withheld because
// the client has POSITIVE proof; `liveUnknown` is not proof of anything.
func TestTeamSessionStartForceWithMaskedLivenessStillForwardsTheOverride(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a masked forced bind must reach the server: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(captured["StartTeamSession"], &vars); err != nil {
		t.Fatalf("decoding StartTeamSession variables: %v", err)
	}
	if vars.Input["force"] != true {
		t.Errorf("a masked caller must keep the override — refusing it is #550's mistake: %v", vars.Input)
	}
}
