//go:build !windows

package team

import (
	"errors"
	"syscall"
)

// linkFallbackApplies says whether a failed hard link means the filesystem cannot
// make one (FAT/exFAT, some network mounts, a cross-device or link-limited
// directory), as opposed to a real fault such as EIO, which must surface.
// EPERM qualifies because FAT-like filesystems and some kernels report it for
// link(2) itself. EPERM can mean unsupported hard links or an
// operation-specific denial (a policy may deny link creation yet allow ordinary
// file creation), so the fallback may succeed in either case; when it too
// fails, that error is the one returned.
func linkFallbackApplies(err error) bool {
	for _, e := range []error{syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.EPERM, syscall.EXDEV, syscall.ENOSYS, syscall.EMLINK} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
