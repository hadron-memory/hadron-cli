package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Listing tasks needs an App, not a worker session or a git worktree. Both
// ordinary and coordinator views should explain the missing scope before
// making any request (#1284).
func TestTeamTaskListOutsideWorktreeNamesAppRemedy1284(t *testing.T) {
	for _, extra := range [][]string{nil, {"--team=worker:Iris"}} {
		t.Run(strings.Join(extra, " "), func(t *testing.T) {
			t.Setenv(team.GitDirEnv, "")
			t.Chdir(t.TempDir())
			srv, calls := attnServer(t, taskResponses())
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"--server", srv.URL, "team", "task", "list"}, extra...))
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want usage exit 2, got %v", err)
			}
			if !strings.Contains(err.Error(), "no team App — pass --app <ref>") || strings.Contains(err.Error(), "a team session binds to one") {
				t.Fatalf("want the App remedy rather than a worktree/session remedy, got %v", err)
			}
			if out.Len() != 0 || len(*calls) != 0 {
				t.Fatalf("missing scope produced output or requests: %q, %+v", out.String(), *calls)
			}
		})
	}
}

func TestTeamTaskListOutsideWorktreeWithAppStillLists1284(t *testing.T) {
	t.Setenv(team.GitDirEnv, "")
	t.Chdir(t.TempDir())
	srv, calls := attnServer(t, taskResponses())
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "--app", "capp100000000000000000000", "team", "task", "list", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	c := taskListCall(t, calls)
	if c.Vars["appRef"] != "capp100000000000000000000" || c.Session != "" || !strings.Contains(out.String(), "Fix the flaky CI build") {
		t.Fatalf("explicit App lost its read-only listing: %+v, %q", c, out.String())
	}
}

func TestTeamTaskListStillRefusesCorruptBinding1284(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(team.GitDirEnv, dir)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(`{"sessionId":`), 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "list"})
	err := root.Execute()
	if err == nil || strings.Contains(err.Error(), "no team App") {
		t.Fatalf("corrupt binding must not be treated as absent scope: %v", err)
	}
}
