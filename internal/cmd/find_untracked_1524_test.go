package cmd

// hadron-cli#1524 C3 fixtures: the claimed-task JSON and the find-untracked
// runner the adapter tests (cli#1961) share. The server's task integration
// path they were first written for is retired (cli#1964); its tests went with
// it.

import (
	"encoding/json"
	"strings"
	"testing"
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
