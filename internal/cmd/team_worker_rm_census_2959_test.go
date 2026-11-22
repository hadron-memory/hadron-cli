package cmd

import (
	"strings"
	"testing"
)

// cli#2959: WORKER_CENSUS_INCOMPLETE is the server's fail-closed refusal when its reference census
// does not finish: nothing was deleted and no reference was found, so the outcome is known (exit 5,
// the class of its sibling WORKER_IN_USE), and the remedy is to retry later or retire. The help and the
// agent contract name it that way, and never as "in use", never as safe, never with the server's
// operational time budget.

func rmCensusHelp2959(t *testing.T) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "rm", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return flat2906(out.String())
}

func TestWorkerRmHelpNamesTheCensusRefusal2959(t *testing.T) {
	help := rmCensusHelp2959(t)
	for _, want := range []string{
		"WORKER_CENSUS_INCOMPLETE",
		"nothing was deleted and no reference was found",
		"never means the delete is safe",
		"retry later or retire",
		"A passing --dry-run is not a promise",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("worker rm --help must say %q:\n%s", want, help)
		}
	}
	for _, bad := range []string{"20 seconds", "20-second", "20s"} {
		if strings.Contains(help, bad) {
			t.Errorf("help must not carry the server's operational budget %q", bad)
		}
	}
}

func TestAgenticUsageNamesTheCensusRefusal2959(t *testing.T) {
	contract := flat2906(string(readContract(t)))
	for _, want := range []string{
		"`WORKER_CENSUS_INCOMPLETE` (exit 5)",
		"nothing was deleted and no reference was found",
		"never means the delete is safe",
		"retry later or retire",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("agentic-usage must say %q about the census refusal", want)
		}
	}
	if strings.Contains(contract, "WORKER_CENSUS_INCOMPLETE` means in use") {
		t.Error("the census refusal is not WORKER_IN_USE")
	}
}
