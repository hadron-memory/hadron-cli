//go:build unix

package delivery

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	fd, err := unix.Openat(dfd, name, flags, 0)
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
	err = unix.Linkat(dfd, tmp, dfd, name, 0)
	_ = unix.Unlinkat(dfd, tmp, 0)
	if errors.Is(err, unix.EEXIST) {
		_ = unix.Close(fd)
		return unix.Openat(dfd, name, flags, 0) // created by another bind first
	}
	if err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}
