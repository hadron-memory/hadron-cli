//go:build windows

package team

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock, blocking until it is available.
//
// LockFileEx is the Windows equivalent of flock(2) for this purpose, and
// shares the property that makes locking acceptable here: the lock is released
// when the handle closes, including on process exit, so there is no stale lock
// to recover from. goreleaser ships a windows_amd64 build, so this path is not
// hypothetical.
func lockFile(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, 1, 0,
		new(windows.Overlapped),
	)
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
