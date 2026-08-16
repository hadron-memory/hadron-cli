package cmd

import (
	"strings"
	"testing"
)

// #441: the `role` group has no rm verb (no deleteTeamRole —
// hadron-server#1002), so retiring a role means deleting its node past this
// group, and handing its register to a successor is ORDER-DEPENDENT. Neither
// fact is derivable from the verbs on offer, so `team role --help` must carry
// the sequence or the next person rediscovers it from two typed refusals.
func TestRoleHelpCarriesTheSupersedeOrder(t *testing.T) {
	// Cobra writes help to its own writer, not the Factory's IOStreams.
	buf := &strings.Builder{}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(buf)
	root.SetArgs([]string{"team", "role", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	help := buf.String()

	// The ORDER is the contract — asserting only that the steps are present
	// would let the two failing permutations the issue reports pass.
	steps := []string{
		"hadron team role get <old> --json", // capture, before the delete destroys it
		"hadron node rm",                    // frees the names
		"hadron team role update <new> --name-range",
		// `add`, not `set`: set replaces the successor's register wholesale,
		// which drops its own free names and refuses on a minted one — at the
		// one point in the sequence where the old definition is already gone
		// (PR #447 review).
		"hadron team role names add <new>",
		"hadron app agent remove <app> <old-agent> --yes",
		// The cleanup is TWO commands, and both are confirmation-gated: an
		// agent running this sequence off a TTY stalls on the last step
		// without --yes. Asserting the flag inside the marker covers both.
		"hadron agent rm <old-agent> --yes",
	}
	prev := -1
	for _, want := range steps {
		i := strings.Index(help, want)
		if i < 0 {
			t.Fatalf("team role --help must name the %q step:\n%s", want, help)
		}
		if i <= prev {
			t.Errorf("step %q is out of order (position %d, previous %d):\n%s", want, i, prev, help)
		}
		prev = i
	}

	// The refusals are what make the order load-bearing; naming them is how a
	// reader who already hit one finds this passage.
	for _, want := range []string{"TEAM_ROLE_NAME_DUPLICATE", "TEAM_ROLE_NAME_OUT_OF_RANGE"} {
		if !strings.Contains(help, want) {
			t.Errorf("help should name the %s refusal the order exists to avoid:\n%s", want, help)
		}
	}
	// Why there is no rm verb — otherwise the gap reads as an oversight.
	if !strings.Contains(help, "deleteTeamRole") {
		t.Errorf("help should say the rm verb waits on a platform surface (deleteTeamRole):\n%s", help)
	}
}
