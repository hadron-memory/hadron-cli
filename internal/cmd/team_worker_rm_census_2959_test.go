package cmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2959: WORKER_CENSUS_INCOMPLETE is the server's fail-closed refusal when its reference census
// does not finish: nothing was deleted and the references were neither found nor ruled out, so the
// outcome is known (exit 5, the class of its sibling WORKER_IN_USE), and the remedy is to retry later or
// retire. The help and the agent contract name it that way, and never as "in use", never as safe, never
// with the server's operational time budget. cor:dmo:050:11 governs the server's refusal (the code, the
// worker id, fail closed); the CLI's wording is the CLI owner's own and is not that clause's text.

// rmCensusHelp2959 is the whitespace-flattened `team worker rm --help`.
func rmCensusHelp2959(t *testing.T) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "rm", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return flat(out.String())
}

// censusFacts2959 are the claims both texts carry. The refusal says what is KNOWN (nothing was
// deleted) and what is NOT (whether references exist: found nor ruled out), never "not in use".
var censusFacts2959 = []string{
	"nothing was deleted",
	"references were neither found nor ruled out",
	"never means the worker is unused or the delete is safe",
	"retry later or retire",
	"no bypass flag exists",
	"a script branches on the --json failure envelope's serverCode",
}

// censusForbidden2959: reassurance and operational detail the refusal must not carry.
var censusForbidden2959 = regexp.MustCompile(`(?i)no references? (?:was|were) found|(?:is|are) not "?in use"?|safe to delete|20[- ]?(?:seconds?|s\b)|time budget`)

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
	contract := flat(string(readContract(t)))
	for _, want := range append([]string{"`WORKER_CENSUS_INCOMPLETE` (exit 5)", "a passing preview is not a promise"}, censusFacts2959...) {
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

// cli#2982: a script cannot tell WORKER_CENSUS_INCOMPLETE from WORKER_IN_USE by exit code (both 5), so the
// --json failure envelope's serverCode must carry it, on the real delete as well as the dry run.
func TestWorkerRmCensusRefusalKeepsServerCode2982(t *testing.T) {
	const code = "WORKER_CENSUS_INCOMPLETE"
	refusal := fmt.Sprintf(`{"errors":[{"message":"census unfinished","extensions":{"code":%q}}]}`, code)
	for _, tc := range []struct {
		name string
		args []string
		op   string
	}{
		{"real delete", []string{"--yes", "--confirm-no-external-attribution"}, "DeleteWorker"},
		{"dry run", []string{"--dry-run"}, "DeleteWorkerPreview"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ops []string
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				ops = append(ops, op)
				switch op {
				case "Workers", "WorkersRoster":
					return staffJSON
				case "GetWorkerInApp":
					return `{"data":{"worker":` + irisWorkerJSON + `}}`
				case tc.op:
					return refusal
				}
				return ""
			})
			f, out := testFactory(t)
			f.JSON = true
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"team", "worker", "rm", "Iris", "--json", "--app", "acme.com:eng-team", "--server", gql.URL}, tc.args...))
			err := root.Execute()
			if got := exitCodeFor(err); got != exitcode.Conflict {
				t.Fatalf("exit %d want %d: %v", got, exitcode.Conflict, err)
			}
			renderError(f, err)
			var env struct {
				Error map[string]any `json:"error"`
			}
			if e := json.Unmarshal([]byte(out.String()), &env); e != nil || env.Error["serverCode"] != code {
				t.Fatalf("envelope must carry serverCode %s: %v %v\n%q", code, e, env.Error, out.String())
			}
			n := 0
			for _, o := range ops {
				if o == tc.op {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("%s sent %d times (want one, no retry): %v", tc.op, n, ops)
			}
		})
	}
}
