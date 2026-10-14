//go:build windows

package hostpoller

import (
	"os/exec"
	"path/filepath"
	"strings"
)

func detach(*exec.Cmd) {}

func realPath(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	return strings.ToLower(r), err
}
