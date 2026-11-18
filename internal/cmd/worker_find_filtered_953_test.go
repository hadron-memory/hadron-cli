package cmd

import (
	"strings"
	"testing"
)

func TestWorkerFindFilteredHelp953(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "find-filtered", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"find-filtered", "report-only", "BLOCKED", "AMBIGUOUS", "UNREADABLE"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "--apply") {
		t.Fatal("recovery flag exposed")
	}
}

func TestWorkerFindFilteredNonOrcaBeforeRead953(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "find-filtered"})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "Orca-hosted workers only") || out.String() != "" {
		t.Fatalf("non-Orca refusal: err=%v out=%q", err, out.String())
	}
}
