package cmd

// cli#1843: an approval on the default path acts as the signed-in person.
// From a worktree bound to a worker session the CLI says so, once, on stderr,
// and names --as-worker. Nothing sent changes (P20 stays pinned in
// as_worker_1872_test.go); stdout and --json are byte-identical.

import (
	"strings"
	"testing"
)

const approvalNotePrefix1843 = "note: approving as you (the signed-in person)"

// runApprovalNote runs argv (after --server) and returns stdout, stderr, err.
// bound puts the process in the bound worktree; otherwise in a directory that
// is no worktree.
func runApprovalNote1843(t *testing.T, bound bool, kind string, argv ...string) (string, string, error) {
	t.Helper()
	var server string
	switch kind {
	case "node":
		srv, _ := attnServer(t, map[string]string{"ResolveUrn": resolveNodeJSON, "ApproveNode": asWorkerApproveNodeJSON})
		server = srv.URL
	case "all":
		gql, _ := captureGraphQL(t, map[string]string{
			"ApproveMemoryNodes": `{"data":{"approveMemoryNodes":{"approvedCount":0,"alreadyApprovedCount":0,"skippedPlaceholders":[],"approved":[]}}}`,
		})
		server = gql.URL
	case "team":
		srv, _ := newApproveFake(t, approveTeam())
		server = srv.URL
	}
	if bound {
		writeTeamBinding(t)
		setTeamBindingServer(t, server)
	} else {
		noWorktreeCwd(t)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", server}, argv...))
	err := root.Execute()
	return out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

func nodeApproveArgs1843(extra ...string) []string {
	return append([]string{"node", "approve", nodeURN, "--revision", "4"}, extra...)
}

func teamApproveArgs1843(extra ...string) []string {
	return append([]string{"--app", batchApp, "team", "worker", "approve"}, extra...)
}

func wantOneNote1843(t *testing.T, stderr string, asWorkerNamed bool) {
	t.Helper()
	if n := strings.Count(stderr, approvalNotePrefix1843); n != 1 {
		t.Fatalf("want exactly one note, got %d in %q", n, stderr)
	}
	if !strings.Contains(stderr, "not as the bound worker Iris") {
		t.Errorf("the note does not name the bound worker: %q", stderr)
	}
	if got := strings.Contains(stderr, "--as-worker"); got != asWorkerNamed {
		t.Errorf("--as-worker named = %v, want %v: %q", got, asWorkerNamed, stderr)
	}
}

// The note says a BOOT PROMPT needs a grant, never that approving needs one:
// an ordinary node's approval follows cor:dmo:120:02 (Victor's FIT).
func TestApprovalNoteNeverClaimsAGrantForOrdinaryNodes1843(t *testing.T) {
	_, stderr, err := runApprovalNote1843(t, true, "node", nodeApproveArgs1843()...)
	if err != nil {
		t.Fatal(err)
	}
	wantOneNote1843(t, stderr, true)
	if !strings.Contains(stderr, "a boot prompt then needs a batch grant a person signed (cor:agt:040:18)") {
		t.Errorf("grant clause is not scoped to a boot prompt: %q", stderr)
	}
	for _, bad := range []string{"requires a", "needs a batch grant a person signed.", "under a batch grant"} {
		if strings.Contains(stderr, bad) {
			t.Errorf("note implies a grant for every node (%q): %q", bad, stderr)
		}
	}
}

func TestApprovalNoteOnEveryDefaultApproval1843(t *testing.T) {
	_, stderr, err := runApprovalNote1843(t, true, "team", teamApproveArgs1843("--apply")...)
	if err != nil {
		t.Fatal(err)
	}
	wantOneNote1843(t, stderr, true)

	// memory approve-all has no --as-worker, so its note does not name one.
	_, stderr, err = runApprovalNote1843(t, true, "all", "memory", "approve-all", "-m", "acme.com::kb", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	wantOneNote1843(t, stderr, false)
}

func TestApprovalNoteAbsentWhenNotApplicable1843(t *testing.T) {
	cases := map[string]struct {
		bound bool
		kind  string
		argv  []string
	}{
		"--as-worker":              {true, "node", nodeApproveArgs1843("--as-worker")},
		"--as-worker batch":        {true, "team", teamApproveArgs1843("--apply", "--as-worker")},
		"unbound directory":        {false, "node", nodeApproveArgs1843()},
		"unbound directory (all)":  {false, "all", []string{"memory", "approve-all", "-m", "acme.com::kb", "--yes"}},
		"team approve preview":     {true, "team", teamApproveArgs1843()},
		"unbound batch with apply": {false, "team", teamApproveArgs1843("--apply")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, stderr, _ := runApprovalNote1843(t, c.bound, c.kind, c.argv...)
			if strings.Contains(stderr, "approving as you") {
				t.Fatalf("unexpected note: %q", stderr)
			}
		})
	}
}

// The note is stderr only: --json stdout is the same bound or not.
func TestApprovalNoteLeavesStdoutAlone1843(t *testing.T) {
	boundOut, boundErr, err := runApprovalNote1843(t, true, "node", nodeApproveArgs1843("--json")...)
	if err != nil {
		t.Fatal(err)
	}
	unboundOut, unboundErr, err := runApprovalNote1843(t, false, "node", nodeApproveArgs1843("--json")...)
	if err != nil {
		t.Fatal(err)
	}
	if boundOut != unboundOut || strings.Contains(boundOut, "approving as you") {
		t.Fatalf("stdout differs:\nbound   %q\nunbound %q", boundOut, unboundOut)
	}
	if !strings.Contains(boundErr, approvalNotePrefix1843) || strings.Contains(unboundErr, "approving as you") {
		t.Fatalf("stderr: bound %q unbound %q", boundErr, unboundErr)
	}
}
