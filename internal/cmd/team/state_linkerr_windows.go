//go:build windows

package team

import (
	"errors"
	"syscall"
)

// linkUnsupported says whether a failed hard link means the filesystem cannot
// make one (FAT/exFAT, a share without links), as opposed to a real fault.
func linkUnsupported(err error) bool {
	if errors.Is(err, errors.ErrUnsupported) {
		return true
	}
	var no syscall.Errno
	// ERROR_INVALID_FUNCTION (1), ERROR_NOT_SUPPORTED (50).
	return errors.As(err, &no) && (no == 1 || no == 50)
}
