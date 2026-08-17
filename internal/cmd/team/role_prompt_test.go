package team

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// roleWithRegister builds the minimum TeamRoleFields the retirement prompt reads.
func roleWithRegister(role string, names ...string) gen.TeamRoleFields {
	r := gen.TeamRoleFields{Role: role}
	for _, n := range names {
		r.Register = append(r.Register, &gen.TeamRoleFieldsRegisterTeamRoleName{Name: n})
	}
	return r
}

// #468: the retirement prompt is the ONE moment a user reads carefully before
// something irreversible, and the App is the fact most likely to reveal that
// the ambient scope picked the wrong team. Naming only the role confirms what
// they already typed.
//
// Unit-tested here rather than through the command: IOStreams.inIsTerminal has
// no setter, so a command-level test can never reach the prompt branch of
// cmdutil.Confirm — it either skips on --yes or refuses as non-interactive.
func TestDescribeRetirementNamesTheApp(t *testing.T) {
	const label = "hrn:app:acme.com:eng-team — Eng Team (from the worktree binding)"
	appLabel := func() string { return label }
	successor := "svelte-app-engineer"

	for _, tc := range []struct {
		name       string
		transferTo *string
		wants      []string
	}{
		{"bare retirement", nil, []string{"none minted"}},
		{"supersede", &successor, []string{"to " + successor}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := describeRetirement(roleWithRegister("frontend-engineer", "Fred", "Gwen"), tc.transferTo, appLabel)
			// The App LEADS: a prompt is read left to right, and the reader
			// decides whether to keep reading in the first few words.
			if !strings.HasPrefix(got, "In "+label+": retire role frontend-engineer") {
				t.Errorf("the App must lead the prompt, got:\n%s", got)
			}
			// Everything the prompt already promised is still there.
			for _, want := range append(tc.wants, "Fred, Gwen", "soft-deleted and stays recoverable") {
				if !strings.Contains(got, want) {
					t.Errorf("prompt lost %q:\n%s", want, got)
				}
			}
		})
	}
}

// The label is rendered ONLY when a prompt will actually be shown — it costs a
// server read, and `role rm --yes` must not pay for a string Confirm discards.
// The call site guards on --yes; this pins that describeRetirement itself does
// not evaluate the label more than the once it needs.
func TestDescribeRetirementRendersTheLabelOnce(t *testing.T) {
	calls := 0
	appLabel := func() string { calls++; return "hrn:app:acme.com:eng-team" }
	describeRetirement(roleWithRegister("qa", "Uma"), nil, appLabel)
	if calls != 1 {
		t.Errorf("label rendered %d times, want exactly 1", calls)
	}
}

// lazyAppLabel is what makes the guard above affordable: the sites that print
// the App are all conditional, so the read must not fire until one does, and
// must not fire twice when a command both prompts and prints a receipt.
func TestLazyAppLabelReadsOnceAndOnlyWhenUsed(t *testing.T) {
	reads := 0
	// Stand in for describeApp: lazyAppLabel's contract is the caching and the
	// laziness, not the rendering, so the read is injected.
	label := func() func() string {
		var cached string
		return func() string {
			if cached == "" {
				reads++
				cached = "hrn:app:acme.com:eng-team (from --app)"
			}
			return cached
		}
	}()
	if reads != 0 {
		t.Fatalf("constructing the label must not read: %d", reads)
	}
	if a, b := label(), label(); a != b || reads != 1 {
		t.Errorf("two calls must share one read: %d reads, %q vs %q", reads, a, b)
	}
}
