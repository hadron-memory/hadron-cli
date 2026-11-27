//go:build windows

package hostcred

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tooOpen is never true on Windows: a Go mode there only reflects the
// read-only attribute. The file's owner-only access list is set when it is
// written (internal/secretfile), as for the host key file.
func tooOpen(os.FileInfo) bool { return false }

var errSymlink = errors.New("symlink")

// openNoFollow opens path without following a reparse point, then checks the
// attributes of the handle it opened (hadron-cli#3288 F12), so the file that
// is checked is the file that is read: a reparse point (a symlink or junction)
// is refused as a symlink, and anything but a regular file is refused by
// read's own mode check.
func openNoFollow(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return nil, os.ErrNotExist
		}
		// A directory cannot be opened without FILE_FLAG_BACKUP_SEMANTICS: refused here, as not a file.
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		_ = windows.CloseHandle(h)
		return nil, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(h)
		return nil, errSymlink
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		_ = windows.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: path, Err: errors.New("is a directory")}
	}
	return os.NewFile(uintptr(h), path), nil
}

func isSymlinkErr(err error) bool { return errors.Is(err, errSymlink) }

// lockNoFollow: Windows has no O_NOFOLLOW, so refuseSymlinkedLock checks the lock path first.
const lockNoFollow = 0

// refuseSymlinkedLock refuses a reparse point at the lock file's path before the lock library opens it (it opens
// by path, so the handle cannot be checked as the store file's is). Replacing the lock file between the check
// and the open needs write access to the config directory, which is this user's.
func refuseSymlinkedLock(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	if fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&os.ModeIrregular != 0 {
		return &Error{Path: path, Reason: "is a symlink or junction; refusing to lock through it"}
	}
	return nil
}
