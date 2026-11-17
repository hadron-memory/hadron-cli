//go:build !windows

package team

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cli#1969 F3 (Gloria's BLIND F3 on #1968): a deleted working directory is
// reported as such, not as "not inside a git worktree".
func TestPlainDir1969DeletedWorkingDirectoryIsNamed(t *testing.T) {
	fakeGit(t, "fatal: not a git repository (or any of the parent directories): .git")
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove the working directory here: %v", err)
	}
	for _, writable := range []bool{false, true} {
		_, err := locateBinding(context.Background(), writable)
		if err == nil || !strings.Contains(err.Error(), "working directory") {
			t.Fatalf("writable=%v: a deleted cwd must be named as such, not read as a missing worktree: %v", writable, err)
		}
		if errors.Is(err, errNoWorktree) {
			t.Errorf("writable=%v: a deleted cwd is not 'no worktree' (readers fall back on that): %v", writable, err)
		}
	}
}

// cli#1969 F8 (Gloria's BLIND F8): a bind tightens a pre-existing cache
// directory, so "owner-only" holds beyond first creation.
func TestPlainDir1969BindTightensAnExistingCacheDirectory(t *testing.T) {
	dir := plainDir1391(t)
	cache := filepath.Join(dir, plainDirCacheName)
	if err := os.Mkdir(cache, 0o755); err != nil { // #nosec G301 — the loose directory under test
		t.Fatal(err)
	}
	if err := os.Chmod(cache, 0o755); err != nil { // #nosec G302 — umask-proof
		t.Fatal(err)
	}
	if _, err := writeBinding(context.Background(), &binding{SessionID: "s1", WorkerID: "wkr1", WorkerName: "Iris"}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(cache); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("the cache directory must be owner-only after a bind: %v %v", st, err)
	}
}

// QA's F3 finding on #2229: the pin above is answered by an injected "not a
// git repository", which real git does not say for a deleted working
// directory ("Unable to read current working directory"). With the REAL git,
// the deleted cwd must still be named.
func TestPlainDir1969DeletedWorkingDirectoryIsNamedUnderRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git binary")
	}
	t.Setenv(GitDirEnv, "")
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove the working directory here: %v", err)
	}
	for _, writable := range []bool{false, true} {
		_, err := locateBinding(context.Background(), writable)
		if err == nil || !strings.Contains(err.Error(), "working directory") || errors.Is(err, errNoWorktree) {
			t.Fatalf("writable=%v: a deleted cwd must be named, not read as a missing worktree: %v", writable, err)
		}
	}
}

// Jibril's BLIND P2 on #2229: a bind must not chmod through a symlinked
// cache directory (it would change a shared target's mode). It refuses, and
// the target is left as it was.
func TestPlainDir1969BindRefusesASymlinkedCacheDirectory(t *testing.T) {
	dir := plainDir1391(t)
	shared := t.TempDir()
	if err := os.Chmod(shared, 0o755); err != nil { // #nosec G302 — the shared directory under test
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(dir, plainDirCacheName)); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	_, err := writeBinding(context.Background(), &binding{SessionID: "s1", WorkerID: "wkr1", WorkerName: "Iris"})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("a symlinked cache must be refused by name: %v", err)
	}
	if st, serr := os.Stat(shared); serr != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("the target's mode must be unchanged: %v %v", st, serr)
	}
	if ents, _ := os.ReadDir(shared); len(ents) != 0 {
		t.Fatalf("nothing may be written through the link: %v", ents)
	}
}
