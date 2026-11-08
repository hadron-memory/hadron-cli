//go:build windows

package hostpoller

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

// terminateGroup stops the process tree a launch created: Windows has no
// process groups (a launched agent stays in the poller's console), so the
// tree is the closest thing to the group Unix stops. taskkill /T reaches the
// pid and its descendants, never the poller itself.
func terminateGroup(pid int, _ time.Duration, _ <-chan struct{}) string {
	out, err := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("could not stop the launched process tree (pid %d): %v: %s", pid, err, strings.TrimSpace(string(out)))
	}
	return fmt.Sprintf("terminated the launched process tree (pid %d)", pid)
}
