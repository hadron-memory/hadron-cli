package cmd

import (
	"regexp"
	"strings"
	"testing"
)

// cli#2959: WORKER_CENSUS_INCOMPLETE is the server's fail-closed refusal when its reference census
// does not finish: nothing was deleted and no reference was found, so the outcome is known (exit 5,
// the class of its sibling WORKER_IN_USE), and the remedy is to retry later or retire. The help and the
// agent contract name it that way, and never as "in use", never as safe, never with the server's
// operational time budget.

// rmCensusHelp2959 is the whitespace-flattened `team worker rm --help`.
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

// censusFacts2959 are the claims both texts carry. The refusal says what is KNOWN (nothing was
// deleted) and what is NOT (whether references exist: found nor ruled out), never "not in use".
var censusFacts2959 = []string{
	"nothing was deleted",
	"references were neither found nor ruled out",
	"never means the worker is unused or the delete is safe",
	"retry later or retire",
}

// censusForbidden2959: reassurance and operational detail the refusal must not carry.
var censusForbidden2959 = regexp.MustCompile(`(?i)no reference was found|is not "in use"|20[- ]?seconds?|20s\b`)

func TestWorkerRmHelpNamesTheCensusRefusal2959(t *testing.T) {
	help := rmCensusHelp2959(t)
	for _, want := range append([]string{"WORKER_CENSUS_INCOMPLETE", "A passing --dry-run is not a promise"}, censusFacts2959...) {
		if !strings.Contains(help, want) {
			t.Errorf("worker rm --help must say %q:\n%s", want, help)
		}
	}
	if m := censusForbidden2959.FindString(help); m != "" {
		t.Errorf("help must not say %q", m)
	}
}

func TestAgenticUsageNamesTheCensusRefusal2959(t *testing.T) {
	contract := flat2906(string(readContract(t)))
	for _, want := range append([]string{"`WORKER_CENSUS_INCOMPLETE` (exit 5)", "a passing preview is not a promise", "no bypass flag exists"}, censusFacts2959...) {
		if !strings.Contains(contract, want) {
			t.Errorf("agentic-usage must say %q about the census refusal", want)
		}
	}
	// Scoped to the worker rm entry: other entries may use the phrases elsewhere.
	i := strings.Index(contract, "`WORKER_CENSUS_INCOMPLETE` (exit 5)")
	if i >= 0 {
		end := i + 700
		if end > len(contract) {
			end = len(contract)
		}
		if m := censusForbidden2959.FindString(contract[i:end]); m != "" {
			t.Errorf("agentic-usage must not say %q near the census refusal", m)
		}
	}
}
