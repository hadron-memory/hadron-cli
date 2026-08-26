package team

import (
	"strings"
	"testing"
)

// The retry confirmation must PRESERVE the identity uncertainty (#524 review,
// @codex P2).
//
// When the AuthContext read failed, the stale holder may BE the caller — the
// hold was simply never readable — so a prompt asserting an admin force-release
// and a team-chat notice describes a public act that may not happen. That is
// #504's "unknown is not none", arriving in the one branch that had not learned
// it, and it is worse in a prompt than in a receipt: the receipt reports what
// happened, while the prompt is what someone consents on.
//
// Driven HERE rather than through the command: the prompt only renders on a
// TTY, `Confirm` refuses without one, and a mutation flipping meKnown to true
// at the call site passed the whole command suite.
func TestStaleReleasePromptPreservesIdentityUncertainty(t *testing.T) {
	const name, holder = "Iris", "Gil (@gil)"

	unknown := staleReleasePrompt(name, holder, nil, false)
	if !strings.Contains(unknown, "could not read your own identity") {
		t.Errorf("an unreadable identity must be stated, not classified: %s", unknown)
	}
	// The KNOWN branch's flat claim must not appear: "not you" is the sentence
	// that turns a hedge into an assertion.
	if strings.Contains(unknown, "not you") {
		t.Errorf("this asserts a relationship it has not established: %s", unknown)
	}

	known := staleReleasePrompt(name, holder, nil, true)
	if !strings.Contains(known, "not you") {
		t.Errorf("the case we ARE certain about must say so flatly: %s", known)
	}
	if strings.Contains(known, "could not read your own identity") {
		t.Errorf("hedging a known force-release makes the certain case sound conditional: %s", known)
	}

	// Both carry the retry's own context — nothing has happened yet — because a
	// caller meeting this prompt has already had one release attempt refused
	// and needs to know the name is untouched.
	for label, got := range map[string]string{"unknown": unknown, "known": known} {
		if !strings.Contains(got, "nothing has been changed yet") {
			t.Errorf("%s: the retry prompt must say the name is untouched: %s", label, got)
		}
		if !strings.Contains(got, holder) {
			t.Errorf("%s: the prompt must name the holder the SERVER found: %s", label, got)
		}
	}

	// The transfer clause still branches on retirement — the detail that would
	// drift out of a hand-rolled second prompt, which is why this composes
	// releasePrompt instead of restating it.
	retiredAt := "2026-08-21T00:00:00Z"
	retired := staleReleasePrompt(name, holder, &retiredAt, true)
	if !strings.Contains(retired, "stay with the name") {
		t.Errorf("a retired worker has no next holder to promise: %s", retired)
	}
	if strings.Contains(retired, "whoever takes the name next") {
		t.Errorf("nobody can bind a retired name: %s", retired)
	}
}
