//go:build !windows

package team

import (
	"os"
	"syscall"
)

// shedOpenTranscript opens without blocking, so a FIFO swapped in for the
// path after any earlier check still cannot hang the shed ahead of the
// checkpoint and the claim; the caller validates the OPENED handle.
func shedOpenTranscript(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) // #nosec G304 — an explicit, opt-in path the operator named
}
