package cmd

// hadron-cli#1524 C3: `team task find-untracked` over hadron-server#3125 S3
// (cor:agt:120:03). One call per invocation, every flag passed through, the
// server's bounds and refusals mapped; plan A3 for incomplete claims.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func untrackedPage1524(items string, total int, complete bool, reason string) string {
	r := "null"
	if reason != "" {
		r = `"` + reason + `"`
	}
	b, _ := json.Marshal(complete)
	return `{"data":{"untrackedTaskItems":{"items":[` + items + `],"total":` + itoa1524(total) + `,"complete":` + string(b) + `,"incompleteReason":` + r + `}}}`
}

func itoa1524(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

var claimedTaskJSON1524 = strings.NewReplacer(
	`"status":"PENDING","queued":true`, `"status":"STARTED","queued":false`,
	`"source":`, `"externalId":"https://github.com/acme/app/issues/42","source":`,
).Replace(taskFieldsJSON)

func claimResult1524(tasks string, complete bool, reason, refusal string) string {
	q := func(s string) string {
		if s == "" {
			return "null"
		}
		return `"` + s + `"`
	}
	b, _ := json.Marshal(complete)
	return `{"data":{"claimUntrackedTaskItems":{"tasks":[` + tasks + `],"complete":` + string(b) + `,"incompleteReason":` + q(reason) + `,"refusalCode":` + q(refusal) + `}}}`
}

func runFind1524(t *testing.T, responses map[string]string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	calls, stdout, stderr, err := runTeamTaskAll1524(t, responses, append([]string{"team", "task", "find-untracked"}, argv...)...)
	return calls, stdout, stderr, err
}

func runTeamTaskAll1524(t *testing.T, responses map[string]string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

// findCalls1524 are the calls the command made, without the binding's App reads.
func findCalls1524(calls *[]attnCall) []attnCall {
	var got []attnCall
	for _, c := range *calls {
		if c.Op == "UntrackedTaskItems" || c.Op == "ClaimUntrackedTaskItems" {
			got = append(got, c)
		}
	}
	return got
}

func TestFindUntrackedUsage1524(t *testing.T) {
	page := map[string]string{"UntrackedTaskItems": untrackedPage1524("", 0, true, "")}
	for name, argv := range map[string][]string{
		"no --integration":             {},
		"--assign-to-me with --skip":   {"--integration", "github", "--assign-to-me", "--skip", "2"},
		"--ready outside the enum":     {"--integration", "github", "--ready", "soon"},
		"a bare --team (no value)":     {"--integration", "github", "--team"},
		"an empty --integration value": {"--integration", " "},
	} {
		t.Run(name, func(t *testing.T) {
			calls, _, _, err := runFind1524(t, page, argv...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
			}
			if strings.Contains(err.Error(), "unknown command") || strings.Contains(err.Error(), "unknown flag: --integration") {
				t.Fatalf("the command must exist: %v", err)
			}
			if len(findCalls1524(calls)) != 0 {
				t.Errorf("refused before any call: %v", *calls)
			}
		})
	}
}
