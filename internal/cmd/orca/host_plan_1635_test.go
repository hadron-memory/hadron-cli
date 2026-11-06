package orca

import (
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

func TestHostPlan1635Surface(t *testing.T) {
	root := NewCmdOrca(&cmdutil.Factory{})
	cmd, rest, err := root.Find([]string{"host", "plan"})
	if err != nil || cmd == root || cmd.Name() != "plan" || len(rest) != 0 {
		t.Fatalf("read-only host plan command absent: cmd=%s rest=%v err=%v", cmd.Name(), rest, err)
	}
	if cmd.Flags().Lookup("roster-file") == nil {
		t.Fatal("plan must require an explicit roster-file")
	}
	for _, flag := range []string{"apply", "start"} {
		if cmd.Flags().Lookup(flag) != nil {
			t.Fatalf("mutating flag %s must not ship", flag)
		}
	}
	for _, sibling := range cmd.Parent().Commands() {
		if sibling.Name() != "plan" {
			t.Fatalf("host surface exposes unexpected verb %s", sibling.Name())
		}
	}
}
