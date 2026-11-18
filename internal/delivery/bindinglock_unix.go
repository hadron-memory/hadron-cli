//go:build unix

package delivery

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// afterDirOpenHook runs after the directory descriptor is held and before the
// file is opened; a test swaps the directory there.
var afterDirOpenHook func(dir string)

// lockCreatedHook runs right after a new lock file exists and before its
// creator has finished with it; a test opens the file there (cli#2238).
var lockCreatedHook func(path string)

// openLockFile opens (creating, 0600) the lock file name inside dir without
// following a link anywhere that matters: dir is opened O_NOFOLLOW|O_DIRECTORY
// and the file is opened RELATIVE to that descriptor, O_NOFOLLOW, so a worker
// that swaps the directory for a link after the caller checked it changes
// nothing: the descriptor already holds the directory that was checked.
func openLockFile(dir, name string, beforeCreate func() error) (*os.File, error) {
	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening the session binding location (a link?): %w", err)
	}
	defer func() { _ = unix.Close(dfd) }()
	if afterDirOpenHook != nil {
		afterDirOpenHook(dir)
	}
	// Try without O_CREAT first: another uid may take an existing lock, but
	// must never recreate one removed between observation and open.
	flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := openLockRetrying(dfd, name, flags)
	if errors.Is(err, unix.ENOENT) {
		if beforeCreate != nil {
			if err := beforeCreate(); err != nil {
				return nil, err
			}
		}
		fd, err = createLockExact(dfd, dir, name, flags)
	}
	if err != nil {
		return nil, fmt.Errorf("opening the session binding lock (a link?): %w", err)
	}
	return os.NewFile(uintptr(fd), filepath.Join(dir, name)), nil
}

// createLockExact creates the lock WITH ITS FINAL MODE and publishes it under
// name in one step. The create mode is 0600 &^ umask (r-------- under 0277, 000
// under 0777); a file made at its own name and chmodded afterwards is visible,
// for a moment, in a mode a concurrent opener cannot open (cli#2238, Julius's
// P2). So a private temporary is created exclusively inside the held
// directory, set to exactly 0600 through its descriptor, and linked to name:
// the name never exists in any other mode. A lost race (the name is taken)
// reopens the winner's file, which is never touched. Everything is relative to
// the held directory descriptor, so a swapped directory changes nothing.
func createLockExact(dfd int, dir, name string, flags int) (int, error) {
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return -1, err
	}
	tmp := name + ".new-" + hex.EncodeToString(suffix)
	fd, err := unix.Openat(dfd, tmp, flags|unix.O_CREAT|unix.O_EXCL, 0o600)
	if err != nil {
		return -1, err
	}
	if lockCreatedHook != nil {
		lockCreatedHook(filepath.Join(dir, name))
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		_ = unix.Close(fd)
		_ = unix.Unlinkat(dfd, tmp, 0)
		return -1, fmt.Errorf("setting the session binding lock mode: %w", err)
	}
	if lockLinkHook != nil {
		err = lockLinkHook()
	} else {
		err = unix.Linkat(dfd, tmp, dfd, name, 0)
	}
	_ = unix.Unlinkat(dfd, tmp, 0)
	if errors.Is(err, unix.EEXIST) {
		_ = unix.Close(fd)
		return openLockRetrying(dfd, name, flags) // created by another bind first
	}
	if err == nil {
		return fd, nil
	}
	_ = unix.Close(fd)
	if !linkFallbackApplies(err) {
		return -1, err // a real fault (EIO ...) surfaces; it is not "no hard links"
	}
	// The filesystem has no hard links (FAT/exFAT, some network mounts): the
	// lock was created there before, so it still is. Create it at its own
	// name and set it exact at once; a concurrent opener that meets the
	// restrictive mode in that moment retries for a bounded time.
	fd, err = unix.Openat(dfd, name, flags|unix.O_CREAT|unix.O_EXCL, 0o600)
	if errors.Is(err, unix.EEXIST) {
		return openLockRetrying(dfd, name, flags)
	}
	if err != nil {
		return -1, err
	}
	if lockFallbackChmodHook != nil {
		err = lockFallbackChmodHook()
	}
	if err == nil {
		err = unix.Fchmod(fd, 0o600)
	}
	if err != nil {
		// The file is ours (O_EXCL) but not usable. It is NOT removed: a name
		// is never deleted on a guess about which inode it still names, since a
		// racing actor could have replaced it and the lock's mutual exclusion
		// would go with it. The error says how to clear it (cli#2238).
		_ = unix.Close(fd)
		return -1, fmt.Errorf("setting the session binding lock mode: %w (remove %s only if every lock user (the hadron team session commands that read or write the binding, and the host poller's stop and relaunch paths) stays stopped for the whole deletion)", err, filepath.Join(dir, name))
	}
	return fd, nil
}

// linkFallbackApplies says whether a failed hard link means the filesystem cannot
// make one (FAT/exFAT, some network mounts, a cross-device or link-limited
// directory), as opposed to a real fault such as EIO, which must surface.
// EPERM qualifies because FAT-like filesystems and some kernels report it for
// link(2) itself. EPERM can mean unsupported hard links or an
// operation-specific denial (a policy may deny link creation yet allow ordinary
// file creation), so the fallback may succeed in either case; when it too
// fails, that error is the one returned.
func linkFallbackApplies(err error) bool {
	for _, e := range []error{unix.ENOTSUP, unix.EOPNOTSUPP, unix.EPERM, unix.EXDEV, unix.ENOSYS, unix.EMLINK} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// lockLinkHook, when set, stands in for the hard link that publishes a new
// lock: a test makes it fail the way a filesystem without links does.
var lockLinkHook func() error

// lockFallbackChmodHook, when set, runs in the no-hard-link fallback after the
// lock is created and before its chmod: a test pauses the creator there, or
// returns an error to fail the chmod step.
var lockFallbackChmodHook func() error

// lockPermRetryHook, when set, runs each time a permission refusal is about
// to be retried: a test releases a paused creator only once the second opener
// has really met the restrictive mode.
var lockPermRetryHook func()

const (
	lockPermRetries    = 20
	lockPermRetryEvery = 10 * time.Millisecond
)

// openLockRetrying opens an existing lock, retrying a permission refusal for
// a bounded moment (see createLockExact's no-hard-link path).
func openLockRetrying(dfd int, name string, flags int) (int, error) {
	var fd int
	var err error
	for range lockPermRetries {
		fd, err = unix.Openat(dfd, name, flags, 0)
		if !errors.Is(err, unix.EACCES) {
			return fd, err
		}
		if lockPermRetryHook != nil {
			lockPermRetryHook()
		}
		time.Sleep(lockPermRetryEvery)
	}
	if errors.Is(err, unix.EACCES) {
		err = fmt.Errorf("%w (the lock file is not readable and writable by you; an interrupted create can leave it so; remove it only if every lock user (the hadron team session commands that read or write the binding, and the host poller's stop and relaunch paths) stays stopped for the whole deletion)", err)
	}
	return fd, err
}
