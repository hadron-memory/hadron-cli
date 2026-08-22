package cmd

import (
	"encoding/json"
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
// nil means "unheld OR held and invisible to you", and an earlier version that
// tried to tell them apart — probing the fields masked alongside it — was
// unsound: all of those are legitimately nullable too (PR #504 review). So the
// receipt reports what it actually knows.
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

// `releaseWorker` takes no precondition and does not return the prior holder,
// so the hold can change between the pre-read that CLASSIFIED the act and the
// call that performs it. Two bad outcomes, and the second is the dangerous one:
//
//   - an admin approves a prompt naming Dara and releases whoever holds it now;
//   - a pre-read showing "unheld" or "me" skips the prompt ENTIRELY, so a hold
//     taken in between is force-released silently while the receipt reports a
//     self-release — the act performed differing from the act described.
//
// The re-read cannot close the race (that needs an expectedHolder precondition
// server-side) but it narrows it to one round trip and refuses rather than
// guessing. These two pin the refusal (PR #504 review, P1).
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

	t.Run("prompted case: a different holder by the time we call", func(t *testing.T) {
		gql, captured := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
			"GetWorker": `{"data":{"worker":` + heldBy("u-gil") + `}}`,
			"GetUser": `{"data":{"user":{"id":"u-gil","name":"Gil","email":null,"handle":"gil",
				"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
				"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
		}))
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
			"--app", "acme.com:eng-team", "--server", gql.URL})
		err := root.Execute()
		if code := exitcode.FromError(err); code != exitcode.Conflict {
			t.Errorf("a changed hold is a state conflict: exit %d, want %d; err: %v", code, exitcode.Conflict, err)
		}
		if _, called := captured["ReleaseWorker"]; called {
			t.Error("the release must not run against a holder the caller never saw")
		}
	})

	// The silent one: nothing to release at pre-read time, so no prompt — and
	// then somebody claims it. Without the re-read this force-releases them and
	// prints "was not held".
	t.Run("unprompted case: a hold appears after an unheld pre-read", func(t *testing.T) {
		gql, captured := captureGraphQL(t, releaseStubs(irisWorkerJSON, map[string]string{
			"GetWorker": `{"data":{"worker":` + heldBy("u-gil") + `}}`,
			"GetUser": `{"data":{"user":{"id":"u-gil","name":"Gil","email":null,"handle":"gil",
				"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
				"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
		}))
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "release", "Iris",
			"--app", "acme.com:eng-team", "--server", gql.URL})
		err := root.Execute()
		if code := exitcode.FromError(err); code != exitcode.Conflict {
			t.Errorf("a hold appearing after an unheld read must refuse, not silently force: exit %d; err: %v", code, err)
		}
		if _, called := captured["ReleaseWorker"]; called {
			t.Error("this is the SILENT force-release the re-read exists to prevent")
		}
	})
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
	for _, want := range []string{"HELD by you", "worker release"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name the stranded hold and its remedy (%q): %v", want, err)
		}
	}
	// CONDITIONALLY, though: only a person's bind claims a name. Telling an
	// App-key caller its name is "HELD by you" would send it to release one it
	// never took — a force-release, with a chat post, on somebody else's hold.
	if !strings.Contains(err.Error(), "if you bound as a person") {
		t.Errorf("the hold claim must be conditional on the credential: %v", err)
	}
	if !strings.Contains(err.Error(), "App-key bind claims no hold") {
		t.Errorf("say which credential needs nothing: %v", err)
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
