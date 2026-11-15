//go:build unix

package delivery

import (
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
func openLockFile(dir, name string) (*os.File, error) {
	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening the session binding location (a link?): %w", err)
	}
	defer func() { _ = unix.Close(dfd) }()
	if afterDirOpenHook != nil {
		afterDirOpenHook(dir)
	}
	fd, err := unix.Openat(dfd, name, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the session binding lock (a link?): %w", err)
	}
	return os.NewFile(uintptr(fd), filepath.Join(dir, name)), nil
}
