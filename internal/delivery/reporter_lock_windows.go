//go:build windows

package delivery

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockReporter takes the reporter's exclusive lock without waiting
// (LockFileEx, released when the handle closes, as flock is on unix).
func tryLockReporter(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}
