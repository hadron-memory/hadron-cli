//go:build !windows

package modelreport

import (
	"os"
	"syscall"
)

// openNoFollow is non-blocking too, so a FIFO swapped in after the regular-file
// check cannot hang a hook; the fd identity check then refuses it. It refuses a symlink at the FINAL path component. It does not
// secure the ancestors; the caller's fd-versus-expected identity check does
// that work.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) // #nosec G304 — canonical, allowlisted, identity-checked path
}
