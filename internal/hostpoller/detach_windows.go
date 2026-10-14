//go:build windows

package hostpoller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func detach(*exec.Cmd) {}

func realPath(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	return strings.ToLower(r), err
}

// keyFileTooOpen: Windows ACLs are not mode bits; not checked there.
func keyFileTooOpen(os.FileInfo) bool { return false }

// processAlive: Windows has no signal-0 probe here; FindProcess opens a
// handle only for a live process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}

// occupied cannot tell on Windows; it reports none.
func occupied(string) (string, error) { return "", nil }
