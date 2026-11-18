//go:build !windows

package filterreport

import (
	"golang.org/x/sys/unix"
	"os"
)

// Nonblocking, no-follow read prevents a worker from swapping a binding file
// for a FIFO/symlink between the metadata check and open. No create flags.
func openRead(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
