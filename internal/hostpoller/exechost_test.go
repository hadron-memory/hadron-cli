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

// Occupied finds a real process working inside a tree (Linux /proc), and
// Alive tracks it.
func TestExecHostSeesAProcessWorkingInATree(t *testing.T) {
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "deep")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	h := ExecHost{}
	if who, err := h.Occupied(context.Background(), dir); err != nil || who != "" {
		t.Fatalf("empty tree: %q %v", who, err)
	}
	a, err := h.Launch(context.Background(), LaunchSpec{Argv: []string{sleep, "30"}, Dir: sub, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscallKill(a.PID()) }()
	deadline := time.Now().Add(5 * time.Second)
	var who string
	for time.Now().Before(deadline) {
		if who, _ = h.Occupied(context.Background(), dir); who != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(who, "sleep") {
		t.Errorf("a process in a subdirectory occupies the tree: %q", who)
	}
	if !strings.Contains(who, "sleep 30") {
		t.Errorf("the occupant is named by its argv (#1474): %q", who)
	}
	if !h.Alive(a.PID()) {
		t.Error("the launched process is alive")
	}
}

// Terminate stops the whole process group a launch created: the launched
// process AND the child it runs (#1474 — a wrapper relaunching its agent
// must not survive its start's failure). A bare kill of the leader would
// leave the child working in the tree; the group signal must not.
func TestExecHostTerminateKillsTheLaunchedGroup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "child-running")
	h := ExecHost{TerminateGrace: 2 * time.Second}
	// sleep as a child the leader waits on: killing only the leader would
	// orphan it in the tree.
	a, err := h.Launch(context.Background(), LaunchSpec{Argv: []string{"sh", "-c", `sleep 30 & touch "$0"; wait`, ready}, Dir: dir, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Terminate() }()
	// Gate on the child existing: a TERM that outruns the fork kills the
	// leader before there is a group to leave behind, which would prove
	// nothing.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if detail := a.Terminate(); !strings.Contains(detail, "terminated the launched process group") {
		t.Errorf("a live group is stopped: %q", detail)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the launched process must exit after Terminate")
	}
	if h.Alive(a.PID()) {
		t.Error("the group leader died with its group")
	}
	// The child went with it: nothing is left working in the tree.
	sweepDeadline := time.Now().Add(5 * time.Second)
	for {
		who, _ := h.Occupied(context.Background(), dir)
		if who == "" {
			break
		}
		if time.Now().After(sweepDeadline) {
			t.Fatalf("the group's child survived the leader: %q", who)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The incident's exact shape: a wrapper that ignores SIGTERM and keeps
// relaunching a child is killed by the escalation after the grace.
func TestExecHostTerminateEscalatesOnAWrapperThatKeepsRelaunching(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "armed")
	h := ExecHost{TerminateGrace: 300 * time.Millisecond}
	a, err := h.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sh", "-c", `trap '' TERM; touch "$0"; while :; do sleep 5 & wait $!; done`, ready},
		Dir:  dir, Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Terminate() }()
	// Wait for the trap to be armed: a TERM that lands first kills the shell
	// before it ever ignores one, which is not the shape under test.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the wrapper never armed its trap")
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	if detail := a.Terminate(); !strings.Contains(detail, "killed the launched process group") {
		t.Errorf("a TERM-ignoring wrapper is killed after the grace: %q", detail)
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Errorf("the escalation must wait the grace first: %s", elapsed)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the wrapper must be dead after the escalation")
	}
}

// An agent that already exited is signalled nothing — its pid may have been
// reused — and Terminate says so.
func TestExecHostTerminateAfterExitSignalsNothing(t *testing.T) {
	h := ExecHost{}
	a, err := h.Launch(context.Background(), LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	if detail := a.Terminate(); detail != "" {
		t.Errorf("an exited agent is signalled nothing: %q", detail)
	}
}

// A remove whose success report was lost is re-delivered after the tree is
// gone, and succeeds again: on the real host, through the poller (Anne's
// delta P2 on #882).
func TestRemoveIsIdempotentOnTheRealHost(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	repo := filepath.Join(base, "repo")
	run(t, base, "init", "-q", "--bare", "-b", "main", origin)
	run(t, base, "clone", "-q", origin, repo)
	run(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, repo, "push", "-q", "origin", "HEAD:main")
	root := filepath.Join(base, "trees")
	cfg := &Config{App: "a", Label: "l", PollIntervalSeconds: 30, WorktreesRoot: root,
		StateFile: filepath.Join(base, "state.json"), Repos: map[string]string{"acme/repo": repo},
		Workers: map[string]WorkerConfig{"iris": {Family: "claude", Launch: []string{"x"}}}}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	srv := &fakeServer{notDone: map[string]int{}, endedOnce: map[string]bool{}}
	p := New(cfg, srv, ExecHost{}, st, "hk", nil)
	create := Operation{ID: "c1", Type: OpWorktreeCreate, WorkerID: "id-iris",
		Args: map[string]any{"repo": "acme/repo", "baseBranch": "main", "branchName": "feat/x", "label": "x"}}
	rm := Operation{ID: "r1", Type: OpWorktreeRemove, WorkerID: "id-iris", Args: map[string]any{"worktree": "repo/x"}}
	srv.batches = [][]Operation{{create}}
	step(t, p)
	run(t, cfg.WorktreePath("repo/x"), "push", "-q", "-u", "origin", "feat/x")
	srv.batches = [][]Operation{{rm}, {rm}} // the second: re-delivered after a lost report
	step(t, p)
	step(t, p)
	rs := srv.finalReports()
	if len(rs) != 3 || rs[1].outcome != Succeeded || rs[2].outcome != Succeeded {
		t.Fatalf("create, remove, re-delivered remove all succeed: %+v", rs)
	}
}
