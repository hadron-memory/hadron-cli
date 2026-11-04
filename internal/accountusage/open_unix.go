//go:build !windows

package accountusage

import (
	"os"
	"syscall"
)

// openRollout opens the final component without following a link and without
// blocking on a special file (a FIFO swapped in after the check). The caller
// still fstat-checks that it is the regular file it checked.
func openRollout(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
