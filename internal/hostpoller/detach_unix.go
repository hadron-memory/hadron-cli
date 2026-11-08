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
	"time"
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

// occupied scans /proc/<pid>/cwd for a process working inside path and names
// it by pid and argv (#1474: an operator must be able to tell WHICH process
// holds the tree — the comm name alone cannot). Only processes this user may
// inspect are seen, which covers agents the holder's user runs. Without /proc
// (macOS) it reports none.
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
			return fmt.Sprintf("pid %d, %s", pid, processArgv(e.Name())), nil
		}
	}
	return "", nil
}

// processArgv names a process by its command line (/proc/<pid>/cmdline,
// NUL-separated), falling back to comm for a process with no command line
// (a kernel thread) or an unreadable one.
func processArgv(dir string) string {
	raw, err := os.ReadFile(filepath.Join("/proc", dir, "cmdline"))
	fields := strings.FieldsFunc(string(raw), func(r rune) bool { return r == 0 })
	if err == nil && len(fields) > 0 {
		return strings.Join(fields, " ")
	}
	comm, _ := os.ReadFile(filepath.Join("/proc", dir, "comm"))
	return strings.TrimSpace(string(comm))
}

// terminateGroup stops the process group a launch created. A launched agent
// runs as its own session leader (detach sets Setsid), so its group id is its
// pid: SIGTERM to the group, a bounded grace, then SIGKILL to the group. The
// caller has checked the agent is not Done, and a pid cannot be reused while
// its exit is unreaped — reaping is what closes Done — so a group id reached
// here can only be the launch's own.
func terminateGroup(pid int, grace time.Duration, done <-chan struct{}) string {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Sprintf("could not stop the launched process group (pid %d): %v", pid, err)
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		return fmt.Sprintf("terminated the launched process group (pid %d)", pid)
	case <-timer.C:
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Sprintf("SIGTERM did not stop the launched process group (pid %d) and SIGKILL failed: %v", pid, err)
	}
	select {
	case <-done:
		return fmt.Sprintf("killed the launched process group (pid %d) after it ignored SIGTERM for %s", pid, grace)
	case <-time.After(grace):
		return fmt.Sprintf("the launched process group (pid %d) did not exit within %s of SIGKILL", pid, grace)
	}
}
