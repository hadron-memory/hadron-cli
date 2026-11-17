package team

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// hadron-cli#2068: the host's stop (delivery.LockBinding) and the binding
// writer (`session start`, through withBindingLockAt) exclude each other on
// ONE lock file. A writer arriving while the stopper holds the lock waits for
// its release, and then writes.
func gitDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBindingWriterWaitsForTheHostsStopLock(t *testing.T) {
	dir := gitDir(t)
	release, err := delivery.LockBinding(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	wrote := make(chan error, 1)
	go func() {
		wrote <- withBindingLockAt(bindingLoc{dir: filepath.Join(dir, ".git")}, func(cacheHandle) error { return nil })
	}()
	select {
	case err := <-wrote:
		t.Fatalf("the binding writer got in while the stopper held the lock (err=%v): they do not share a lock file", err)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	released = true
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatalf("the writer failed after the stopper released: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the writer never got the lock after the stopper released it")
	}
}

// And the other way: a writer holding the lock makes the stopper decline
// within its bound rather than signal.
func TestHostsStopLockIsRefusedWhileTheWriterHoldsIt(t *testing.T) {
	dir := gitDir(t)
	inside, leave := make(chan struct{}), make(chan struct{})
	var once sync.Once
	leaveWriter := func() { once.Do(func() { close(leave) }) }
	t.Cleanup(leaveWriter)
	done := make(chan error, 1)
	go func() {
		done <- withBindingLockAt(bindingLoc{dir: filepath.Join(dir, ".git")}, func(cacheHandle) error {
			close(inside)
			<-leave
			return nil
		})
	}()
	select {
	case <-inside:
	case err := <-done:
		t.Fatalf("the writer never took its lock: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("the writer never took its lock")
	}
	if _, err := delivery.LockBinding(dir, 100*time.Millisecond); err == nil {
		t.Fatal("the stopper took the lock a binding write held")
	}
	leaveWriter()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the writer: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the writer never finished")
	}
}
