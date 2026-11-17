//go:build !windows

package orca

import (
	"os"
	"syscall"
)

func openHostReadOnly(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0) // #nosec G304 — explicit read-only roster or git binding path; opened handle validated
}
