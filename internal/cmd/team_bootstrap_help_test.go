package cmd

import (
	"strings"
	"testing"
)

// #402: the bootstrap sequence is order-dependent, and two of its steps are
// not in the `team` group at all — so `hadron team --help` must carry the
// order, or the next person rediscovers it from the spec.
func TestTeamHelpCarriesTheBootstrapOrder(t *testing.T) {
	// Cobra writes help to its own writer, not the Factory's IOStreams.
	buf := &strings.Builder{}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(buf)
	root.SetArgs([]string{"team", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	help := buf.String()

	// The ORDER is the contract, so assert all six markers appear in
	// increasing position — checking only init-after-mint would let
	// App-before-Agent or persona-before-role-authoring pass (PR #421 review).
	steps := []string{
		"hadron agent create",
		"hadron app install",
		"roles:<role>",
		"hadron team persona create",
		"hadron team session start",
		"hadron team init",
	}
	prev := -1
	for _, want := range steps {
		i := strings.Index(help, want)
		if i < 0 {
			t.Fatalf("team --help must name the %q step:\n%s", want, help)
		}
		if i <= prev {
			t.Errorf("step %q is out of order (position %d, previous %d):\n%s", want, i, prev, help)
		}
		prev = i
	}
	// The interim role-node command must be executable as printed: `node
	// create` requires --name, and the prompt/register are the whole point
	// (PR #421 review).
	if !strings.Contains(help, "--name backend-engineer") || !strings.Contains(help, "--content-file") ||
		!strings.Contains(help, `"names"`) {
		t.Errorf("the interim role-node command must be complete (name, content, register):\n%s", help)
	}
	if !strings.Contains(help, "optional") {
		t.Errorf("`team init` is not a precondition — help should say so: %s", help)
	}
	// Name permanence is the irreversible part of the sequence.
	if !strings.Contains(help, "PERMANENT") {
		t.Errorf("help should warn that a minted name is permanent: %s", help)
	}
}
