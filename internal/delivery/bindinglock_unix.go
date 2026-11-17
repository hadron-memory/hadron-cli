//go:build unix

package delivery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// afterDirOpenHook runs after the directory descriptor is held and before the
// file is opened; a test swaps the directory there.
var afterDirOpenHook func(dir string)

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
		fd, err = unix.Openat(dfd, name, flags|unix.O_CREAT|unix.O_EXCL, 0o600)
		if errors.Is(err, unix.EEXIST) {
			fd, err = unix.Openat(dfd, name, flags, 0) // created by another bind first
		} else if err == nil {
			// The create mode is 0600 &^ umask: under umask 0277 or 0777 the
			// file would be r-------- or 000 and the next lock's read-write
			// open would fail (cli#2238). The creator owns it, so exact it
			// through the descriptor; an existing lock is never touched.
			if cerr := unix.Fchmod(fd, 0o600); cerr != nil {
				_ = unix.Close(fd)
				return nil, fmt.Errorf("setting the session binding lock mode: %w", cerr)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("opening the session binding lock (a link?): %w", err)
	}
	return os.NewFile(uintptr(fd), filepath.Join(dir, name)), nil
}
