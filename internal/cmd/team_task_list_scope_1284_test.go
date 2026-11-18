package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// isolateFromGit1289 makes dir a place git discovery cannot escape from: it is
// the working directory, the git dir override is cleared, every inherited GIT_*
// variable is unset (GIT_DIR, GIT_WORK_TREE and friends would otherwise make git
// "find" a repository from the environment, whatever the directory), and
// GIT_CEILING_DIRECTORIES stops the walk up through dir's ancestors, so a runner
// whose TempDir sits inside a repository still sees "not a git repository"
// (cli#1289). Restores the environment when the test ends.
func isolateFromGit1289(t *testing.T, dir string) {
	t.Helper()
	t.Setenv(team.GitDirEnv, "")
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") && name != team.GitDirEnv {
			t.Setenv(name, value) // registers the restore of the original value
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
}

// noWorktree1289 is a fresh directory with no git worktree reachable from it.
func noWorktree1289(t *testing.T) {
	t.Helper()
	isolateFromGit1289(t, t.TempDir())
}

// The fixture's isolation is itself pinned: a repository ABOVE the directory, and
// a GIT_DIR in the environment, are exactly what an unisolated fixture would
// inherit. The control proves the hazard is real; the isolated directory must
// then see no repository.
func TestNoWorktreeFixtureIsIsolatedFromGitDiscovery1289(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	outer := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", outer).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	inner := filepath.Join(outer, "sub")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	gitDirHere := func() error {
		cmd := exec.Command("git", "rev-parse", "--git-dir")
		cmd.Dir = inner
		return cmd.Run()
	}
	// Control: with nothing isolated, git discovers the ancestor repository.
	t.Run("control: the hazard is real", func(t *testing.T) {
		for _, kv := range os.Environ() {
			if strings.HasPrefix(kv, "GIT_DIR=") || strings.HasPrefix(kv, "GIT_CEILING_DIRECTORIES=") {
				t.Skip("the runner already sets GIT_DIR or GIT_CEILING_DIRECTORIES; the control cannot show the hazard")
			}
		}
		if err := gitDirHere(); err != nil {
			t.Fatalf("a directory inside a repository must see it: %v", err)
		}
	})
	t.Run("an ancestor repository is not discovered", func(t *testing.T) {
		isolateFromGit1289(t, inner)
		if err := gitDirHere(); err == nil {
			t.Fatal("the isolated directory still discovers its ancestor repository")
		}
	})
	t.Run("an inherited GIT_DIR is not honoured", func(t *testing.T) {
		t.Setenv("GIT_DIR", filepath.Join(outer, ".git"))
		isolateFromGit1289(t, t.TempDir())
		if got := os.Getenv("GIT_DIR"); got != "" {
			t.Fatalf("GIT_DIR must be unset in the isolated fixture, got %q", got)
		}
	})
}

// Listing tasks needs an App, not a worker session or a git worktree. Both
// ordinary and coordinator views should explain the missing scope before
// making any request (#1284).
func TestTeamTaskListOutsideWorktreeNamesAppRemedy1284(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []string
	}{
		{"ordinary view", nil},
		{"coordinator view (--team)", []string{"--team=worker:Iris"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noWorktree1289(t)
			srv, calls := attnServer(t, taskResponses())
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"--server", srv.URL, "team", "task", "list"}, tc.extra...))
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
	noWorktree1289(t)
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

// The coordinator view's positive control: outside any worktree, `--team` with an
// explicit --app lists, read-only (no session header), against that App.
func TestTeamTaskListTeamViewOutsideWorktreeWithAppStillLists1289(t *testing.T) {
	noWorktree1289(t)
	r := taskResponses()
	r["TeamReportingLines"] = r["TeamRoster"]
	srv, calls := attnServer(t, r)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "--app", "capp100000000000000000000", "team", "task", "list", "--team=worker:Iris", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--team with an explicit --app must list outside a worktree: %v", err)
	}
	c := taskListCall(t, calls)
	if c.Vars["appRef"] != "capp100000000000000000000" || c.Session != "" {
		t.Fatalf("the team view lost its App scope or sent a session: %+v", c)
	}
	if out.Len() == 0 {
		t.Fatal("the team view listed nothing")
	}
}

// A corrupt binding is a real error in either view, not an absent scope: the
// refusal names the binding file and the parse error, and no request is made. An
// unrelated failure (a bad flag, a missing App) cannot satisfy this.
func TestTeamTaskListStillRefusesCorruptBinding1284(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []string
	}{
		{"ordinary view", nil},
		{"coordinator view (--team)", []string{"--team=worker:Iris"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(team.GitDirEnv, dir)
			if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(`{"sessionId":`), 0o600); err != nil {
				t.Fatal(err)
			}
			srv, calls := attnServer(t, taskResponses())
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"--server", srv.URL, "team", "task", "list"}, tc.extra...))
			err := root.Execute()
			if err == nil {
				t.Fatal("a corrupt binding must be refused")
			}
			if got := exitCodeFor(err); got != exitcode.Error {
				t.Fatalf("exit = %d, want %d (a generic failure, not a usage or App-remedy refusal): %v", got, exitcode.Error, err)
			}
			for _, want := range []string{"hadron-team-session.json", "is corrupt", "unexpected end of JSON input"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the refusal must name the binding and its parse error, lacks %q: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "no team App") {
				t.Fatalf("a corrupt binding must not be treated as an absent scope: %v", err)
			}
			if out.Len() != 0 || len(*calls) != 0 {
				t.Fatalf("a corrupt binding must not produce output or requests: %q, %+v", out.String(), *calls)
			}
		})
	}
}
