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

// lockOpenHook runs between LockBinding's directory check and its open; a test
// uses it to swap the directory the way a worker could.
var lockOpenHook func(dir string)

// ErrBindingLockBusy: the binding's writer held the lock for the whole wait.
var ErrBindingLockBusy = errors.New("the worktree's session binding is being written")

// LockBinding takes the worktree's session-binding lock, waiting at most wait
// for a writer to finish. The caller holds it from its final binding read
// through its action: Unix releases after each signal syscall, before grace
// waits; Windows holds through synchronous taskkill and its cancellation/drain.
// A binding writer waits until this authority is released. The kernel
// drops the lock when the descriptor closes, including on process death.
//
// A path that is not a worktree or a plain directory at all has no binding
// location and nothing to lock: the release is a no-op and the error nil.
// On Unix, a missing plain-directory cache or a missing lock in either
// binding location is created only as the worktree owner's uid. Existing
// locks are opened under normal OS permissions. An unreadable .git pointer
// is an error: nothing can be said about who writes there.
func LockBinding(worktree string, wait time.Duration) (release func(), err error) {
	dir, state := bindingDir(worktree)
	var beforeCreate func() error
	switch state {
	case bindingDirNone:
		return func() {}, nil
	case bindingDirUnusable:
		return nil, errors.New("the worktree's .git pointer cannot be read")
	case bindingDirGit:
		beforeCreate = func() error { return allowBindingCreation(worktree) }
	case bindingDirPlain:
		beforeCreate = func() error { return allowBindingCreation(worktree) }
		// A plain directory's cache is created by the first bind, under this
		// same lock. Create it here, owner-only, as that bind does, so the
		// lock exists for both sides before anyone reads or writes: a bind
		// that races the stop then waits on this lock instead of slipping by.
		// The tree is writable by the worker, so nothing here follows a link:
		// the cache is made with Mkdir (never MkdirAll) and must be a real
		// directory afterwards.
		dir = filepath.Join(worktree, plainCacheName)
		if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
			if err := beforeCreate(); err != nil {
				return nil, err
			}
			if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return nil, fmt.Errorf("creating the session binding cache: %w", err)
			}
		}
	}
	// Whatever holds the lock must be a real directory, never a link into
	// somewhere the poller should not create files.
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return nil, errors.New("the session binding location is not a plain directory (a link or a file?), so it was not locked")
	}
	// The directory was checked above for a clear refusal; the open below does
	// not trust that check (a worker can swap the directory for a link in
	// between): it opens the directory and then the file relative to it,
	// refusing links, so the file is created only where it was validated.
	if lockOpenHook != nil {
		lockOpenHook(dir)
	}
	f, err := openLockFile(dir, bindingLockFileName, beforeCreate)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("the session binding lock is not a regular file, so it was not taken")
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

// fileAttributeReparsePoint is Windows' FILE_ATTRIBUTE_REPARSE_POINT: a
// symlink, junction or mount point. It lives here, not in the Windows file, so
// the refusal rule is a unit seam every platform's tests can pin.
const fileAttributeReparsePoint = 0x400

// refusesAttributes says whether a Windows handle's file attributes name a
// reparse point, which the lock never uses (hadron-cli#2079).
func refusesAttributes(attrs uint32) error {
	if attrs&fileAttributeReparsePoint != 0 {
		return errors.New("the session binding location is a link, so it was not used")
	}
	return nil
}

// LockUsersHint ends the manual-delete advice in a lock error: the lock file
// may be removed only while every user of the lock stays stopped for the whole
// deletion, since a user that locked the old inode would not exclude one that
// locks a new file (#2327).
const LockUsersHint = "every lock user (the hadron team session commands that read or write the binding, and the host poller's stop and relaunch paths) stays stopped for the whole deletion"
