package hostpoller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hadron-cli#2119 (Chip's repro at main de8f5d8f): a worker directory with its OWN
// .git entry that git cannot use as a repository. An empty .git DIRECTORY is
// skipped by git's repository discovery: standalone, git says "not a git
// repository" (the reset took the no-Git branch and the agent launched into an
// un-reset tree); inside an enclosing repository, git resolves the OUTER one and
// the reset fetched and moved it. The rule: with its own .git entry the directory
// must resolve to a valid repository whose top level is the directory itself, else
// refuse; there is no "worker without Git" for a .git that is there.

func brokenStandalone(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "worker")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// brokenNested is Chip's layout B: a clean outer clone whose origin has advanced,
// with worker/.git an empty directory, worker/ excluded so the outer stays clean.
func brokenNested(t *testing.T) (outer, worker, outerHead string) {
	t.Helper()
	origin, clone := resetRepo(t)
	advanceOrigin(t, origin)
	worker = filepath.Join(clone, "worker")
	if err := os.MkdirAll(filepath.Join(worker, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	ex := filepath.Join(clone, ".git", "info", "exclude")
	_ = os.MkdirAll(filepath.Dir(ex), 0o755)
	if err := os.WriteFile(ex, []byte("worker/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return clone, worker, gitIn(t, clone, "rev-parse", "HEAD")
}

func TestResetRefusesAStandaloneBrokenGitDirectory2119(t *testing.T) {
	dir := brokenStandalone(t)
	out, err := ResetWorktree(context.Background(), dir, resetNow)
	if err != nil || out.Reset || out.Code != CodeResetFailed {
		t.Fatalf("a broken .git must refuse, not be a worker without Git: out=%+v err=%v", out, err)
	}
	if !strings.Contains(out.Detail, ".git entry") || !strings.Contains(out.Detail, "not a usable repository (") {
		t.Fatalf("the refusal must say the .git entry is not a usable repository: %q", out.Detail)
	}
}

func TestResetRefusesABrokenGitDirectoryInsideAnEnclosingRepository2119(t *testing.T) {
	outer, worker, head := brokenNested(t)
	out, err := ResetWorktree(context.Background(), worker, resetNow)
	if err != nil || out.Reset || out.Code != CodeResetFailed {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if !strings.Contains(out.Detail, "enclosing one") {
		t.Fatalf("the refusal must say git fell back to an enclosing repository: %q", out.Detail)
	}
	if got := gitIn(t, outer, "rev-parse", "HEAD"); got != head {
		t.Fatalf("the ENCLOSING repository moved: %s -> %s", head, got)
	}
	if got := gitIn(t, outer, "branch", "--show-current"); got != "main" {
		t.Fatalf("the enclosing repository left its branch: %q", got)
	}
	if refs := gitIn(t, outer, "for-each-ref", "refs/remotes/origin"); strings.Contains(refs, "refs/remotes/origin/main") && gitIn(t, outer, "rev-parse", "refs/remotes/origin/main") != head {
		t.Fatal("the enclosing repository was fetched")
	}
}

func TestInspectRefusesABrokenGitDirectory2119(t *testing.T) {
	_, worker, _ := brokenNested(t)
	for name, dir := range map[string]string{"standalone": brokenStandalone(t), "nested": worker} {
		st, err := (ExecHost{}).Inspect(context.Background(), dir)
		var broken *BrokenRepoError
		if !errors.As(err, &broken) || st.IsWorktree {
			t.Errorf("%s: Inspect must return the refusal, not 'not a worktree' with no error: st=%+v err=%v", name, st, err)
		}
	}
}

// What must keep working: a plain child with no .git of its own, a valid root, a
// linked worktree and the invalid-gitfile control (which already refused).
func TestBrokenGitRuleLeavesTheHealthyLayoutsAlone2119(t *testing.T) {
	origin, clone := resetRepo(t)
	advanceOrigin(t, origin)
	child := filepath.Join(clone, "plain-child")
	_ = os.MkdirAll(child, 0o755)
	if out, err := ResetWorktree(context.Background(), child, resetNow); err != nil || out.Reset || out.Refused() {
		t.Fatalf("a plain child is a worker without Git: out=%+v err=%v", out, err)
	}
	if st, err := (ExecHost{}).Inspect(context.Background(), child); err != nil || st.IsWorktree {
		t.Fatalf("plain child Inspect: st=%+v err=%v", st, err)
	}
	if out, err := ResetWorktree(context.Background(), clone, resetNow); err != nil || !out.Reset {
		t.Fatalf("a valid root must reset: out=%+v err=%v", out, err)
	}
	gitFile := filepath.Join(t.TempDir(), "w")
	_ = os.MkdirAll(gitFile, 0o755)
	_ = os.WriteFile(filepath.Join(gitFile, ".git"), []byte("not a valid gitdir file\n"), 0o644)
	if out, err := ResetWorktree(context.Background(), gitFile, resetNow); err != nil || out.Reset || out.Code != CodeResetFailed {
		t.Fatalf("an invalid gitfile must keep refusing: out=%+v err=%v", out, err)
	}
}
