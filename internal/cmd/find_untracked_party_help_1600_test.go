package cmd

import (
	"strings"
	"testing"
)

// cli#1600: with --assignee/--owner the claimed tasks can belong to someone
// else, so the claim paragraph and the flag usage must not say "yours"
// unconditionally.
func TestFindUntrackedClaimHelpQualifiesParties1600(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "find-untracked", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, stale := range []string{"owned by and assigned to you, with", "(they are yours)", "STARTED tasks assigned to you "} {
		if strings.Contains(help, stale) {
			t.Errorf("help still claims the tasks are yours unconditionally: %q", stale)
		}
	}
	for _, want := range []string{
		"owned by and assigned to you unless --owner or --assignee names another party",
		"(they stay claimed)",
		"assigned to you unless --assignee names a party",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}
