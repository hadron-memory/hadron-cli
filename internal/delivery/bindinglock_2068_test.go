package delivery

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func gitTree(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.Mkdir(filepath.Join(d, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// hadron-cli#2068: the stopper and `session start` exclude each other on the
// binding's lock file.
func TestLockBindingExcludesAWriterAndIsBounded(t *testing.T) {
	d := gitTree(t)
	release, err := LockBinding(d, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if _, err := LockBinding(d, 80*time.Millisecond); !errors.Is(err, ErrBindingLockBusy) {
		t.Fatalf("a second taker must be refused within the bound, got %v", err)
	}
	if took := time.Since(began); took > time.Second {
		t.Fatalf("the bound was not honoured: %s", took)
	}
	release()
	second, err := LockBinding(d, time.Second)
	if err != nil {
		t.Fatalf("a released lock is takeable: %v", err)
	}
	second()
}

// The lock file is the one the binding writer holds (internal/cmd/team).
func TestLockBindingUsesTheWritersLockFile(t *testing.T) {
	d := gitTree(t)
	release, err := LockBinding(d, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := os.Stat(filepath.Join(d, ".git", "hadron-team-session.lock")); err != nil {
		t.Fatalf("the lock must sit beside the binding: %v", err)
	}
}

func TestLockBindingWithNoBindingLocationHoldsNothing(t *testing.T) {
	release, err := LockBinding(filepath.Join(t.TempDir(), "gone"), time.Second)
	if err != nil || release == nil {
		t.Fatalf("%v", err)
	}
	release()
}

// Blind P1: a plain directory with no cache yet still gets a real lock, so a
// bind that creates the cache during the stop waits on it.
func TestLockBindingCreatesAPlainDirectoryCacheAndLocksIt(t *testing.T) {
	d := t.TempDir()
	release, err := LockBinding(d, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	info, err := os.Stat(filepath.Join(d, plainCacheName))
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("the cache must exist, owner-only: %v %v", info, err)
	}
	if _, err := LockBinding(d, 50*time.Millisecond); !errors.Is(err, ErrBindingLockBusy) {
		t.Fatalf("a bind racing the stop must find the lock held: %v", err)
	}
}

func TestLockBindingInAPlainDirectoryCacheLocksThere(t *testing.T) {
	d := t.TempDir()
	if err := os.Mkdir(filepath.Join(d, plainCacheName), 0o700); err != nil {
		t.Fatal(err)
	}
	release, err := LockBinding(d, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := LockBinding(d, 50*time.Millisecond); !errors.Is(err, ErrBindingLockBusy) {
		t.Fatalf("%v", err)
	}
}

// §10 F1: the poller creates and locks inside a tree the worker writes, so a
// link planted there must be refused, never followed.
func TestLockBindingRefusesALinkedCacheDirectory(t *testing.T) {
	outside := t.TempDir()
	d := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(d, plainCacheName)); err != nil {
		t.Skip("no symlinks")
	}
	if _, err := LockBinding(d, time.Second); err == nil {
		t.Fatal("a symlinked cache directory was followed")
	}
	if _, err := os.Stat(filepath.Join(outside, bindingLockFileName)); err == nil {
		t.Fatal("a lock file was created through the link")
	}
}

func TestLockBindingRefusesALinkedLockFile(t *testing.T) {
	d := gitTree(t)
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.Symlink(target, filepath.Join(d, ".git", bindingLockFileName)); err != nil {
		t.Skip("no symlinks")
	}
	if _, err := LockBinding(d, time.Second); err == nil {
		t.Fatal("a symlinked lock file was opened")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("a file was created at the link's target")
	}
}

func TestLockBindingRefusesALinkedGitDirectory(t *testing.T) {
	d := t.TempDir()
	real := t.TempDir()
	if err := os.Symlink(real, filepath.Join(d, ".git")); err != nil {
		t.Skip("no symlinks")
	}
	if _, err := LockBinding(d, time.Second); err == nil {
		t.Fatal("a symlinked .git directory was followed")
	}
	if _, err := os.Stat(filepath.Join(real, bindingLockFileName)); err == nil {
		t.Fatal("a lock file was created through the link")
	}
}

// Blind delta P2: a directory swapped for a link AFTER the check and before the
// open is not followed: the file is never created where it was not validated.
func TestLockBindingRefusesADirectorySwappedForALinkBeforeTheOpen(t *testing.T) {
	d := gitTree(t)
	outside := t.TempDir()
	lockOpenHook = func(dir string) {
		if err := os.Rename(dir, dir+".moved"); err != nil {
			t.Error(err)
			return
		}
		if err := os.Symlink(outside, dir); err != nil {
			t.Error(err)
		}
	}
	defer func() { lockOpenHook = nil }()
	if _, err := LockBinding(d, time.Second); err == nil {
		t.Fatal("a directory swapped for a link was followed")
	}
	if _, err := os.Stat(filepath.Join(outside, bindingLockFileName)); err == nil {
		t.Fatal("the lock was created through the swapped link")
	}
}

// #2079: the Windows refusal rule, pinned on every platform. A handle whose
// attributes carry the reparse-point bit is refused; plain files and
// directories are not.
func TestRefusesAttributesRefusesAReparsePoint(t *testing.T) {
	const (
		attrDirectory = 0x10
		attrArchive   = 0x20
		attrReparse   = 0x400
	)
	for name, tc := range map[string]struct {
		attrs   uint32
		refused bool
	}{
		"regular file":            {attrArchive, false},
		"directory":               {attrDirectory, false},
		"symlink or junction":     {attrReparse, true},
		"directory link":          {attrDirectory | attrReparse, true},
		"file with a reparse tag": {attrArchive | attrReparse, true},
	} {
		if err := refusesAttributes(tc.attrs); (err != nil) != tc.refused {
			t.Errorf("%s: refused=%v, want %v", name, err != nil, tc.refused)
		}
	}
}
