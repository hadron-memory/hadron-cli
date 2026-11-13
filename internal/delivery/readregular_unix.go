//go:build unix

package delivery

import (
	"io"
	"os"
	"syscall"
)

// readSmallRegular reads a worker-writable file: never through a symlink,
// never blocking on a FIFO or device (O_NONBLOCK), only a regular file, and
// at most maxTerminalRecord bytes. Anything else is "no file".
func readSmallRegular(path string) ([]byte, bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(f, maxTerminalRecord+1))
	if err != nil || len(b) > maxTerminalRecord {
		return nil, false
	}
	return b, true
}
