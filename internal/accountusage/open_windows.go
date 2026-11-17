package accountusage

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// OPEN_REPARSE_POINT opens the final reparse point itself rather than its
// target. Inspect that handle before handing it to a reader; path attributes
// would reintroduce a check/open race. Parent traversal is hardened on Unix;
// this Windows slice rejects final-component reparse points only.
func openRollout(_ string, path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return nil, syscall.ELOOP
	}
	return os.NewFile(uintptr(handle), path), nil
}
