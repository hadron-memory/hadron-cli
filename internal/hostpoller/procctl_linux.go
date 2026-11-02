//go:build linux

package hostpoller

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// LinuxProcessControl is ProcessControl over pidfd_open,
// pidfd_send_signal and CLOCK_BOOTTIME.
type LinuxProcessControl struct{}

func (LinuxProcessControl) Now() time.Duration {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		// CLOCK_BOOTTIME exists on every kernel with pidfd (5.3+); a
		// failure here is unreachable in practice and must not read as
		// "inside the lease": answer a time that is past any lease.
		return time.Duration(1<<62 - 1)
	}
	return time.Duration(ts.Nano())
}

// Observe opens a pidfd first, then reads the start time, then proves the
// pidfd's process is still alive: if it is, the pid has not been reused
// between the open and the read, so the token belongs to the pinned process.
func (LinuxProcessControl) Observe(pid int) (ProcessHandle, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("no process %d", pid)
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, fmt.Errorf("pidfd_open %d: %w", pid, err)
	}
	start, err := observeStartTime(pid)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if err := unix.PidfdSendSignal(fd, 0, nil, 0); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("process %d is gone: %w", pid, err)
	}
	return &linuxHandle{fd: fd, id: ProcessIdentity{PID: pid, StartTime: start}}, nil
}

type linuxHandle struct {
	mu     sync.Mutex
	fd     int
	closed bool
	id     ProcessIdentity
}

func (h *linuxHandle) Identity() ProcessIdentity { return h.id }

func (h *linuxHandle) SignalWithin(sig syscall.Signal, t0, lease time.Duration, now func() time.Duration) (time.Duration, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, ErrHandleClosed
	}
	// The check and the send are adjacent: nothing between them.
	if now()-t0 >= lease {
		return 0, ErrLeaseExpired
	}
	err := unix.PidfdSendSignal(h.fd, sig, nil, 0)
	after := now()
	if err != nil {
		return 0, fmt.Errorf("pidfd_send_signal: %w", err)
	}
	if late := after - t0 - lease; late > 0 {
		return late, nil
	}
	return 0, nil
}

func (h *linuxHandle) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		_ = unix.Close(h.fd)
	}
}

// observeStartTime is the read Observe makes; a test swaps it to land a
// process exit between the pidfd open and the read.
var observeStartTime = readStartTime

// readStartTime is /proc/<pid>/stat field 22, the kernel's start time in
// clock ticks since boot: with the pid, it names one process.
func readStartTime(pid int) (string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", fmt.Errorf("reading process %d: %w", pid, err)
	}
	return parseStartTime(string(b))
}

// parseStartTime reads field 22. comm (field 2) is parenthesised and may hold
// spaces and parens, so fields are counted from the LAST ')'.
func parseStartTime(stat string) (string, error) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return "", fmt.Errorf("malformed stat line")
	}
	f := strings.Fields(stat[i+1:])
	// f[0] is field 3 (state); field 22 is f[19].
	if len(f) < 20 {
		return "", fmt.Errorf("malformed stat line: %d fields after comm", len(f))
	}
	if _, err := strconv.ParseUint(f[19], 10, 64); err != nil {
		return "", fmt.Errorf("malformed start time %q", f[19])
	}
	return f[19], nil
}
