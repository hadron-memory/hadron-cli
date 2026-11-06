//go:build windows

package delivery

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

// OPEN_REPARSE_POINT opens the link itself, not its target. Reject all reparse
// points from the opened handle before os.File can read any bytes.
func openStopLastFail(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(h)
		return nil, errors.New("advisory status is a reparse point")
	}
	return os.NewFile(uintptr(h), path), nil
}
