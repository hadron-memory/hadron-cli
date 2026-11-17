//go:build !windows

package team

import (
	"context"
	"errors"
	"os"
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
