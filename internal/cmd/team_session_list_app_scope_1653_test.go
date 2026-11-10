package cmd

import (
	"strings"
	"testing"
)

// cli#1653: `team session list --app <app>` ignores --app (sessionsPage has no
// App argument, so the listing cannot narrow) and said nothing about it. A
// reader who passes --app reads a table of other Apps' sessions as the answer.
// The ask is a scope line on STDERR when --app is explicit; stdout and --json
// stay byte-identical, and the provenance forms are untouched.

const appScopeFlagRef = "hrn:app:qa-baragaun:qa-team-app"

func runSessionList1653(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	teamGitDir(t)
	gql, _ := captureGraphQL(t, sessionRenderStubs(map[string]string{
		"TeamSessionsPage": `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` + activeSessionJSON + `]}}}`,
		"TeamWorkItems":    emptyWorklog,
		"TeamMemoryApp":    `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`,
	}))
	f, out := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "session", "list", "--server", gql.URL}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}
	return out.String(), errOut.String()
}

func TestSessionListExplicitAppPrintsScopeLineOnStderr(t *testing.T) {
	_, errOut := runSessionList1653(t, "--app", appScopeFlagRef)
	if n := strings.Count(errOut, "does not filter"); n != 1 {
		t.Fatalf("want exactly one scope line, got %d: %q", n, errOut)
	}
	for _, want := range []string{appScopeFlagRef, "(from --app)", "every session you can see", "--as", "--repo", "worker-name"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("scope line lacks %q: %q", want, errOut)
		}
	}
}

func TestSessionListExplicitAppLeavesStdoutAndJSONByteIdentical(t *testing.T) {
	for _, args := range [][]string{nil, {"--json"}} {
		with, _ := runSessionList1653(t, append([]string{"--app", appScopeFlagRef}, args...)...)
		without, errWithout := runSessionList1653(t, args...)
		if with != without {
			t.Errorf("stdout changed by --app (args %v):\nwith:    %q\nwithout: %q", args, with, without)
		}
		if strings.Contains(errWithout, "does not filter") {
			t.Errorf("no --app, no scope line: %q", errWithout)
		}
	}
	_, errJSON := runSessionList1653(t, "--app", appScopeFlagRef, "--json")
	if !strings.Contains(errJSON, "does not filter") {
		t.Errorf("--json still says so on stderr: %q", errJSON)
	}
}

// An ambient App (config, binding) is not an explicit --app: no line.
func TestSessionListAmbientAppPrintsNoScopeLine(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, sessionRenderStubs(map[string]string{
		"TeamSessionsPage": `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` + activeSessionJSON + `]}}}`,
	}))
	f, _ := testFactory(t) // sets XDG_CONFIG_HOME: configuredApp must run after it, or it writes into the cwd
	configuredApp(t, "acme.com:ambient-team")
	if got, err := f.App(); err != nil || got != "hrn:app:acme.com:ambient-team" {
		t.Fatalf("the ambient App must actually resolve for this test to mean anything: %q %v", got, err)
	}
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut.String(), "does not filter") {
		t.Fatalf("ambient App must not print the line: %q", errOut.String())
	}
}

// The provenance form genuinely uses --app: green on main and after, no line.
func TestSessionListProvenanceWithAppPrintsNoNotAppliedLine(t *testing.T) {
	_, errOut := runSessionList1653(t, "--pr", "https://github.com/hadron-memory/urn-lib-js/pull/15", "--app", appScopeFlagRef)
	if strings.Contains(errOut, "does not filter") {
		t.Fatalf("--pr honours --app; it must not say it is not applied: %q", errOut)
	}
}

// The line must stay true when --app is combined with --as (which does use
// --app to resolve the worker NAME) and --repo: it says the App is not a
// filter, never that the flag is ignored outright.
func TestSessionListExplicitAppWithAsAndRepoKeepsTheLineTrue(t *testing.T) {
	_, errOut := runSessionList1653(t, "--app", appScopeFlagRef, "--repo", "hadron-memory/hadron-cli")
	for _, want := range []string{"does not filter", "worker-name", "--repo"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("lacks %q: %q", want, errOut)
		}
	}
	if strings.Contains(errOut, "not applied") || strings.Contains(errOut, "ignored") {
		t.Errorf("must not claim --app is ignored (it scopes --as worker-name lookup): %q", errOut)
	}
}

// A worktree binding alone is an ambient App too: no line.
func TestSessionListBindingAppPrintsNoScopeLine(t *testing.T) {
	writeTeamBinding(t)
	srv, _ := attnServer(t, sessionRenderStubs(map[string]string{
		"TeamSessionsPage": `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` + activeSessionJSON + `]}}}`,
	}))
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut.String(), "does not filter") {
		t.Fatalf("a binding is not an explicit --app: %q", errOut.String())
	}
}
