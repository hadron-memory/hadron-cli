package cmd

// hadron-cli#1524 C3: the find-untracked runner the adapter tests (cli#1961)
// share. The server's task integration path it was first written for is
// retired (cli#1964); that path's tests and fixtures went with it.

import (
	"strings"
	"testing"
)

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
