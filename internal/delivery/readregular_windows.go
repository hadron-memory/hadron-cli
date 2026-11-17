//go:build windows

package delivery

import (
	"io"
	"os"
)

// readSmallRegular reads a worker-writable file: a regular file only (not a
// link), at most maxTerminalRecord bytes. Anything else is "no file".
func readSmallRegular(path string) ([]byte, bool) {
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxTerminalRecord+1))
	if err != nil || len(b) > maxTerminalRecord {
		return nil, false
	}
	return b, true
}
