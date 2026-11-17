//go:build windows

package team

import "os"

// Windows has no O_NONBLOCK; the opened handle is still validated as a
// regular file before it is read.
func shedOpenTranscript(path string) (*os.File, error) {
	return os.Open(path) // #nosec G304 — an explicit, opt-in path the operator named
}
