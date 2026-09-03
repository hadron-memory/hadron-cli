package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The prompt branch of `worker release` (hadron-cli#525).
//
// Every test here was unwritable before output.TestTTY: cmdutil.Confirm refuses
// as non-interactive BEFORE composing its prompt, so a command-level test could
// only ever observe the refusal. The neighbouring file's coverage stops at that
// wall — TestWorkerReleaseForcedRefusesWithoutYes pins what happens when nobody
// can be asked, and the compositions are unit-tested as pure functions, which
// pins the STRING each builder produces. Neither pins the ARGUMENTS the call
// site passes, and that is where the defect lived: a mutation flipping
// staleReleasePrompt's meKnown to true passed the entire suite (PR #524).
//
// So these drive the command end to end and assert the prompt a PERSON sees.
// Two of them are the mutation detectors that were missing; the argument under
// test is `meKnown`, and it decides whether a confirmation asserts a public act
// or hedges because the caller's own identity could not be read.

const daraUserJSON = `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
	"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
	"externalId":null,"externalAppId":null,"linkedAt":null}}}`

// The identity read FAILING is the state that makes `meKnown` false. It is not
// an exotic one — an offline resolver, a permission change, an expired token
// mid-command — and it is the only state in which the hedge is correct.
const authContextUnreadableJSON = `{"errors":[{"message":"identity unavailable"}]}`

// A force-release POSTS TO THE TEAM CHAT naming both parties. The prompt is the
// only place a caller is told that before it happens, so this asserts what it
// says — and that declining stops everything, which is the property the whole
// confirmation exists to provide.
func TestWorkerReleaseForcePromptIsShownAndDeclineChangesNothing(t *testing.T) {
	gql, captured := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
		"GetUser": daraUserJSON,
	}))
	f, _, errOut := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})

	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Cancelled {
		t.Errorf("declining must exit Cancelled, got %d (err: %v)", code, err)
	}
	if _, called := captured["ReleaseWorker"]; called {
		t.Error("the mutation ran after the confirmation was declined")
	}

	prompt := errOut.String()
	// The holder as a PERSON. "u-dara" is not something a caller can act on,
	// and naming them is the whole reason the GetUser round trip is paid for
	// here and suppressed on the non-interactive path.
	if !strings.Contains(prompt, "Dara") {
		t.Errorf("the prompt must name the holder as a person: %q", prompt)
	}
	// The consequence that is not guessable from the verb "release".
	if !strings.Contains(prompt, "POSTS TO THE TEAM CHAT") {
		t.Errorf("the prompt must say the act is public: %q", prompt)
	}
	// The transfer clause: where the worker's notes go.
	if !strings.Contains(prompt, "whoever takes the name next") {
		t.Errorf("the prompt must say where the working memory goes: %q", prompt)
	}
	if !strings.Contains(prompt, "Aborted.") {
		t.Errorf("declining must say so rather than exiting silently: %q", prompt)
	}
}

// THE MUTATION DETECTOR for the ordinary force path.
//
// With the identity read refused, `forced` stays nil — NOT false — because it
// could not be established, and the holder may therefore BE the caller. The
// prompt must hedge. Passing a computed "forced" or a hard `true` here would
// assert a public act that may not happen, which is #504's "unknown is not
// none"; before this test that substitution was invisible.
func TestWorkerReleaseForcePromptHedgesWhenIdentityIsUnreadable(t *testing.T) {
	gql, _ := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
		"AuthContext": authContextUnreadableJSON,
		"GetUser":     daraUserJSON,
	}))
	f, _, errOut := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	_ = root.Execute()

	prompt := errOut.String()
	if !strings.Contains(prompt, "could not read your own identity") {
		t.Errorf("an unreadable identity must HEDGE, not assert a force-release: %q", prompt)
	}
	// The flat wording belongs to the KNOWN branch only. Asserting its absence
	// is what makes this a detector rather than a description: a hard `true`
	// argument produces "not you" and nothing else here would notice.
	if strings.Contains(prompt, "not you") {
		t.Errorf("the caller may BE the holder — the prompt must not claim otherwise: %q", prompt)
	}
}

// …and when the identity IS readable, the prompt states the force flatly.
// Reusing the "if it is not" hedge for the case we are CERTAIN about made the
// one branch we know sound conditional, which is backwards for a warning that a
// public act is about to happen (PR #504 review). Pinned in both directions so
// neither wording can drift into the other's branch.
func TestWorkerReleaseForcePromptStatesTheForceFlatlyWhenKnown(t *testing.T) {
	gql, _ := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
		"GetUser": daraUserJSON,
	}))
	f, _, errOut := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	_ = root.Execute()

	prompt := errOut.String()
	if !strings.Contains(prompt, "not you") {
		t.Errorf("a known force must be stated flatly: %q", prompt)
	}
	if strings.Contains(prompt, "could not read your own identity") {
		t.Errorf("hedging a case we are certain about is backwards: %q", prompt)
	}
}

// THE MUTATION DETECTOR the issue was filed for.
//
// This is the retry after WORKER_HOLD_STALE — the second confirmation on this
// command, and the call site whose `meKnown` argument could be flipped to true
// with the whole suite still green. The retry's prompt reuses releasePrompt
// precisely so the hedge cannot be lost in a copy; that reuse is only load-
// bearing if something reads the result.
func TestWorkerReleaseStalePromptHedgesWhenIdentityIsUnreadable(t *testing.T) {
	srv, seen := releaseSequence(t, irisWorkerJSON,
		[]string{holdStale("u-gil"), releasedFromOK("u-gil")}, map[string]string{
			"AuthContext": authContextUnreadableJSON,
			"GetUser": `{"data":{"user":{"id":"u-gil","name":"Gil","email":null,"handle":"gil",
				"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
				"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
		})
	f, out, errOut := testFactoryTTY(t, "y\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("consenting to the retry must proceed: %v", err)
	}

	prompt := errOut.String()
	// The retry's OWN context first: what the caller is being asked a second
	// time, and that nothing has happened yet.
	if !strings.Contains(prompt, "not what this command classified") ||
		!strings.Contains(prompt, "nothing has been changed yet") {
		t.Errorf("the retry must say why it is asking again and that nothing changed: %q", prompt)
	}
	// Then the hedge, carried over from releasePrompt rather than re-written.
	if !strings.Contains(prompt, "could not read your own identity") {
		t.Errorf("the retry must inherit the unknown-identity hedge: %q", prompt)
	}
	if strings.Contains(prompt, "not you") {
		t.Errorf("meKnown is false here — the retry must not assert a force-release: %q", prompt)
	}
	// Consent given, so the retry runs — against the holder the SERVER named.
	if len(*seen) != 2 {
		t.Fatalf("want the refused call and one consented retry, got %d", len(*seen))
	}
	if got := (*seen)[1].ExpectedHolderUserID; got == nil || *got != "u-gil" {
		t.Errorf("the retry must assert the holder the prompt named, got %v", deref(got))
	}
	// The consented path RUNS TO A RECEIPT. Reaching the prompt and then
	// asserting only on the prompt would leave the branch half-driven — the
	// same "green result, mechanism never ran" shape as the refusal-only
	// coverage this test exists to replace.
	// FORCE-released, and named. The PROMPT above hedges because this CLI could
	// not read its own identity; the RECEIPT does not, because #1073 has the
	// server classify the act. Two different jobs that used to share one
	// nullable answer — and the hedge belonged to only one of them.
	if receipt := out.String(); !strings.Contains(receipt, "✓ force-released Iris from Gil") {
		t.Errorf("a consented retry must report what it did: %q", receipt)
	}
}

// The TWO-CONFIRMATION path: `worker release` asks for the force-release, the
// server then answers WORKER_HOLD_STALE, and it asks again. One command, two
// prompts, two answers.
//
// This is the case @codex found the seam could not serve on PR #531 — a
// strings.Reader hands the whole script to the FIRST prompt's scanner, so the
// second saw EOF and returned Cancelled: a decline nobody wrote, wearing the
// face of a prompt working correctly. Pinned at the command level rather than
// only on the reader, because "both prompts got their own answer" is the
// property that matters and the reader is only how it is achieved.
func TestWorkerReleaseAnswersBothPromptsInOneRun(t *testing.T) {
	srv, seen := releaseSequence(t, heldBy("u-dara"),
		[]string{holdStale("u-gil"), releasedFromOK("u-gil")}, map[string]string{
			"GetUser": `{"data":{"user":{"id":"u-gil","name":"Gil","email":null,"handle":"gil",
				"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
				"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
		})
	f, out, errOut := testFactoryTTY(t, "y\ny\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("both prompts were answered yes — the release must complete: %v", err)
	}

	prompt := errOut.String()
	// The force prompt…
	if !strings.Contains(prompt, "POSTS TO THE TEAM CHAT") {
		t.Errorf("the first prompt is missing: %q", prompt)
	}
	// …and then the retry's own, which only renders if the second answer
	// arrived. Before the fix this run died at Cancelled with one prompt shown.
	if !strings.Contains(prompt, "not what this command classified") {
		t.Errorf("the second prompt never got its answer: %q", prompt)
	}
	if len(*seen) != 2 {
		t.Fatalf("want the refused call and the consented retry, got %d", len(*seen))
	}
	// FORCE-released, not merely released: the name was held by someone else,
	// which is what both prompts warned about. The receipt must say the act the
	// caller consented to, not a milder one.
	if receipt := out.String(); !strings.Contains(receipt, "✓ force-released Iris from Gil") {
		t.Errorf("a doubly-consented force-release must report what it did: %q", receipt)
	}
}

// Declining the retry stops after the refused call. The prompt's whole purpose
// is that a force-release the caller did not consent to does not happen, and
// "the second call is absent" is the only evidence of that.
func TestWorkerReleaseStalePromptDeclineStopsAfterTheRefusal(t *testing.T) {
	srv, seen := releaseSequence(t, irisWorkerJSON,
		[]string{holdStale("u-gil"), releasedFromOK("u-gil")}, map[string]string{
			"GetUser": `{"data":{"user":{"id":"u-gil","name":"Gil","email":null,"handle":"gil",
				"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
				"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
		})
	f, _, errOut := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", srv.URL})

	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Cancelled {
		t.Errorf("declining the retry must exit Cancelled, got %d (err: %v)", code, err)
	}
	if len(*seen) != 1 {
		t.Errorf("a declined retry must not run, got %d release calls", len(*seen))
	}
	// Identity readable here, so the retry states the force flatly — the same
	// both-directions pin as the first prompt, on the second call site.
	if prompt := errOut.String(); !strings.Contains(prompt, "not you") {
		t.Errorf("a known force must be stated flatly on the retry too: %q", prompt)
	}
}
