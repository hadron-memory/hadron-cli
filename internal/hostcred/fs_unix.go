//go:build !windows

package hostcred

import (
	"errors"
	"os"
	"syscall"
)

// tooOpen: any group or other permission bit.
func tooOpen(fi os.FileInfo) bool { return fi.Mode().Perm()&0o077 != 0 }

// openNoFollow opens path for reading without following a symlink at its last
// component, so the mode check runs on the file that is read.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

func isSymlinkErr(err error) bool { return errors.Is(err, syscall.ELOOP) }

// lockNoFollow makes the lock file's open refuse a symlink at its last component (ELOOP).
const lockNoFollow = syscall.O_NOFOLLOW

// refuseSymlinkedLock: O_NOFOLLOW refuses the link itself, so there is nothing to check first.
func refuseSymlinkedLock(string) error { return nil }
