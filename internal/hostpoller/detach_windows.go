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
