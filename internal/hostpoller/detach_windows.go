//go:build windows

package hostpoller

import (
	"context"
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

// stop is the Windows Terminate: Windows has no group probe, so a launch
// that has exited cannot be told from a reused pid — after cmd.Wait the
// handle is gone, and Windows reuses pids aggressively. taskkill
// /PID <pid> /T /F on the exited launch's pid would force-kill whatever
// process now holds the number AND its descendant tree (#1474 delta review
// P1-1); the exited-before-binding path always reaches Terminate this way.
// Once Done is closed the stop is confirmed without signalling anything.
// Descendants the launch left behind cannot be reached by pid after its
// exit; a Job Object assigned at launch would close that — left for later,
// documented here. The shared decision checks exit again after authorization
// waits (#2099), before calling taskkill. This closes the lock-wait window,
// not the remaining numeric-PID race between the final check and taskkill:
// no process handle is retained across that action.
func (a *execAgent) stopAuthorized(authorize Authorize) (bool, string) {
	return a.stopTreeAuthorized(authorize, func() (bool, string) { return terminateGroup(a.pid, 0) })
}

const taskkillTimeout = 10 * time.Second
const taskkillWaitDelay = time.Second

// runTaskkill is the process-tree kill terminateGroup uses, as a package var
// so the Windows tests can observe (and refuse) it.
var runTaskkill = func(ctx context.Context, pid int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	cmd.WaitDelay = taskkillWaitDelay
	return cmd.CombinedOutput()
}

// terminateGroup stops the process tree a launch created: Windows has no
// process groups (a launched agent stays in the poller's console), so the
// tree is the closest thing to the group Unix stops. taskkill /T reaches the
// pid and its descendants, never the poller itself.
//
// The Unix semantics do not all carry over, deliberately: there is no group
// signal to try before a forceful one, and no signal-0 probe to confirm the
// tree emptied, so the stop is forceful at once (/F) and taskkill's own exit
// decides. /T walks parentage, so a descendant of an already-exited parent
// can be missed and a stale parent pid can match a reused one — which is
// why the decision above checks for an observed exit before taskkill. The
// last check still is not atomic with taskkill. Authorization remains held
// through this command's return, including its cancellation/drain handling.
func terminateGroup(pid int, _ time.Duration) (bool, string) {
	return runTreeKill(pid, taskkillTimeout, runTaskkill)
}
