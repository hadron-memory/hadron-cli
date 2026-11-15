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
