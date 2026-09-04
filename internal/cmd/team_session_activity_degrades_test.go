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

// abortOpGraphQL answers `responses` normally, but HANGS UP on failOp without
// writing a byte — a real transport failure rather than a GraphQL error body.
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
func abortOpGraphQL(t *testing.T, failOp string, responses map[string]string) (*httptest.Server, func(string) bool) {
	t.Helper()
	var mu sync.Mutex
	reached := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		reached[body.OperationName] = true
		mu.Unlock()
		if body.OperationName == failOp {
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
	gql, _ := abortOpGraphQL(t, "TeamSessions", map[string]string{
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
	gql, reached := abortOpGraphQL(t, "TeamSessions", map[string]string{
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
