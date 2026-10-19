package hostpoller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#906 row 4: validate refuses one worktree token listed for two workers
// (TestConfigRefusesAWorktreeListedForTwoWorkers), and worktree.create refuses
// another worker's token (TestWorktreeCreateRefusesAnotherWorkersToken). What
// is left is two different tokens naming one directory through a symlink,
// whether both are configured or one was created by this poller.

func aliasRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repo", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "repo", "a"), filepath.Join(root, "repo", "b")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.Symlink("a", filepath.Join(root, "repo", "c")); err != nil {
		t.Fatal(err)
	}
	return root
}

func aliasConfig(root string, workers map[string][]string) *Config {
	c := &Config{WorktreesRoot: root, Workers: map[string]WorkerConfig{}}
	for ref, trees := range workers {
		c.Workers[ref] = WorkerConfig{Family: "claude", Launch: []string{"x"}, Worktrees: trees}
	}
	return c
}

func emptyState() *State { return &State{Created: map[string][]string{}} }

func TestWorktreeAliasesRefuseTwoWorkersOnOneDirectory(t *testing.T) {
	root := aliasRoot(t)
	for name, other := range map[string]string{"absolute symlink": "repo/b", "relative symlink": "repo/c"} {
		t.Run(name, func(t *testing.T) {
			c := aliasConfig(root, map[string][]string{"iris": {"repo/a"}, "dara": {other}})
			err := c.CheckWorktreeAliases(emptyState())
			if err == nil {
				t.Fatal("two workers on one directory accepted")
			}
			for _, want := range []string{"repo/a", other, "iris", "dara", "one directory"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal should name %q: %v", want, err)
				}
			}
		})
	}
}

func TestWorktreeAliasesIncludeTheStateFile(t *testing.T) {
	root := aliasRoot(t)
	c := aliasConfig(root, map[string][]string{"iris": {"repo/a"}, "dara": nil})
	s := emptyState()
	s.Created["dara"] = []string{"repo/b"}
	if err := c.CheckWorktreeAliases(s); err == nil || !strings.Contains(err.Error(), "repo/b") {
		t.Fatalf("a created worktree aliasing another worker's: %v", err)
	}
}

func TestWorktreeAliasesAllowedWithinOneWorkerAndForMissingTrees(t *testing.T) {
	root := aliasRoot(t)
	c := aliasConfig(root, map[string][]string{"iris": {"repo/a", "repo/b"}, "dara": {"repo/missing"}, "gil": {"repo/also-missing"}})
	if err := c.CheckWorktreeAliases(emptyState()); err != nil {
		t.Fatalf("one worker's own aliases, or trees not yet created, must pass: %v", err)
	}
}
