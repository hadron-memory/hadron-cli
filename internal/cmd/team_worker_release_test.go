package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// `worker release` clears the HOLD (hadron-cli#495, cor:agt:020:09) — the only
// thing that frees one. Three branches, and telling them apart is most of the
// command: the server decides who MAY release, the CLI only says which act you
// are performing.
//
// The worker fixtures below carry an explicit hold, since the shared
// irisWorkerJSON is unheld.

// released is the POST-release shape of a worker fixture: the hold cleared, as
// releaseWorker returns it by construction. Stubbing a still-held worker there
// contradicts the contract and can hide a regression that reads the prior
// holder off the response rather than the pre-read (PR #504 review).
func released(worker string) string {
	out := regexp.MustCompile(`"heldByUserId":"[^"]*"`).ReplaceAllString(worker, `"heldByUserId":null`)
	return regexp.MustCompile(`"heldAt":"[^"]*"`).ReplaceAllString(out, `"heldAt":null`)
}

// heldBy returns irisWorkerJSON with the hold set to a given user.
func heldBy(userID string) string {
	return strings.Replace(irisWorkerJSON, `"memoryId":"mw1"`,
		`"memoryId":"mw1","heldByUserId":"`+userID+`","heldAt":"2026-08-20T09:00:00Z"`, 1)
}

const authContextHolgerJSON = `{"data":{"authContext":{"principalType":"USER","appId":null,"agentId":null,
	"user":{"id":"u-holger","name":"Holger","email":"h@example.com","handle":"holger",
	        "githubUsername":null,"roles":["ADMIN"]},
	"apiKey":null,"impersonation":null}}}`

func releaseStubs(worker string, extra map[string]string) map[string]string {
	m := map[string]string{
		"Workers":     `{"data":{"workers":{"total":1,"items":[` + worker + `]}}}`,
		"AuthContext": authContextHolgerJSON,
		// The re-read immediately before the mutation. By default it answers
		// with the SAME worker, i.e. the hold did not change.
		"GetWorker": `{"data":{"worker":` + worker + `}}`,
		// POST-release by construction: irisWorkerJSON carries no hold, which
		// is what releaseWorker returns. Do not swap in a held fixture here.
		"ReleaseWorker": `{"data":{"releaseWorker":` + irisWorkerJSON + `}}`,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// Releasing YOUR OWN name notifies nobody and loses nothing, so it must not
// prompt — a prompt on the ordinary end-of-work step is what teaches people to
// reach for --yes reflexively, which would then also skip the force prompt that
// actually matters.
func TestWorkerReleaseSelfDoesNotPrompt(t *testing.T) {
	gql, captured := captureGraphQL(t, releaseStubs(heldBy("u-holger"), nil))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// No --yes, and no TTY: a prompt here would refuse as non-interactive.
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a self-release must not prompt: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["ReleaseWorker"], &vars)
	if vars["workerRef"] != "wkr1" {
		t.Errorf("release vars: %v", vars)
	}
	got := out.String()
	if !strings.Contains(got, "✓ released Iris") {
		t.Errorf("receipt: %s", got)
	}
	if strings.Contains(got, "force-released") || strings.Contains(got, "team chat") {
		t.Errorf("a self-release announces nothing — do not imply it does: %s", got)
	}
	// The transfer is the least guessable consequence and applies here too.
	if !strings.Contains(got, "go with the name, not with you") {
		t.Errorf("the receipt must say the memory/handoff follows the name: %s", got)
	}
}

// Releasing SOMEONE ELSE'S name is an admin force-release: it posts to the team
// chat naming both parties. Doing that without asking would make a caller
// perform a public act privately, which is the failure cor:agt:020:09 exists to
// prevent — so it refuses non-interactively rather than proceeding.
func TestWorkerReleaseForcedRefusesWithoutYes(t *testing.T) {
	gql, captured := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
		"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
			"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
			"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
	}))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("force-releasing someone else's name must not proceed unprompted")
	}
	if _, called := captured["ReleaseWorker"]; called {
		t.Error("the mutation must not run when the confirmation was refused")
	}
	// Confirm refuses outright without a TTY, so resolving the holder's NAME
	// here is a round trip — and an audit event — spent on a string nobody can
	// read (PR #504 review, suppressed).
	if _, called := captured["GetUser"]; called {
		t.Error("no prompt can be shown, so do not pay to decorate one")
	}
}

// …and with --yes it proceeds, names the prior holder as a PERSON, and says the
// chat post happened rather than leaving the caller to discover it there.
func TestWorkerReleaseForcedNamesTheHolderAndTheChatPost(t *testing.T) {
	gql, _ := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
		"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
			"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
			"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
	}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"force-released Iris", "Dara (@dara)", "posts a notice to the team chat"} {
		if !strings.Contains(got, want) {
			t.Errorf("the force receipt must carry %q: %s", want, got)
		}
	}
	// The notification is BEST-EFFORT server-side and the payload carries no
	// delivery signal, so the receipt must not assert the notice appeared.
	if strings.Contains(got, "announced in the team chat") {
		t.Errorf("claims a delivery it cannot verify: %s", got)
	}
	if !strings.Contains(got, "best-effort") {
		t.Errorf("must say the announcement is best-effort: %s", got)
	}
}

// A holder whose user record the caller may not read must not fail the release
// — the name is decoration, and gating an operation on a display label is the
// describeApp lesson. It falls back to the id.
func TestWorkerReleaseSurvivesUnreadableHolder(t *testing.T) {
	gql, _ := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
		"GetUser": `{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
	}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an unreadable holder must not fail the release: %v", err)
	}
	if !strings.Contains(out.String(), "u-dara") {
		t.Errorf("it must fall back to the holder's id: %s", out.String())
	}
}

// Idempotent: releasing a name nobody holds succeeds server-side. Printing
// "✓ released" there would be a receipt for something that did not happen.
//
// It does NOT say "was not held" either. heldByUserId masks to null on deny, so
// nil read on its own means "unheld OR held and invisible to you", and an
// earlier version that tried to tell them apart — probing the fields masked
// alongside it — was unsound: all of those are legitimately nullable too
// (PR #504 review). So the receipt reports what it actually knows.
//
// #487 added a field that IS sound for this — hasLiveSession, which unlike
// those probes is never legitimately null on a permitted read — so the receipt
// COULD now classify. It deliberately does not yet (#522), which is why this
// test still pins the hedge: the behaviour under test is a choice, and a test
// that stopped asserting it would let the choice be reversed silently.
func TestWorkerReleaseNilHoldClaimsNothing(t *testing.T) {
	for _, tc := range []struct{ name, worker string }{
		{"genuinely unheld", irisWorkerJSON},
		{"held but masked from the caller", maskedWorkerJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubs := releaseStubs(tc.worker, nil)
			stubs["GetWorker"] = `{"data":{"worker":` + tc.worker + `}}`
			gql, _ := captureGraphQL(t, stubs)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			// No --yes and no TTY: a prompt here would refuse. Both cases must
			// stay quiet — prompting on every no-op is the cost #495 asked us
			// not to pay, and the two are indistinguishable anyway.
			root.SetArgs([]string{"team", "worker", "release", "Iris",
				"--app", "acme.com:eng-team", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("a nil hold must not prompt: %v", err)
			}
			got := out.String()
			if !strings.Contains(got, "no hold on Iris was visible to you") {
				t.Errorf("report what is known: %s", got)
			}
			// The two claims it must never make: that something was released,
			// and that the name is free. A reader acting on the second meets
			// WORKER_HELD at the next `session start`.
			if strings.Contains(got, "✓ released") {
				t.Errorf("a no-op must not print a success receipt: %s", got)
			}
			if strings.Contains(got, "was not held") {
				t.Errorf("nil is not proof the name is free: %s", got)
			}
		})
	}
}

// The --json contract. wasHeld/releasedFromUserId describe the state BEFORE the
// call, because the returned worker is post-release by construction — a
// consumer could not compute "did anything happen" from the payload otherwise.
func TestWorkerReleaseJSONShape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		worker  string
		wasHeld bool
		forced  bool
		status  string
	}{
		{"self", heldBy("u-holger"), true, false, "released"},
		{"forced", heldBy("u-dara"), true, true, "released"},
		{"unheld", irisWorkerJSON, false, false, "no-visible-hold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, releaseStubs(tc.worker, map[string]string{
				"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
					"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
					"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
			}))
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes", "--json",
				"--app", "acme.com:eng-team", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var dto struct {
				ID                 string  `json:"id"`
				Name               string  `json:"name"`
				WasHeld            *bool   `json:"wasHeld"`
				ReleasedFromUserID *string `json:"releasedFromUserId"`
				Forced             *bool   `json:"forced"`
				Status             string  `json:"status"`
			}
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatalf("--json must parse: %v (%s)", err, out.String())
			}
			if dto.Status != tc.status {
				t.Errorf("status: %s", out.String())
			}
			if tc.wasHeld {
				if dto.WasHeld == nil || !*dto.WasHeld {
					t.Errorf("a held name must report wasHeld true: %s", out.String())
				}
				if dto.ReleasedFromUserID == nil {
					t.Errorf("the prior holder is the one fact only the pre-read has: %s", out.String())
				}
				if dto.Forced == nil || *dto.Forced != tc.forced {
					t.Errorf("forced: %s", out.String())
				}
			} else {
				// A nil hold is ambiguous, so BOTH booleans stay null rather
				// than encoding a guess as false.
				if dto.WasHeld != nil || dto.Forced != nil {
					t.Errorf("a nil hold must not be reported as a known false: %s", out.String())
				}
				if dto.ReleasedFromUserID != nil {
					t.Errorf("no visible holder means none to report: %s", out.String())
				}
			}
		})
	}
}

// An App-key caller has no user, so it can never be the holder (cor:agt:020:09
// — an App key holds nothing). A null authContext.user must land in the FORCE
// branch rather than erroring or being mistaken for the holder.
func TestWorkerReleaseAppKeyCallerIsNeverTheHolder(t *testing.T) {
	gql, captured := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
		"AuthContext": `{"data":{"authContext":{"principalType":"APP","appId":"app1","agentId":null,
			"user":null,"apiKey":null,"impersonation":null}}}`,
		// Stubbed because reaching the prompt REQUIRES resolving the holder —
		// its absence is itself the signal that the force branch was taken.
		"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
			"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
			"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
	}))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("an App-key caller is not the holder — this must take the force branch and prompt")
	}
	if _, called := captured["ReleaseWorker"]; called {
		t.Error("the mutation must not run when the force confirmation was refused")
	}
}

// What is left of this test is the RETIREMENT half, and only that.
//
// It used to guard the hold as well, by re-reading it before the call: the
// hold could change between the pre-read that CLASSIFIED the act and the call
// that performed it, so an admin could approve a prompt naming Dara and release
// whoever held it now — or, worse, a pre-read showing "unheld" skipped the
// prompt entirely and a hold taken in between was force-released silently.
//
// That re-read is GONE (#522). `releaseWorker` gained a precondition
// (hadron-server#1084) which the release now states, enforced by the guarded
// write, so the window is closed rather than narrowed. The hold's coverage
// moved to TestWorkerReleaseAssertsTheHoldItClassifiedAgainst and
// TestWorkerReleaseHandlesAStaleHold.
//
// Retirement stays here because the precondition does NOT cover it: it asserts
// who holds the name, not whether the worker is still working, and the
// confirmation's transfer clause branches on retirement. Note it is pinned
// under --yes, where no prompt is shown — waiving the QUESTION is not waiving
// the ACT's meaning, which is why #522 did not narrow this guard to the
// prompted path.
func TestWorkerReleaseRefusesWhenTheHoldChanged(t *testing.T) {
	// RETIREMENT is part of "the act just described" too: the confirmation's
	// transfer clause branches on it, so a retirement landing between the
	// prompt and the call leaves the caller having approved wording that no
	// longer fits (PR #504 review).
	t.Run("retired between the prompt and the call", func(t *testing.T) {
		nowRetired := strings.Replace(heldBy("u-dara"), `"retiredAt":null`,
			`"retiredAt":"2026-08-21T00:00:00Z"`, 1)
		gql, captured := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
			"GetWorker": `{"data":{"worker":` + nowRetired + `}}`,
			"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
				"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
				"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
		}))
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
			"--app", "acme.com:eng-team", "--server", gql.URL})
		err := root.Execute()
		if code := exitcode.FromError(err); code != exitcode.Conflict {
			t.Errorf("a retirement mid-flight changes what release MEANS: exit %d; err: %v", code, err)
		}
		if _, called := captured["ReleaseWorker"]; called {
			t.Error("must not perform an act whose description has gone stale")
		}
	})

	// THE HOLD's half of this test moved (#522). It used to live here as two
	// subtests asserting a client-side refusal when a re-read disagreed with
	// the pre-read. That mechanism is GONE: the assertion now travels with the
	// mutation and the server refuses, which closes the window rather than
	// narrowing it. The contract those subtests encoded — an act must not be
	// performed against a hold the caller never saw — is unchanged and is
	// pinned below by TestWorkerReleaseAssertsTheHoldItClassifiedAgainst and
	// TestWorkerReleaseHandlesAStaleHold. Deleting them outright would have
	// dropped the coverage; they were rewritten against the new mechanism.
}

// releaseVars is what the CLI asserts about the hold on the wire.
//
// `raw` is carried alongside the typed fields because THE TYPED FIELDS CANNOT
// SEE THE BUG THIS COMMAND IS MOST EXPOSED TO. `expectUnheld: null` and an
// omitted `expectUnheld` both decode to a nil *bool — while on the wire they
// are different requests: the server reads an omitted field as "no assertion"
// and a present null as an assertion nobody made. That is
// findings:optional-arg-meets-presence-semantics, it is a real incident in this
// codebase, and it is exactly what the omitempty annotations exist to prevent.
// A test that could not distinguish them would let a dropped omitempty pass.
type releaseVars struct {
	WorkerRef            string  `json:"workerRef"`
	ExpectedHolderUserID *string `json:"expectedHolderUserId"`
	ExpectUnheld         *bool   `json:"expectUnheld"`
	raw                  map[string]json.RawMessage
}

// sent reports whether a variable was PRESENT on the wire at all.
func (v releaseVars) sent(key string) bool {
	_, present := v.raw[key]
	return present
}

// releaseSequence serves the ordinary stubs but answers ReleaseWorker from a
// QUEUE, so a test can stage the refuse-then-retry path that #522 introduces.
// captureGraphQL keeps only the last call of an operation and cannot vary its
// response, and both matter here: the retry is a SECOND call whose variables
// differ from the first, and the whole point is that the first one is refused.
//
// Follows the recording-handler pattern the replace tests already use for a
// two-call sequence.
func releaseSequence(t *testing.T, worker string, releases []string, extra map[string]string) (*httptest.Server, *[]releaseVars) {
	t.Helper()
	stubs := releaseStubs(worker, extra)
	seen := &[]releaseVars{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName == "ReleaseWorker" {
			var v releaseVars
			_ = json.Unmarshal(body.Variables, &v)
			_ = json.Unmarshal(body.Variables, &v.raw)
			*seen = append(*seen, v)
			if len(*seen) > len(releases) {
				t.Errorf("ReleaseWorker called %d times, only %d staged — a retry loop is the one thing "+
					"this path must not do", len(*seen), len(releases))
				_, _ = w.Write([]byte(`{"errors":[{"message":"too many calls"}]}`))
				return
			}
			_, _ = w.Write([]byte(releases[len(*seen)-1]))
			return
		}
		resp, ok := stubs[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

func holdStale(heldByUserID string) string {
	holder := "null"
	if heldByUserID != "" {
		holder = `"` + heldByUserID + `"`
	}
	return `{"errors":[{"message":"The hold is not what you asserted.","extensions":{` +
		`"code":"WORKER_HOLD_STALE","workerId":"wkr1","heldByUserId":` + holder + `}}]}`
}

const releasedOK = `{"data":{"releaseWorker":` + irisWorkerJSON + `}}`

// EXACTLY ONE assertion travels with every release, and which one is the whole
// safety property (#522, hadron-server#1084).
//
// The unheld case is the one worth having. Before this, a nil hold meant the
// CLI asserted NOTHING and released unconditionally — so a hold taken in the
// interval, or one merely masked from this caller, was force-released in
// silence and announced in the team chat for an act nobody was asked about.
func TestWorkerReleaseAssertsTheHoldItClassifiedAgainst(t *testing.T) {
	for _, tc := range []struct {
		name       string
		worker     string
		wantHolder *string
		wantUnheld *bool
	}{
		{"held: assert that holder", heldBy("u-dara"), strPtr("u-dara"), nil},
		{"my own hold: assert me", heldBy("u-holger"), strPtr("u-holger"), nil},
		{"no visible hold: assert unheld", irisWorkerJSON, nil, boolPtrT(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, seen := releaseSequence(t, tc.worker, []string{releasedOK}, map[string]string{
				"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
					"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
					"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
				"--app", "acme.com:eng-team", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if len(*seen) != 1 {
				t.Fatalf("want exactly one release call, got %d", len(*seen))
			}
			got := (*seen)[0]
			if !eqStrPtr(got.ExpectedHolderUserID, tc.wantHolder) {
				t.Errorf("expectedHolderUserId = %v, want %v", deref(got.ExpectedHolderUserID), deref(tc.wantHolder))
			}
			if (got.ExpectUnheld == nil) != (tc.wantUnheld == nil) {
				t.Errorf("expectUnheld = %v, want %v", got.ExpectUnheld, tc.wantUnheld)
			}
			// PRESENCE, not just value. The unused argument must be ABSENT
			// from the request, never present-as-null: the server reads an
			// omitted field as "no assertion" and a present null as an
			// assertion nobody made, so a dropped omitempty turns every
			// release into a malformed one — invisibly to a typed decode,
			// which renders both as nil.
			wantHolderSent, wantUnheldSent := tc.wantHolder != nil, tc.wantUnheld != nil
			if got.sent("expectedHolderUserId") != wantHolderSent {
				t.Errorf("expectedHolderUserId present=%v, want %v (raw: %s)",
					got.sent("expectedHolderUserId"), wantHolderSent, got.raw["expectedHolderUserId"])
			}
			if got.sent("expectUnheld") != wantUnheldSent {
				t.Errorf("expectUnheld present=%v, want %v (raw: %s)",
					got.sent("expectUnheld"), wantUnheldSent, got.raw["expectUnheld"])
			}
			// NEVER BOTH — the server refuses BAD_USER_INPUT for that, and the
			// two arguments exist as a pair precisely so "expect nobody" and
			// "no expectation" cannot collapse into each other.
			if got.sent("expectedHolderUserId") && got.sent("expectUnheld") {
				t.Error("both assertions sent; the server refuses that as BAD_USER_INPUT")
			}
			// NEVER NEITHER — that is the old unconditional release, which is
			// the behaviour #522 exists to remove.
			if !got.sent("expectedHolderUserId") && !got.sent("expectUnheld") {
				t.Error("no assertion sent — this is the unconditional release #522 removes")
			}
		})
	}
}

// A refused assertion is turned into a TRUTHFUL second offer, exactly once.
//
// Refusing outright would be the safe-looking answer and it would break a
// legitimate operation: a caller who cannot READ the hold classifies it as nil
// and asserts "unheld", so re-running re-derives the same wrong assertion and
// they are refused forever. The retry is what keeps an admin force-release
// possible while making it INFORMED — which is the thing that was silent before.
func TestWorkerReleaseHandlesAStaleHold(t *testing.T) {
	gilUser := map[string]string{
		"GetUser": `{"data":{"user":{"id":"u-gil","name":"Gil","email":null,"handle":"gil",
			"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
			"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
	}

	// Non-interactive and without --yes: a force-release nobody consented to is
	// refused, and NOTHING is changed. The second call must not happen.
	t.Run("someone else holds it, no --yes and no TTY: refuse", func(t *testing.T) {
		srv, seen := releaseSequence(t, irisWorkerJSON, []string{holdStale("u-gil")}, gilUser)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "release", "Iris",
			"--app", "acme.com:eng-team", "--server", srv.URL})
		err := root.Execute()
		if code := exitcode.FromError(err); code != exitcode.Conflict {
			t.Errorf("exit %d, want %d; err: %v", code, exitcode.Conflict, err)
		}
		if len(*seen) != 1 {
			t.Errorf("the release must not be retried without consent, got %d calls", len(*seen))
		}
		// The refusal names WHO, which the caller could not otherwise learn —
		// and says nothing was changed, because nothing was.
		if !strings.Contains(err.Error(), "Gil") || !strings.Contains(err.Error(), "nothing was changed") {
			t.Errorf("the refusal must name the holder and say nothing changed: %v", err)
		}
	})

	// With --yes the caller has pre-consented to a force-release, so the retry
	// proceeds — against the holder the SERVER named, not the CLI's guess.
	t.Run("someone else holds it, --yes: retry against the true holder", func(t *testing.T) {
		srv, seen := releaseSequence(t, irisWorkerJSON,
			[]string{holdStale("u-gil"), releasedOK}, gilUser)
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes", "--json",
			"--app", "acme.com:eng-team", "--server", srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if len(*seen) != 2 {
			t.Fatalf("want a refusal then one retry, got %d calls", len(*seen))
		}
		if got := deref((*seen)[0].ExpectUnheld); got != "true" {
			t.Errorf("the first attempt must assert unheld, got %v", got)
		}
		if got := deref((*seen)[1].ExpectedHolderUserID); got != "u-gil" {
			t.Errorf("the retry must assert the holder the SERVER named, got %q", got)
		}
		// The receipt reports what the server SAW. Before #522 this path
		// produced wasHeld/forced nil and no prior holder at all, because the
		// CLI had classified against a hold it could not see.
		var dto struct {
			WasHeld            *bool   `json:"wasHeld"`
			ReleasedFromUserID *string `json:"releasedFromUserId"`
			Forced             *bool   `json:"forced"`
			Status             string  `json:"status"`
		}
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, out.String())
		}
		if dto.WasHeld == nil || !*dto.WasHeld {
			t.Errorf("wasHeld must be true — the server proved it was held: %v", dto.WasHeld)
		}
		if deref(dto.ReleasedFromUserID) != "u-gil" {
			t.Errorf("releasedFromUserId = %v, want u-gil", deref(dto.ReleasedFromUserID))
		}
		if dto.Forced == nil || !*dto.Forced {
			t.Errorf("forced must be true — a known holder who is not me: %v", dto.Forced)
		}
		if dto.Status != "released" {
			t.Errorf("status = %q, want released", dto.Status)
		}
	})

	// The hold turns out to be MINE. A self-release owes nobody notice and no
	// confirmation, so this proceeds without asking even without --yes — as it
	// would have if the hold had been readable in the first place.
	t.Run("it is my own hold after all: no prompt, not forced", func(t *testing.T) {
		srv, seen := releaseSequence(t, irisWorkerJSON,
			[]string{holdStale("u-holger"), releasedOK}, nil)
		f, out := testFactory(t)
		root := NewRootCmd(f)
		// No --yes and no TTY: a prompt here would refuse as non-interactive,
		// so reaching a successful release IS the assertion that none was shown.
		root.SetArgs([]string{"team", "worker", "release", "Iris", "--json",
			"--app", "acme.com:eng-team", "--server", srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("a self-release must not prompt, even when discovered late: %v", err)
		}
		if len(*seen) != 2 {
			t.Fatalf("want a refusal then one retry, got %d calls", len(*seen))
		}
		var dto struct {
			Forced             *bool   `json:"forced"`
			ReleasedFromUserID *string `json:"releasedFromUserId"`
		}
		_ = json.Unmarshal([]byte(out.String()), &dto)
		if dto.Forced == nil || *dto.Forced {
			t.Errorf("releasing my own name is not a force-release: %v", dto.Forced)
		}
		if deref(dto.ReleasedFromUserID) != "u-holger" {
			t.Errorf("releasedFromUserId = %v, want u-holger", deref(dto.ReleasedFromUserID))
		}
	})

	// The hold was RELEASED underneath us. There is nothing left to release, so
	// the act the caller approved is not available to perform — and reporting
	// "released" would claim this command did it.
	t.Run("unheld by the time we call: refuse rather than claim a release", func(t *testing.T) {
		srv, seen := releaseSequence(t, heldBy("u-dara"), []string{holdStale("")}, map[string]string{
			"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
				"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
				"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
			"--app", "acme.com:eng-team", "--server", srv.URL})
		err := root.Execute()
		if code := exitcode.FromError(err); code != exitcode.Conflict {
			t.Errorf("exit %d, want %d; err: %v", code, exitcode.Conflict, err)
		}
		if len(*seen) != 1 {
			t.Errorf("nothing to retry when the name is already free, got %d calls", len(*seen))
		}
		if !strings.Contains(err.Error(), "nothing left to release") {
			t.Errorf("the refusal must say the name is already free: %v", err)
		}
	})

	// A FAILED identity lookup is "unknown", not "you are not the holder", and
	// the retry path had not learned that yet (PR #524 review, @codex P2).
	//
	// With meKnown false the stale holder may BE the caller, so a message
	// asserting an admin force-release and a team-chat notice describes a
	// public act that may not happen — #504's lesson arriving in the one place
	// it had not been applied. The receipt was already honest here (`forced`
	// stays null); the WORDING was not.
	t.Run("unknown identity: the refusal must hedge, not assert a public act", func(t *testing.T) {
		srv, seen := releaseSequence(t, irisWorkerJSON, []string{holdStale("u-gil")}, map[string]string{
			"AuthContext": `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
			"GetUser": `{"data":{"user":{"id":"u-gil","name":"Gil","email":null,"handle":"gil",
				"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
				"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "release", "Iris",
			"--app", "acme.com:eng-team", "--server", srv.URL})
		err := root.Execute()
		if code := exitcode.FromError(err); code != exitcode.Conflict {
			t.Fatalf("exit %d, want %d; err: %v", code, exitcode.Conflict, err)
		}
		if len(*seen) != 1 {
			t.Errorf("no consent, no retry: got %d calls", len(*seen))
		}
		msg := err.Error()
		// It must state the UNCERTAINTY rather than the classification.
		if !strings.Contains(msg, "could not read your own identity") {
			t.Errorf("an unreadable identity must be said out loud: %v", msg)
		}
		// And it must NOT assert the act it cannot classify. "would be an admin
		// force-release" is the exact phrase the known branch uses, so its
		// absence here is what separates the two.
		if strings.Contains(msg, "would be an admin force-release") {
			t.Errorf("this asserts a classification it does not have: %v", msg)
		}
	})

	// The hold moves AGAIN inside the retry window. One retry, never a loop —
	// a client racing a human is not a fix.
	t.Run("the hold moves again: stop, do not loop", func(t *testing.T) {
		srv, seen := releaseSequence(t, irisWorkerJSON,
			[]string{holdStale("u-gil"), holdStale("u-dara")}, gilUser)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
			"--app", "acme.com:eng-team", "--server", srv.URL})
		err := root.Execute()
		if code := exitcode.FromError(err); code != exitcode.Conflict {
			t.Errorf("exit %d, want %d; err: %v", code, exitcode.Conflict, err)
		}
		if len(*seen) != 2 {
			t.Errorf("want exactly two attempts and no third, got %d", len(*seen))
		}
		if !strings.Contains(err.Error(), "changed again") {
			t.Errorf("the second refusal must say so: %v", err)
		}
	})
}

func strPtr(s string) *string { return &s }
func boolPtrT(b bool) *bool   { return &b }

func eqStrPtr(a, b *string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

// deref renders an optional for an error message — "<nil>" rather than a panic,
// and the value's own spelling otherwise.
func deref(v any) string {
	switch p := v.(type) {
	case *string:
		if p == nil {
			return "<nil>"
		}
		return *p
	case *bool:
		if p == nil {
			return "<nil>"
		}
		if *p {
			return "true"
		}
		return "false"
	}
	return "<nil>"
}

// A FAILED identity lookup is "unknown", not "you are not the holder"
// (PR #504 review). Collapsing them reclassifies a legitimate self-release as
// a force-release: refused non-interactively, and — worse — reported with
// `forced: true` and a team-chat announcement the server never made.
func TestWorkerReleaseUnknownIdentityIsNotAForceRelease(t *testing.T) {
	stubs := releaseStubs(heldBy("u-holger"), map[string]string{
		"AuthContext": `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
		"GetUser": `{"data":{"user":{"id":"u-holger","name":"Holger","email":null,"handle":"holger",
			"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
			"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
	})

	// It still asks, because it cannot rule out a public act — but the prompt
	// says WHY rather than asserting the name is someone else's.
	gql, captured := captureGraphQL(t, stubs)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("an unclassifiable release must ask before acting")
	}
	if _, called := captured["ReleaseWorker"]; called {
		t.Error("refused prompt must not reach the mutation")
	}

	// With --yes it proceeds, and reports `forced: null` — not false (which
	// would claim a private act) and not true (which would claim an
	// announcement). The human line says the same thing in words.
	gql2, _ := captureGraphQL(t, stubs)
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "worker", "release", "Iris", "--yes", "--json",
		"--app", "acme.com:eng-team", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out2.String()), &raw); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out2.String())
	}
	if v, present := raw["forced"]; !present || v != nil {
		t.Errorf("forced must be null when the act could not be classified, got %v: %s", v, out2.String())
	}

	f3, out3 := testFactory(t)
	root3 := NewRootCmd(f3)
	gql3, _ := captureGraphQL(t, stubs)
	root3.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
		"--app", "acme.com:eng-team", "--server", gql3.URL})
	if err := root3.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out3.String(), "identity could not be read") {
		t.Errorf("the receipt must say it could not classify the act: %s", out3.String())
	}
}

// `worker get` is the detail surface, and "whose name is this" is a question
// people ask of it before asking a colleague for a name. Rendered only when
// KNOWN: heldByUserId masks to null on deny, so printing "held by: —" would
// answer "nobody" to a caller who merely cannot see (PR #504 review — the plan
// doc claimed this render before it existed).
func TestWorkerGetRendersTheHolder(t *testing.T) {
	held := map[string]string{
		"Workers":         `{"data":{"workers":{"total":1,"items":[` + heldBy("u-dara") + `]}}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
		"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
			"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
			"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
	}
	gql, _ := captureGraphQL(t, held)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "Iris", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"held by: Dara (@dara)", "since 2026-08-20T09:00:00Z"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("get must render the hold (%q): %s", want, out.String())
		}
	}

	// An UNHELD (or unreadable) worker renders no hold line at all, rather than
	// an empty one that would read as a settled "nobody".
	gql2, captured2 := captureGraphQL(t, map[string]string{
		"Workers":         `{"data":{"workers":{"total":1,"items":[` + irisWorkerJSON + `]}}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "worker", "get", "Iris", "--app", "acme.com:eng-team", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out2.String(), "held by") {
		t.Errorf("an absent hold must render nothing, not an empty claim: %s", out2.String())
	}
	// And it costs no user lookup — absence is known without asking.
	if _, called := captured2["GetUser"]; called {
		t.Error("no holder means no holder read")
	}

	// --json is unaffected by the render and carries the actionable id.
	gql3, _ := captureGraphQL(t, held)
	f3, out3 := testFactory(t)
	root3 := NewRootCmd(f3)
	root3.SetArgs([]string{"team", "worker", "get", "Iris", "--json", "--app", "acme.com:eng-team", "--server", gql3.URL})
	if err := root3.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out3.String()), &dto); err != nil {
		t.Fatalf("--json must parse: %v", err)
	}
	if dto["heldByUserId"] != "u-dara" || dto["heldAt"] == nil {
		t.Errorf("--json must carry the hold: %s", out3.String())
	}
}

// maskedWorker is what a caller who may NOT read working state sees: the hold
// is null, and so are the fields masked alongside it (prompt, promptOverride,
// memoryId). Indistinguishable from "unheld" on heldByUserId alone.
const maskedWorkerJSON = `{"id":"wkr1","urn":"hrn:worker:acme.com:eng-team:iris","slug":"iris",
	"appId":"app1","agentId":"agt1","name":"Iris","role":"backend-engineer",
	"prompt":null,"promptOverride":null,"memoryId":null,"heldByUserId":null,"heldAt":null,
	"retiredAt":null,"retiredBy":null,"createdAt":"2026-08-14T00:00:00Z","createdBy":"u-holger"}`

// "anyone may bind it now" is the natural thing to say after a release, and it
// is FALSE for a retired worker: startSession refuses one (WORKER_RETIRED)
// regardless of the hold, so releasing frees nothing a caller can use.
//
// Found by sweeping every sentence the command prints and asking what proves
// each — the pass I should have run after the FIRST unverifiable claim in this
// PR's review rather than the third.
func TestWorkerReleaseDoesNotPromiseBindingARetiredWorker(t *testing.T) {
	retiredHeld := strings.Replace(heldBy("u-holger"), `"retiredAt":null`,
		`"retiredAt":"2026-08-15T00:00:00Z"`, 1)
	stubs := releaseStubs(retiredHeld, nil)
	stubs["GetWorker"] = `{"data":{"worker":` + retiredHeld + `}}`
	// POST-release: releaseWorker returns the worker with the hold cleared, by
	// construction. A stub that hands back a still-held worker is inconsistent
	// with the contract and can mask a regression that reads the hold from the
	// response instead of the pre-read (PR #504 review).
	stubs["ReleaseWorker"] = `{"data":{"releaseWorker":` + released(retiredHeld) + `}}`

	gql, _ := captureGraphQL(t, stubs)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "anyone may bind it now") {
		t.Errorf("a retired worker cannot be bound by anyone — the release frees nothing usable: %s", got)
	}
	if !strings.Contains(got, "retired") {
		t.Errorf("say why the released name is still unbindable: %s", got)
	}

	// Control: a LIVE worker still gets the plain, useful sentence.
	gql2, _ := captureGraphQL(t, releaseStubs(heldBy("u-holger"), nil))
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out2.String(), "anyone may bind it now") {
		t.Errorf("a live worker's release should say the name is available: %s", out2.String())
	}
}

// The force PROMPT makes the same transfer promise the receipt does, and it is
// equally false for a retired worker — nobody takes a retired name. Caught in
// review AFTER the receipt was fixed: I swept the receipts and not the prompt,
// which is precisely the narrow fix the preceding commit was about not making.
//
// Unit-tested through the command's refusal path rather than by reading the
// string, because cmdutil.Confirm's prompt branch is unreachable without a TTY
// — the assertion is on what a non-interactive run REFUSES, plus the retired
// receipt, so the two stay consistent.
func TestWorkerReleasePromptDoesNotPromiseANextHolderForARetiredWorker(t *testing.T) {
	retiredHeld := strings.Replace(heldBy("u-dara"), `"retiredAt":null`,
		`"retiredAt":"2026-08-15T00:00:00Z"`, 1)
	stubs := releaseStubs(retiredHeld, map[string]string{
		"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
			"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
			"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
	})
	stubs["GetWorker"] = `{"data":{"worker":` + retiredHeld + `}}`
	stubs["ReleaseWorker"] = `{"data":{"releaseWorker":` + released(retiredHeld) + `}}`

	gql, _ := captureGraphQL(t, stubs)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// A forced release of a RETIRED worker: the receipt must not send anyone
	// off to bind it either.
	if strings.Contains(out.String(), "anyone may bind") {
		t.Errorf("nobody can bind a retired worker: %s", out.String())
	}
}

// The command HELP is where a reader forms their model of the verb, and
// "releasing frees the name for the next person" is the model they carry away.
// It is incomplete rather than wrong for a retired worker — the release
// succeeds; it is the later bind that refuses — so the help completes the
// model rather than hedging the verb (PR #504 review).
func TestWorkerReleaseHelpCoversTheRetiredCase(t *testing.T) {
	buf := &strings.Builder{}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(buf)
	root.SetArgs([]string{"team", "worker", "release", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	help := buf.String()
	for _, want := range []string{"RETIRED worker can be released", "WORKER_RETIRED", "no next holder"} {
		if !strings.Contains(help, want) {
			t.Errorf("release help must complete the model for a retired worker (%q):\n%s", want, help)
		}
	}
	// And it must NOT imply the release itself fails — it does not.
	for _, wrong := range []string{"cannot be released", "release will fail", "refuses to release"} {
		if strings.Contains(help, wrong) {
			t.Errorf("releasing a retired worker succeeds; help must not say otherwise (%q)", wrong)
		}
	}
}

// `session start` CLAIMS the hold for a person, but builds its --json worker
// from the PRE-mutation read — so including the hold there reports
// `heldByUserId: null` immediately after the bind that set it (PR #504 review).
//
// Omitted rather than nulled: a null asserts "unheld", which is the claim this
// command spent a review learning not to make.
func TestSessionStartDoesNotReportAStaleHold(t *testing.T) {
	teamGitDir(t)
	// The fixture must CARRY a hold, or `omitempty` hides the difference and
	// this test cannot fail — which is what the first version of it did.
	gql, _ := captureGraphQL(t, map[string]string{
		"Workers":          `{"data":{"workers":{"total":1,"items":[` + heldBy("u-dara") + `]}}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--json",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var res struct {
		Worker map[string]any `json:"worker"`
	}
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out.String())
	}
	if res.Worker["name"] != "Iris" {
		t.Fatalf("the worker must still be reported: %s", out.String())
	}
	for _, gone := range []string{"heldByUserId", "heldAt"} {
		if _, present := res.Worker[gone]; present {
			t.Errorf("%q here is the hold BEFORE the bind that claimed it — omit rather than mislead: %s",
				gone, out.String())
		}
	}
}

// Ending a session never clears a hold (cor:agt:020:09), so the session-start
// ROLLBACK — server session created, local binding write failed — leaves a hold
// the caller does not know they took. Reporting "worker X is not held" there
// strands it on the one path where they believe nothing happened
// (PR #504 review).
func TestSessionStartRollbackSaysTheHoldRemains(t *testing.T) {
	dir := teamGitDir(t)
	// The READ must succeed (no binding) and the WRITE must fail, so make the
	// directory unwritable rather than putting something in the file's place —
	// a bad file fails the read first and never reaches the rollback.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	gql, captured := captureGraphQL(t, map[string]string{
		"Workers":          `{"data":{"workers":{"total":1,"items":[` + irisWorkerJSON + `]}}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"EndTeamSession":   `{"data":{"endSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a failed binding write must fail the command")
	}
	if _, called := captured["EndTeamSession"]; !called {
		t.Error("the server session must still be rolled back")
	}
	if strings.Contains(err.Error(), "is not held") {
		t.Errorf("ending a session does not clear a hold — do not claim it did: %v", err)
	}
	// It must DISCLOSE that a hold may remain — the failure mode of the
	// original wording was a hold stranded in silence.
	if !strings.Contains(err.Error(), "never clears a name HOLD") {
		t.Errorf("the error must disclose that the hold outlives the session: %v", err)
	}
	// …without PRESCRIBING release. Three callers reach this line and only one
	// of them should release: a person whose bind claimed the name. An App key
	// claimed nothing, and a person who ALREADY held the name acquired nothing
	// new — for them, releasing discards a hold they had all along.
	if strings.Contains(err.Error(), "release it with") || strings.Contains(err.Error(), "so release it") {
		t.Errorf("must not instruct a release it cannot know is right: %v", err)
	}
	if !strings.Contains(err.Error(), "worker get") {
		t.Errorf("point at the read that answers it instead: %v", err)
	}
}

// `worker --help` renders each subcommand's Short ALONE, without the Long that
// qualifies it — so the summary must not promise a next holder either
// (PR #504 review).
func TestWorkerGroupHelpSummaryDoesNotPromiseANextHolder(t *testing.T) {
	buf := &strings.Builder{}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(buf)
	root.SetArgs([]string{"team", "worker", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	help := buf.String()
	if !strings.Contains(help, "release") {
		t.Fatalf("the release verb must be listed: %s", help)
	}
	for _, promise := range []string{"somebody else can take it", "so someone else can take"} {
		if strings.Contains(help, promise) {
			t.Errorf("the group summary must not promise a next holder (%q) — a retired worker has none:\n%s",
				promise, help)
		}
	}
}
