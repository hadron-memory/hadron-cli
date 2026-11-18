//go:build unix

package delivery

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hadron-cli#2326: an EACCES opening the binding location is a permission
// problem with an owner-side repair, not a link. The old "(a link?)" tail sent
// the reader looking for a symlink that was never there.
func TestLockBindingEACCESOnTheLocationNamesPermissionsNotALink2326(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	d := t.TempDir()
	cache := filepath.Join(d, plainCacheName)
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cache, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cache, 0o700) })
	_, err := LockBinding(d, time.Second)
	if err == nil {
		t.Fatal("an unreadable binding location was locked")
	}
	msg := err.Error()
	if strings.Contains(msg, "a link?") {
		t.Errorf("a permission refusal still says to look for a link: %q", msg)
	}
	for _, want := range []string{"permission", "owner"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q (owner-side repair): %q", want, msg)
		}
	}
}

// A real link is still reported as one: the guidance is only for EACCES.
func TestLockBindingLinkRefusalStillSaysLink2326(t *testing.T) {
	d := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(d, plainCacheName)); err != nil {
		t.Skip("no symlinks")
	}
	_, err := LockBinding(d, time.Second)
	if err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("a linked location must still say link: %v", err)
	}
}

// A plain worktree whose .git is a link (here dangling) is neither plain nor a
// git tree: nothing is locked and nothing is created, never a cache made
// through or beside it. This pins today's Lstat/Stat behavior as the
// documented fail-closed answer; any move to following the link is its own
// gate (cli#2326).
func TestLockBindingSymlinkedGitInAPlainWorktreeFailsClosed2326(t *testing.T) {
	d := t.TempDir()
	if err := os.Symlink(filepath.Join(d, "nowhere"), filepath.Join(d, ".git")); err != nil {
		t.Skip("no symlinks")
	}
	release, err := LockBinding(d, time.Second)
	if err != nil {
		t.Fatalf("a dangling .git link has no binding location: %v", err)
	}
	release()
	if _, err := os.Lstat(filepath.Join(d, plainCacheName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a cache was created beside a .git link: %v", err)
	}
}
