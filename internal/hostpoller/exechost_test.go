package hostpoller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// The real git path, end to end on a scratch repo: create, the states remove
// decides on, and remove.
func TestExecHostWorktreeLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	repo := filepath.Join(base, "repo")
	run(t, base, "init", "-q", "--bare", "-b", "main", origin)
	run(t, base, "clone", "-q", origin, repo)
	run(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, repo, "push", "-q", "origin", "HEAD:main")

	h := ExecHost{}
	wt := filepath.Join(base, "trees", "repo", "x")
	if err := h.CreateWorktree(ctx, repo, wt, "feat/x", "main"); err != nil {
		t.Fatal(err)
	}
	st, err := h.Inspect(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || !st.IsWorktree || st.Branch != "feat/x" || st.Dirty || st.Upstream != "" {
		t.Fatalf("fresh worktree: %+v", st)
	}
	// Pushed and tracking: nothing to lose.
	run(t, wt, "push", "-q", "-u", "origin", "feat/x")
	if st, _ = h.Inspect(ctx, wt); st.Upstream != "origin/feat/x" || st.Ahead != 0 {
		t.Fatalf("pushed: %+v", st)
	}
	run(t, wt, "commit", "-q", "--allow-empty", "-m", "local")
	if st, _ = h.Inspect(ctx, wt); st.Ahead != 1 {
		t.Fatalf("one unpushed commit: %+v", st)
	}
	run(t, wt, "push", "-q")
	if err := os.WriteFile(filepath.Join(wt, "scratch"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st, _ = h.Inspect(ctx, wt); !st.Dirty {
		t.Fatalf("untracked file is dirty: %+v", st)
	}
	if err := os.Remove(filepath.Join(wt, "scratch")); err != nil {
		t.Fatal(err)
	}
	if err := h.RemoveWorktree(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if st, _ = h.Inspect(ctx, wt); st.Exists {
		t.Fatal("removed")
	}
	// A plain directory is not a worktree.
	plain := filepath.Join(base, "plain")
	if err := os.Mkdir(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	if st, _ = h.Inspect(ctx, plain); !st.Exists || st.IsWorktree {
		t.Fatalf("plain dir: %+v", st)
	}
}

// A launched agent gets exactly the environment it is given, and its exit is
// observed.
func TestExecHostLaunchUsesTheGivenEnvironment(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "env")
	a, err := ExecHost{}.Launch(context.Background(), LaunchSpec{
		Argv: []string{sh, "-c", "env > " + out},
		Dir:  dir,
		Env:  []string{"HADRON_LAUNCH_GRANT=hlg_x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not exit")
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"HADRON_LAUNCH_GRANT=hlg_x"} {
		if !strings.Contains(string(raw), line) {
			t.Errorf("env missing %q:\n%s", line, raw)
		}
	}
	if strings.Contains(string(raw), "HADRON_TOKEN=") {
		t.Errorf("only the given environment reaches the agent:\n%s", raw)
	}
}
