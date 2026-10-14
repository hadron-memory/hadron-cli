//go:build unix

package teamrouter

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// processGone is true only on proof no process has pid (ESRCH). EPERM is a
// live process under another user, and is never displaced.
func processGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// processStartedAtMS is when pid started, via `ps -o lstart=` (which reads
// any user's processes), in the C locale so the layout is fixed.
func processStartedAtMS(pid int) (int64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.Join(strings.Fields(string(out)), " "), time.Local)
	if err != nil {
		// _2 pads with a space that Fields collapsed; retry the unpadded form.
		t, err = time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(strings.Fields(string(out)), " "), time.Local)
		if err != nil {
			return 0, false
		}
	}
	return t.UnixMilli(), true
}

var bootTimeSec = regexp.MustCompile(`sec = (\d+)`)

// bootEpochMS is this host's boot time: now minus /proc/uptime on Linux,
// kern.boottime elsewhere.
func bootEpochMS() (int64, bool) {
	if runtime.GOOS == "linux" {
		b, err := os.ReadFile("/proc/uptime")
		if err != nil {
			return 0, false
		}
		fields := strings.Fields(string(b))
		if len(fields) == 0 {
			return 0, false
		}
		up, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return 0, false
		}
		return time.Now().UnixMilli() - int64(up*1000), true
	}
	out, err := exec.Command("sysctl", "-n", "kern.boottime").Output()
	if err != nil {
		return 0, false
	}
	m := bootTimeSec.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	sec, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil {
		return 0, false
	}
	return sec * 1000, true
}
