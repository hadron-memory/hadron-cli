package cmd

import (
	"strings"
	"testing"
)

// cli#1149, ruling 0144 (cor:dmo:120:06): `spec mint` is memory-wide. One call
// mints every eligible node of the memory (hadron-server#1591), not only the
// specs, so its help and the usage guide must not describe the scope as "a spec
// corpus". Text only: the report, the request and the behaviour are unchanged.

func mintHelp1149(t *testing.T) (short, long string) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(out)
	root.SetArgs([]string{"spec", "mint", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	mint, _, err := root.Find([]string{"spec", "mint"})
	if err != nil {
		t.Fatal(err)
	}
	return mint.Short, strings.Join(strings.Fields(out.String()), " ")
}

func TestSpecMintHelpStatesTheWholeMemoryScope(t *testing.T) {
	short, help := mintHelp1149(t)
	if !strings.Contains(short, "memory") || strings.Contains(strings.ToLower(short), "spec corpus") {
		t.Errorf("Short must name the memory and not a spec corpus, got %q", short)
	}
	for _, want := range []string{"every eligible node of the memory", "not only specs", "cor:dmo:120:06", "only comments are outside it"} {
		if !strings.Contains(help, want) {
			t.Errorf("spec mint --help must say %q:\n%s", want, help)
		}
	}
	// The dry run is the place to see what a mint would cover.
	if !strings.Contains(help, "eligible nodes") {
		t.Errorf("spec mint --help must point at the report's list of eligible nodes:\n%s", help)
	}
	for _, bad := range []string{"in a spec corpus", "in the spec corpus", "a spec corpus."} {
		if strings.Contains(help, bad) {
			t.Errorf("spec mint --help must not claim a corpus-only scope (%q):\n%s", bad, help)
		}
	}
}
