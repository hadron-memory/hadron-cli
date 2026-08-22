package cmd

import (
	"encoding/json"
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
		"GetWorker":     `{"data":{"worker":` + worker + `}}`,
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
func TestWorkerReleaseUnheldReportsTheNoOp(t *testing.T) {
	gql, _ := captureGraphQL(t, releaseStubs(irisWorkerJSON, nil)) // no hold
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "was not held — nothing to release") {
		t.Errorf("a no-op must say so: %s", got)
	}
	if strings.Contains(got, "✓ released") {
		t.Errorf("a no-op must not print a success receipt: %s", got)
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
		{"unheld", irisWorkerJSON, false, false, "not-held"},
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
				WasHeld            bool    `json:"wasHeld"`
				ReleasedFromUserID *string `json:"releasedFromUserId"`
				Forced             bool    `json:"forced"`
				Status             string  `json:"status"`
			}
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatalf("--json must parse: %v (%s)", err, out.String())
			}
			if dto.WasHeld != tc.wasHeld || dto.Forced != tc.forced || dto.Status != tc.status {
				t.Errorf("shape: %s", out.String())
			}
			if tc.wasHeld && dto.ReleasedFromUserID == nil {
				t.Errorf("the prior holder is the one fact only the pre-read has: %s", out.String())
			}
			if !tc.wasHeld && dto.ReleasedFromUserID != nil {
				t.Errorf("a no-op released nobody: %s", out.String())
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

// A nil hold means "unheld OR masked on deny" — heldByUserId is gated exactly
// like prompt/promptOverride/memoryId. My first version asserted the ambiguity
// collapsed, reasoning that a successful release implies the caller could read
// the field. It does not: the mask exists so a FORMER App member cannot read
// staffing, and a former member can still BE the holder — passing the release
// gate while failing the read gate (PR #504 review, @copilot).
//
// So the co-masked fields are the probe. Visible ⇒ the gate is open ⇒ a nil
// hold is real. All null ⇒ we cannot tell, and must not claim.
func TestWorkerReleaseDoesNotClaimUnheldWhenItCannotSee(t *testing.T) {
	masked := func(extra map[string]string) map[string]string {
		m := releaseStubs(maskedWorkerJSON, extra)
		m["GetWorker"] = `{"data":{"worker":` + maskedWorkerJSON + `}}`
		return m
	}

	// It PROMPTS: an unheld-looking release could still take somebody's name
	// and post to the team chat.
	gql, captured := captureGraphQL(t, masked(nil))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("an invisible hold must be asked about, not assumed absent")
	}
	if _, called := captured["ReleaseWorker"]; called {
		t.Error("refused prompt must not reach the mutation")
	}

	// And with --yes it never claims the name was free.
	gql2, _ := captureGraphQL(t, masked(nil))
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
		"--app", "acme.com:eng-team", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out2.String(), "was not held") {
		t.Errorf("must not assert the name was free — a reader binds on that: %s", out2.String())
	}
	if !strings.Contains(out2.String(), "not visible to you") {
		t.Errorf("must say the hold was not visible: %s", out2.String())
	}

	// --json says it too, rather than encoding the guess.
	gql3, _ := captureGraphQL(t, masked(nil))
	f3, out3 := testFactory(t)
	root3 := NewRootCmd(f3)
	root3.SetArgs([]string{"team", "worker", "release", "Iris", "--yes", "--json",
		"--app", "acme.com:eng-team", "--server", gql3.URL})
	if err := root3.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out3.String()), &raw); err != nil {
		t.Fatalf("--json must parse: %v", err)
	}
	if raw["status"] != "unknown-hold" {
		t.Errorf(`status must be "unknown-hold", got %v: %s`, raw["status"], out3.String())
	}
	if v, present := raw["wasHeld"]; !present || v != nil {
		t.Errorf("wasHeld must be null when the hold is not visible, got %v", v)
	}

	// The control: the SAME nil hold, but with co-masked fields visible, is a
	// genuine no-op — no prompt, and it does say "was not held". Without this
	// the guard could be "always prompt", which would ruin the common case.
	gql4, _ := captureGraphQL(t, releaseStubs(irisWorkerJSON, nil))
	f4, out4 := testFactory(t)
	root4 := NewRootCmd(f4)
	root4.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql4.URL})
	if err := root4.Execute(); err != nil {
		t.Fatalf("a visible-and-unheld release must not prompt: %v", err)
	}
	if !strings.Contains(out4.String(), "was not held") {
		t.Errorf("a genuinely unheld name still says so plainly: %s", out4.String())
	}
}

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
	stubs["ReleaseWorker"] = `{"data":{"releaseWorker":` + retiredHeld + `}}`

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
