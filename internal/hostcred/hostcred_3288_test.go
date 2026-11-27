package hostcred

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// hadron-cli#3288 F11: a lock held elsewhere is waited for a bounded time, then refused with the lock file's path.
func TestLock3288WaitIsBounded(t *testing.T) {
	dir := tempConfig(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := lockWait
	lockWait = 100 * time.Millisecond
	t.Cleanup(func() { lockWait = old })
	other := flock.New(filepath.Join(dir, LockName))
	if ok, err := other.TryLock(); !ok || err != nil {
		t.Fatalf("hold the lock: %v %v", ok, err)
	}
	defer func() { _ = other.Unlock() }()
	done := make(chan error, 1)
	go func() {
		s, err := Lock()
		if err == nil {
			s.Unlock()
		}
		done <- err
	}()
	select {
	case err := <-done:
		var se *Error
		if !errors.As(err, &se) || se.Path != filepath.Join(dir, LockName) || !strings.Contains(err.Error(), LockName) {
			t.Fatalf("a held lock is refused naming the lock file: %v", err)
		}
		if !strings.Contains(err.Error(), "another hadron") {
			t.Errorf("the refusal says another process holds it: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Lock waited past its bound for a held lock")
	}
	_ = other.Unlock()
	s, err := Lock()
	if err != nil {
		t.Fatalf("a free lock is taken: %v", err)
	}
	s.Unlock()
}

// hadron-cli#3288 F13: the lock file is opened without following a symlink, as the store file is read.
func TestLock3288RefusesASymlinkedLockFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows; the Windows open refuses a reparse point instead")
	}
	dir := tempConfig(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere.lock")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, LockName)); err != nil {
		t.Fatal(err)
	}
	s, err := Lock()
	if err == nil {
		s.Unlock()
		t.Fatal("a symlinked lock file is refused")
	}
	if !strings.Contains(err.Error(), "symlink") || !strings.Contains(err.Error(), LockName) {
		t.Errorf("the refusal names the lock file and the symlink: %v", err)
	}
}
