//go:build windows

package delivery

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// afterDirOpenHook runs while the directory handle is held, before file open.
var afterDirOpenHook func(dir string)

// openLockFile opens (creating) the lock file name inside dir WITHOUT following
// a reparse point: FILE_FLAG_OPEN_REPARSE_POINT opens a link itself rather than
// its target, and the opened handle's own attributes are checked, so a link
// swapped in after the caller's checks is refused, never followed or created
// through. The directory is checked the same way on its handle and that handle
// stays open, without delete-sharing, until the file is open: Windows has no
// openat, so holding the directory is what keeps it from being swapped between
// the two opens.
func openLockFile(dir, name string) (*os.File, error) {
	// The directory handle is held open, without FILE_SHARE_DELETE, until the
	// lock file is open and checked: while it is held the directory (and so
	// every path through it) cannot be renamed or replaced by a link, which
	// closes the gap between the two opens.
	dh, err := windows.CreateFile(mustUTF16(dir), windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, fmt.Errorf("opening the session binding location: %w", err)
	}
	defer func() { _ = windows.CloseHandle(dh) }()
	if err := refuseReparse(dh); err != nil {
		return nil, err
	}
	if afterDirOpenHook != nil {
		afterDirOpenHook(dir)
	}
	path := filepath.Join(dir, name)
	h, err := openNoReparse(path, 0, windows.OPEN_ALWAYS)
	if err != nil {
		return nil, fmt.Errorf("opening the session binding lock: %w", err)
	}
	if err := refuseReparse(h); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func mustUTF16(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		p, _ = windows.UTF16PtrFromString("")
	}
	return p
}

func openNoReparse(path string, flags uint32, disposition uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		disposition, flags|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
}

func refuseReparse(h windows.Handle) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	return refusesAttributes(info.FileAttributes)
}
