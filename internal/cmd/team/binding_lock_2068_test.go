package team

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// hadron-cli#2068: the host's stop (delivery.LockBinding) and the binding
// writer (`session start`, through withBindingLockAt) exclude each other on
// ONE lock file. A writer arriving while the stopper holds the lock waits for
// its release, and then writes.
func TestBindingWriterWaitsForTheHostsStopLock(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	release, err := delivery.LockBinding(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	wrote := make(chan struct{})
	go func() {
		_ = withBindingLockAt(bindingLoc{dir: filepath.Join(dir, ".git")}, func() error { return nil })
		close(wrote)
	}()
	select {
	case <-wrote:
		t.Fatal("the binding writer got in while the stopper held the lock: they do not share a lock file")
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case <-wrote:
	case <-time.After(3 * time.Second):
		t.Fatal("the writer never got the lock after the stopper released it")
	}
}

// And the other way: a writer holding the lock makes the stopper decline
// within its bound rather than signal.
func TestHostsStopLockIsRefusedWhileTheWriterHoldsIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	inside, leave := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = withBindingLockAt(bindingLoc{dir: filepath.Join(dir, ".git")}, func() error { close(inside); <-leave; return nil })
		close(done)
	}()
	<-inside
	if _, err := delivery.LockBinding(dir, 100*time.Millisecond); err == nil {
		t.Fatal("the stopper took the lock a binding write held")
	}
	close(leave)
	<-done
}
