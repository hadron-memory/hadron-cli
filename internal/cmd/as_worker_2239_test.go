package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2239 (the #2236 review, Jonas P3-2 and D-P3-3): the shared App lookup
// normalizes a PURE permission refusal to a missing App, and nothing else.
// A MIXED FORBIDDEN+UNAUTHENTICATED answer, and a failed lookup, keep their
// ordinary mapped exit and the server's own messages, in the order the server
// sent them. The exit for a mixed answer is the FIRST error's code (the
// documented mapping, not a normalization), so it differs by order; what must
// hold, and is pinned here on every caller of the shared helper, is that it
// never collapses into the missing-App refusal and is repeatable for the same
// lookup answer (V58 / cor:agt:050:21, Proposed: a missing App and an
// unreadable one print the same bytes only for the pure refusal).

const (
	lookupForbidden2239    = `{"code":"FORBIDDEN"}`
	lookupUnauth2239       = `{"code":"UNAUTHENTICATED"}`
	mixedForbiddenFirst    = `{"errors":[{"message":"private App exists","extensions":` + lookupForbidden2239 + `},{"message":"sign in again","extensions":` + lookupUnauth2239 + `}]}`
	mixedUnauthFirst       = `{"errors":[{"message":"sign in again","extensions":` + lookupUnauth2239 + `},{"message":"private App exists","extensions":` + lookupForbidden2239 + `}]}`
	lookupInternal2239     = `{"errors":[{"message":"lookup failed","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
	lookupMissing2239      = `{"data":{"app":null}}`
	forbiddenThenUnauth    = "private App exists; sign in again"
	unauthThenForbidden    = "sign in again; private App exists"
	otherApp2239           = "hrn:app:acme.com:other"
	supportBotAgentRef2239 = "hrn:agent:acme.com:support-bot"
)

type lookupCaller2239 struct {
	name     string
	args     []string
	authored bool
	// otherAppWorker makes the resolved worker belong to another App, the
	// way the worker-target arm reaches the lookup.
	otherAppWorker bool
}

func lookupCallers2239() []lookupCaller2239 {
	return []lookupCaller2239{
		{name: "as-worker flag", args: []string{"agent", "compile", supportBotAgentRef2239, "--as-worker", "--app", otherApp2239}},
		{name: "as-worker positional", args: []string{"app", "agent", "compile", otherApp2239, supportBotAgentRef2239, "--as-worker"}},
		{name: "signing", args: []string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris", "--signature", "new template", "--app", otherApp2239}},
		{name: "worker target", args: []string{"team", "worker", "compile", "hrn:worker:acme.com:other:iris", "--as-worker"}, otherAppWorker: true},
		{name: "grant read", args: []string{"team", "grant", "list", "--app", otherApp2239}},
		{name: "grant request", args: []string{"team", "grant", "request", "--action", "node.read", "--values", `{"node":["n1"]}`, "--reason", "need it", "--app", otherApp2239}},
		{name: "task mine", args: []string{"team", "task", "mine", "--app", otherApp2239}},
		{name: "write attribution", args: []string{"node", "create", "-m", "acme.com:kb", "--loc", "test:authored", "--name", "Authored", "--app", otherApp2239}, authored: true},
	}
}

func runLookup2239(t *testing.T, c lookupCaller2239, lookup string, jsonMode bool) asWorkerResult1896 {
	t.Helper()
	responses := c1Responses()
	responses["CreateNode"] = `{"data":{"createNode":` + nodeJSON + `}}`
	if c.otherAppWorker {
		responses["GetWorker"] = strings.ReplaceAll(responses["GetWorker"], "capp100000000000000000000", "capp900000000000000000000")
	}
	responses["TeamAppIdentity"] = lookup
	args := append([]string{}, c.args...)
	if jsonMode {
		args = append(args, "--json")
	}
	return runAsWorker1896(t, responses, nil, args...)
}

func TestMixedAndFailedAppLookupsKeepTheirMappedExit2239(t *testing.T) {
	for _, c := range lookupCallers2239() {
		for _, jsonMode := range []bool{false, true} {
			t.Run(c.name+map[bool]string{false: "/text", true: "/json"}[jsonMode], func(t *testing.T) {
				missing := runLookup2239(t, c, lookupMissing2239, jsonMode)
				for _, tc := range []struct {
					kind, lookup string
					wantExit     int
					wantOrder    string
				}{
					{"mixed forbidden first", mixedForbiddenFirst, exitcode.Forbidden, forbiddenThenUnauth},
					{"mixed unauthenticated first", mixedUnauthFirst, exitcode.AuthRequired, unauthThenForbidden},
					{"failed lookup", lookupInternal2239, exitcode.Error, "the server hit an internal error"},
				} {
					r := runLookup2239(t, c, tc.lookup, jsonMode)
					all := r.stdout + r.stderr
					// Never the missing-App refusal, never silently dropped.
					if r.stdout == missing.stdout && r.stderr == missing.stderr {
						t.Fatalf("%s collapsed into the missing-App result: %+v", tc.kind, r)
					}
					if !strings.Contains(all, tc.wantOrder) {
						t.Errorf("%s lost the server's messages in order (%q): %+v", tc.kind, tc.wantOrder, r)
					}
					if c.authored {
						// Optional attribution: the person's own write still goes
						// out, with one local note, and no session on any request.
						if r.exit != exitcode.OK || strings.Count(r.stderr, "worker attribution unavailable") != 1 {
							t.Errorf("%s: want the person write plus one note: %+v", tc.kind, r)
						}
						// The person's write is sent exactly once, with no session
						// (cli#2297 item 1): the note and exit alone would not
						// notice a write that went out twice.
						writes := 0
						for _, call := range r.calls {
							if call.Session != "" {
								t.Errorf("%s: request attributed after a failed App lookup: %+v", tc.kind, call)
							}
							if call.Op == "CreateNode" {
								writes++
							}
						}
						if writes != 1 {
							t.Errorf("%s: want exactly one CreateNode, got %d: %+v", tc.kind, writes, r.calls)
						}
					} else {
						if r.exit != tc.wantExit {
							t.Errorf("%s: exit %d, want %d: %+v", tc.kind, r.exit, tc.wantExit, r)
						}
						assertNoWrite1896(t, r)
					}
					// Repeatable for the same lookup answer: running the identical
					// fixture again prints the same bytes and exits the same. This
					// does not vary the App's state: the CLI sees only the lookup
					// answer, so nothing else can reach this path (cli#2297 item 2,
					// option i).
					again := runLookup2239(t, c, tc.lookup, jsonMode)
					if again.exit != r.exit || again.stdout != r.stdout || again.stderr != r.stderr {
						t.Errorf("%s is not repeatable: %+v vs %+v", tc.kind, r, again)
					}
				}
			})
		}
	}
}

// The as-worker and signing arms refuse with "cannot confirm" and say nothing
// went out ("sent" for a compile, "written" for the signing template).
func TestMixedLookupAsWorkerRefusalSaysItCannotConfirm2239(t *testing.T) {
	for _, c := range lookupCallers2239()[:4] {
		t.Run(c.name, func(t *testing.T) {
			for _, lookup := range []string{mixedForbiddenFirst, mixedUnauthFirst} {
				r := runLookup2239(t, c, lookup, false)
				wentOut := !strings.Contains(r.stderr, "nothing was sent") && !strings.Contains(r.stderr, "nothing was written")
				if !strings.Contains(r.stderr, "cannot confirm") || wentOut {
					t.Errorf("the refusal must say it cannot confirm and that nothing went out: %+v", r)
				}
			}
		})
	}
}
