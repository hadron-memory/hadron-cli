//go:build unix

package secretfile

import (
	"os"
	"syscall"
)

// createExcl creates path exclusively at 0600 and refuses a symlink as the
// final path component (O_NOFOLLOW).
func createExcl(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
}
