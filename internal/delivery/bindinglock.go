package delivery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// bindingLockFileName is the lock `hadron team session start` (and every other
// binding writer) holds while it changes a worktree's binding: the same file
// internal/cmd/team locks, beside the binding, never the binding itself (a
// rename would replace the locked inode).
const bindingLockFileName = "hadron-team-session.lock"

// ErrBindingLockBusy: the binding's writer held the lock for the whole wait.
var ErrBindingLockBusy = errors.New("the worktree's session binding is being written")

// LockBinding takes the worktree's session-binding lock, waiting at most wait
// for a writer to finish. The caller holds it from its final binding read
// through its signal and releases it BEFORE any wait, so a bind never blocks
// for longer than one read and one signal (hadron-cli#2068). The kernel
// drops the lock when the descriptor closes, including on process death.
//
// A worktree with no binding location (a plain directory with no cache yet,
// or no worktree at all) has nothing to lock: the release is a no-op and the
// error is nil, as there is no writer to exclude. An unreadable .git pointer
// is an error: nothing can be said about who writes there.
func LockBinding(worktree string, wait time.Duration) (release func(), err error) {
	dir, state := bindingDir(worktree)
	switch state {
	case bindingDirNone:
		return func() {}, nil
	case bindingDirUnusable:
		return nil, errors.New("the worktree's .git pointer cannot be read")
	case bindingDirPlain:
		dir = filepath.Join(worktree, plainCacheName)
		if info, serr := os.Stat(dir); serr != nil || !info.IsDir() {
			return func() {}, nil // no cache, no binding, nobody to exclude yet
		}
	}
	path := filepath.Join(dir, bindingLockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 — derived from the worktree's git dir or cache
	if err != nil {
		return nil, fmt.Errorf("opening the session binding lock: %w", err)
	}
	deadline := time.Now().Add(wait)
	for {
		ok, lerr := tryLockReporter(f)
		if lerr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("taking the session binding lock: %w", lerr)
		}
		if ok {
			return func() { _ = f.Close() }, nil // closing releases it
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, ErrBindingLockBusy
		}
		time.Sleep(10 * time.Millisecond)
	}
}
