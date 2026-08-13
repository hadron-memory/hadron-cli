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

	// The steps that are NOT in this group are the ones nobody would guess.
	for _, want := range []string{"agent create", "app install", "roles:<role>", "persona create"} {
		if !strings.Contains(help, want) {
			t.Errorf("team --help must name the %q step: %s", want, help)
		}
	}
	// `team init` sounds like step one and is nearly last — say so, since
	// that specific inversion is what the issue reports.
	initIdx := strings.Index(help, "hadron team init")
	mintIdx := strings.Index(help, "hadron team persona create")
	if initIdx < 0 || mintIdx < 0 || initIdx < mintIdx {
		t.Errorf("`team init` must appear AFTER minting, not before: init=%d mint=%d", initIdx, mintIdx)
	}
	if !strings.Contains(help, "optional") {
		t.Errorf("`team init` is not a precondition — help should say so: %s", help)
	}
	// Name permanence is the irreversible part of the sequence.
	if !strings.Contains(help, "PERMANENT") {
		t.Errorf("help should warn that a minted name is permanent: %s", help)
	}
}
