package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cli#3007: after #2979 a `team pr` write prints the task line and then ONE
// register line, and no dash. The help must teach that, not the old receipt.
// The output itself is pinned in team_pr_1664_test.go; this pins the help.
func TestTeamPRHelpDescribesTheRegisterLine3007(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	group, _, err := root.Find([]string{"team", "pr"})
	if err != nil || group == nil || group.Name() != "pr" {
		t.Fatalf("team pr not found: %v", err)
	}
	helpOf := func(c *cobra.Command) string {
		return strings.Join(strings.Fields(c.Long+" "+c.Example), " ")
	}

	// Every command under `team pr`, so a future leaf cannot bring the old
	// receipt wording back unnoticed.
	var walk func(c *cobra.Command)
	leaves := 0
	walk = func(c *cobra.Command) {
		help := helpOf(c)
		for _, gone := range []string{"the recorded action", "dash in text"} {
			if strings.Contains(help, gone) {
				t.Errorf("%s help still says %q", c.CommandPath(), gone)
			}
		}
		if c != group && strings.Contains(help, "Prints the") &&
			!strings.Contains(help, "the register line") {
			t.Errorf("%s help says what it prints but not %q", c.CommandPath(), "the register line")
		}
		if c != group {
			leaves++
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(group)
	if leaves < 6 {
		t.Errorf("walked %d leaves under team pr, want at least the six that print a receipt", leaves)
	}

	groupHelp := helpOf(group)
	for _, s := range []string{"register line", "UNMAPPED", "the task line is the receipt"} {
		if !strings.Contains(groupHelp, s) {
			t.Errorf("group help missing %q:\n%s", s, groupHelp)
		}
	}
}
