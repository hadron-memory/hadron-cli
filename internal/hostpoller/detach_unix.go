//go:build !windows

package hostpoller

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// detach starts the agent in a new session, so it outlives the poller and
// the poller's signals do not reach it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func realPath(p string) (string, error) { return filepath.EvalSymlinks(p) }

// keyFileTooOpen: any group or other permission bit.
func keyFileTooOpen(info os.FileInfo) bool { return info.Mode().Perm()&0o077 != 0 }

// processAlive: signal 0 probes without delivering; EPERM still means a
// process holds the pid.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// occupied scans /proc/<pid>/cwd for a process working inside path. Only
// processes this user may inspect are seen, which covers agents the holder's
// user runs. Without /proc (macOS) it reports none.
func occupied(path string) (string, error) {
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir("/proc")
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	self := os.Getpid()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err != nil {
			continue // gone, or not ours to inspect
		}
		if cwd == root || strings.HasPrefix(cwd, root+string(os.PathSeparator)) {
			comm, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
			return fmt.Sprintf("pid %d, %s", pid, strings.TrimSpace(string(comm))), nil
		}
	}
	return "", nil
}
