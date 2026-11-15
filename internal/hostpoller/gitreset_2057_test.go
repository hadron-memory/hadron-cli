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

// hadron-cli#2057 C2b: the workspace reset, on real Git repositories.

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
		"-c", "protocol.file.allow=always"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// resetRepo builds a bare "origin" with one commit on main and a clone of it
// (origin/HEAD set), the shape of a real worktree.
func resetRepo(t *testing.T) (origin, clone string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	origin = filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	clone = filepath.Join(root, "clone")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gitIn(t, root, "init", "-q", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", ".")
	gitIn(t, seed, "commit", "-q", "-m", "one")
	gitIn(t, seed, "remote", "add", "origin", origin)
	gitIn(t, seed, "push", "-q", "origin", "main")
	gitIn(t, root, "clone", "-q", origin, clone)
	return origin, clone
}

// advanceOrigin pushes a new commit to origin/main from a scratch clone.
func advanceOrigin(t *testing.T, origin string) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", origin, other)
	if err := os.WriteFile(filepath.Join(other, "b.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, other, "add", ".")
	gitIn(t, other, "commit", "-q", "-m", "two")
	gitIn(t, other, "push", "-q", "origin", "main")
	return gitIn(t, other, "rev-parse", "HEAD")
}

var resetNow = time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)

func TestResetWorktreeMovesACleanPushedTreeToTheRemoteDefaultBranch(t *testing.T) {
	origin, clone := resetRepo(t)
	tip := advanceOrigin(t, origin) // the remote moved on since the clone
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || !out.Reset || out.Refused() {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if head := gitIn(t, clone, "rev-parse", "HEAD"); head != tip {
		t.Fatalf("HEAD %s, want the fetched remote tip %s", head, tip)
	}
	if br := gitIn(t, clone, "branch", "--show-current"); br != "" {
		t.Fatalf("the tree must be detached at the base, on branch %q", br)
	}
	if _, err := os.Stat(filepath.Join(clone, "b.txt")); err != nil {
		t.Fatalf("the new base's files must be checked out: %v", err)
	}
}

func TestResetWorktreeIsIdempotentFromADetachedBase(t *testing.T) {
	_, clone := resetRepo(t)
	for i := 0; i < 2; i++ {
		out, err := ResetWorktree(context.Background(), clone, resetNow)
		if err != nil || !out.Reset {
			t.Fatalf("run %d: out=%+v err=%v (a detached HEAD at a remote branch's commit is pushed)", i, out, err)
		}
	}
}

func TestResetWorktreeRefusesDirtyAndUntrackedWithoutMovingAnything(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, clone string){
		"modified tracked file": func(t *testing.T, c string) { _ = os.WriteFile(filepath.Join(c, "a.txt"), []byte("edited\n"), 0o644) },
		"untracked file":        func(t *testing.T, c string) { _ = os.WriteFile(filepath.Join(c, "new.txt"), []byte("x\n"), 0o644) },
	} {
		origin, clone := resetRepo(t)
		advanceOrigin(t, origin)
		before := gitIn(t, clone, "rev-parse", "HEAD")
		mutate(t, clone)
		out, err := ResetWorktree(context.Background(), clone, resetNow)
		if err != nil || out.Code != CodeWorktreeDirty || out.Reset {
			t.Errorf("%s: out=%+v err=%v", name, out, err)
		}
		if after := gitIn(t, clone, "rev-parse", "HEAD"); after != before {
			t.Errorf("%s: a refused reset moved HEAD", name)
		}
	}
}

func TestResetWorktreeRefusesUnpushedCommitsAndKeepsThemAsRescueRefs(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "checkout", "-q", "-b", "work")
	_ = os.WriteFile(filepath.Join(clone, "w.txt"), []byte("w\n"), 0o644)
	gitIn(t, clone, "add", ".")
	gitIn(t, clone, "commit", "-q", "-m", "unpushed work")
	work := gitIn(t, clone, "rev-parse", "HEAD")
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Code != CodeWorktreeUnpushed || out.Reset {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if head := gitIn(t, clone, "rev-parse", "HEAD"); head != work {
		t.Fatal("a refused reset must leave HEAD where it was")
	}
	ref := "refs/hadron/rescue/20261115T140000Z/work"
	if got := gitIn(t, clone, "rev-parse", ref); got != work {
		t.Fatalf("rescue ref %s = %s, want the unpushed commit %s", ref, got, work)
	}
	if len(out.Rescued) != 1 || !strings.Contains(out.Rescued[0], ref) {
		t.Fatalf("rescued = %v", out.Rescued)
	}
}

// Unpushed work on ANOTHER branch survives the reset as a rescue ref, and the
// branch itself is left alone.
func TestResetWorktreeKeepsAnotherBranchsUnpushedCommitsReachable(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "checkout", "-q", "-b", "side")
	_ = os.WriteFile(filepath.Join(clone, "s.txt"), []byte("s\n"), 0o644)
	gitIn(t, clone, "add", ".")
	gitIn(t, clone, "commit", "-q", "-m", "side work")
	side := gitIn(t, clone, "rev-parse", "HEAD")
	gitIn(t, clone, "checkout", "-q", "main") // HEAD is on a pushed branch
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || !out.Reset {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if got := gitIn(t, clone, "rev-parse", "refs/hadron/rescue/20261115T140000Z/side"); got != side {
		t.Fatalf("rescue ref for the side branch = %s, want %s", got, side)
	}
	if got := gitIn(t, clone, "rev-parse", "side"); got != side {
		t.Fatal("the reset must never move or delete a branch")
	}
}

func TestResetWorktreeLeavesAWorkerWithoutGitAlone(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "notes.md"), []byte("n\n"), 0o644)
	out, err := ResetWorktree(context.Background(), dir, resetNow)
	if err != nil || out.Reset || out.Refused() {
		t.Fatalf("out=%+v err=%v: a plain directory has no tree to reset and nothing may be refused", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.md")); err != nil {
		t.Fatal("a plain directory must be untouched")
	}
}

func TestResetWorktreeRefusesATreeWithNoOriginAndAFailedFetch(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "fetch") {
		t.Fatalf("a failed fetch must refuse, not guess: out=%+v err=%v", out, err)
	}
	gitIn(t, clone, "remote", "remove", "origin")
	out, err = ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "origin") {
		t.Fatalf("no origin: out=%+v err=%v", out, err)
	}
}
