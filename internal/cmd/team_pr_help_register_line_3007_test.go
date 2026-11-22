package cmd

import (
	"strings"
	"testing"
)

// cli#3007: after #2979 a `team pr` write prints the task line and then ONE
// register line, and no dash. The help must teach that, not the old receipt.
func TestTeamPRHelpDescribesTheRegisterLine3007(t *testing.T) {
	leaves := [][]string{
		{"team", "pr"},
		{"team", "pr", "schedule"},
		{"team", "pr", "freeze"},
		{"team", "pr", "seat"},
		{"team", "pr", "verdict"},
		{"team", "pr", "ready"},
	}
	for _, args := range leaves {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{}, args...), "--help"))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		help := strings.Join(strings.Fields(out.String()), " ")
		name := strings.Join(args, " ")
		for _, gone := range []string{"the recorded action", "dash in text"} {
			if strings.Contains(help, gone) {
				t.Errorf("%s help still says %q:\n%s", name, gone, help)
			}
		}
		want := "the register line"
		if len(args) == 2 {
			want = "one register line"
		}
		if !strings.Contains(help, want) {
			t.Errorf("%s help missing %q:\n%s", name, want, help)
		}
		if len(args) == 2 {
			for _, s := range []string{"UNMAPPED", "never whether the write recorded", "the task line is the receipt"} {
				if !strings.Contains(help, s) {
					t.Errorf("group help missing %q:\n%s", s, help)
				}
			}
		}
	}
}
