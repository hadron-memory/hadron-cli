//go:build !windows

package team

import (
	"errors"
	"syscall"
)

// linkUnsupported says whether a failed hard link means the filesystem cannot
// make one (FAT/exFAT, some network mounts, a cross-device or link-limited
// directory), as opposed to a real fault such as EIO, which must surface.
func linkUnsupported(err error) bool {
	for _, e := range []error{syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.EPERM, syscall.EXDEV, syscall.ENOSYS, syscall.EMLINK} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
