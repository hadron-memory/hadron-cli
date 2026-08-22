package team

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// #468: the retirement prompt is the ONE moment a user reads carefully before
// something destructive, and the App is the fact most likely to reveal that
// the ambient scope picked the wrong team. Naming only the role confirms what
// they already typed.
//
// The prompt used to have two shapes — a supersede naming the successor, and a
// bare retirement listing the register it was about to discard. hadron#1050
// removed the register, so there is one shape and nothing to enumerate.
//
// Unit-tested here rather than through the command: IOStreams.inIsTerminal has
// no setter, so a command-level test can never reach the prompt branch of
// cmdutil.Confirm — it either skips on --yes or refuses as non-interactive.
func TestDescribeRetirementNamesTheApp(t *testing.T) {
	const label = "hrn:app:acme.com:eng-team — Eng Team (from the worktree binding)"
	appLabel := func() string { return label }

	got := describeRetirement(gen.TeamRoleFields{Role: "frontend-engineer"}, appLabel)
	// The App LEADS: a prompt is read left to right, and the reader decides
	// whether to keep reading in the first few words.
	if !strings.HasPrefix(got, "In "+label+": retire role frontend-engineer") {
		t.Errorf("the App must lead the prompt, got:\n%s", got)
	}
	// The delete is SOFT, and saying so is why this uses Confirm rather than
	// ConfirmDeletion (which would append "This cannot be undone").
	if !strings.Contains(got, "soft-deleted and stays recoverable") {
		t.Errorf("prompt must say the delete is recoverable:\n%s", got)
	}
	// Nothing may promise a hand-off that no longer exists.
	for _, gone := range []string{"minted", "register", "to svelte-app-engineer"} {
		if strings.Contains(got, gone) {
			t.Errorf("prompt still mentions the removed register (%q):\n%s", gone, got)
		}
	}
}

// The label is rendered ONLY when a prompt will actually be shown — it costs a
// server read, and `role rm --yes` must not pay for a string Confirm discards.
// The call site guards on --yes; this pins that describeRetirement itself does
// not evaluate the label more than the once it needs.
func TestDescribeRetirementRendersTheLabelOnce(t *testing.T) {
	calls := 0
	appLabel := func() string { calls++; return "hrn:app:acme.com:eng-team" }
	describeRetirement(gen.TeamRoleFields{Role: "qa"}, appLabel)
	if calls != 1 {
		t.Errorf("label rendered %d times, want exactly 1", calls)
	}
}

// lazyOnce is what makes the guard above affordable: the sites that print the
// App are all conditional, so the read must not fire until one does, and must
// not fire twice when a command both prompts and prints a receipt.
//
// This exercises the REAL helper. The first version of this test reimplemented
// the caching inline and would have passed even if lazyAppLabel stopped being
// lazy — caught in PR #471 review, and the reason the primitive was extracted
// from lazyAppLabel in the first place.
func TestLazyOnceReadsOnceAndOnlyWhenUsed(t *testing.T) {
	reads := 0
	label := lazyOnce(func() string {
		reads++
		return "hrn:app:acme.com:eng-team (from --app)"
	})
	if reads != 0 {
		t.Fatalf("constructing the label must not read: %d", reads)
	}
	if a, b := label(), label(); a != b || reads != 1 {
		t.Errorf("two calls must share one read: %d reads, %q vs %q", reads, a, b)
	}
}

// An empty read result must still be cached — otherwise describeApp returning
// "" (or a legitimately blank label) would re-read on every call, which is the
// bug a `cached == ""` sentinel would have shipped.
func TestLazyOnceCachesAnEmptyResult(t *testing.T) {
	reads := 0
	label := lazyOnce(func() string { reads++; return "" })
	label()
	label()
	if reads != 1 {
		t.Errorf("an empty result must still count as read: %d reads", reads)
	}
}

// releasePromptTransferClause is the half of `worker release`'s force prompt
// that says where the worker's notes go. Unit-tested here for the same reason
// describeRetirement is: cmdutil.Confirm's prompt branch is unreachable without
// a TTY, so a command-level test can never read the string it builds.
//
// The clause is INSTANCE-specific. Nobody takes a retired name (startSession
// refuses WORKER_RETIRED), so promising a next holder there is the same false
// promise the receipt avoids — caught in review after I had fixed the receipts
// and not the prompt, which is the narrow fix the preceding commit was about
// not making (hadron-cli#495 / PR #504).
func TestReleasePromptTransferClauseMatchesRetirement(t *testing.T) {
	at := "2026-08-15T00:00:00Z"
	live, retired := releasePromptTransferClause(nil), releasePromptTransferClause(&at)

	if !strings.Contains(live, "whoever takes the name next") {
		t.Errorf("a live worker's name does pass on: %q", live)
	}
	if strings.Contains(retired, "whoever takes the name next") {
		t.Errorf("nobody takes a retired name: %q", retired)
	}
	// Say where the history goes INSTEAD, rather than just deleting the clause
	// — the transfer is the least guessable consequence of the verb, and a
	// retired worker's notes still travel with the name if it is ever revived.
	if !strings.Contains(retired, "stay with the name") {
		t.Errorf("say where the history goes instead: %q", retired)
	}
	if !strings.Contains(retired, "retired") {
		t.Errorf("say why: %q", retired)
	}
}
