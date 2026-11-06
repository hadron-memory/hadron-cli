//go:build !windows

package delivery

import (
	"os"
	"syscall"
)

// Only advisory .lastfail reads use this helper; no global outbox semantics change.
func openStopLastFail(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) // #nosec G304 — validated hashed outbox sibling, fd checked before bounded read
}
