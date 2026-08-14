package cmd

import (
	"strings"
	"testing"
)

// #402: the bootstrap sequence is order-dependent, and two of its steps are
// not in the `team` group at all — so `hadron team --help` must carry the
// order, or the next person rediscovers it from the spec. Re-keyed to the
// Worker model in #428: dressing on the agent, then the App, then the cast.
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

	// The ORDER is the contract, so assert all markers appear in increasing
	// position — checking only init-after-cast would let App-before-Agent or
	// cast-before-dressing pass (PR #421 review).
	steps := []string{
		"hadron agent create",
		"hadron app install",
		"hadron team worker cast",
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
	// The dressing step must show the template placeholders — a persona
	// prompt without {{name}} addresses nobody.
	if !strings.Contains(help, "{{name}}") {
		t.Errorf("the dressing step must show the template placeholder:\n%s", help)
	}
	if !strings.Contains(help, "optional") {
		t.Errorf("`team init` is not a precondition — help should say so: %s", help)
	}
	// Name permanence is the irreversible part of the sequence.
	if !strings.Contains(help, "PERMANENT") {
		t.Errorf("help should warn that a cast name is permanent: %s", help)
	}
}
