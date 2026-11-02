package hostpoller

import (
	"errors"
	"syscall"
	"time"
)

// ProcessControl is the reset handler's only way to touch an agent process
// (#1043). It observes a process by pid into a handle that pins it (a pidfd
// on Linux), so a later signal can only reach that process, never a reused
// pid. Now is CLOCK_BOOTTIME: it keeps counting across a suspend, so a lease
// measured on it is never longer than the server meant.
type ProcessControl interface {
	Observe(pid int) (ProcessHandle, error)
	Now() time.Duration
}

// ProcessHandle is one observed process.
type ProcessHandle interface {
	Identity() ProcessIdentity
	// SignalWithin sends sig only if now() - t0 < lease, with the check and
	// the send adjacent. t0 is read BEFORE the authorizeStop request goes
	// out, so a slow answer shortens the window and never lengthens it. It
	// re-reads the clock after the send and returns how far past the lease
	// the send landed (0 when inside): a late stop the caller records
	// (ruling 0087 A). ErrLeaseExpired means nothing was sent.
	SignalWithin(sig syscall.Signal, t0, lease time.Duration, now func() time.Duration) (late time.Duration, err error)
	Close()
}

// ErrLeaseExpired: the stop lease had run out at the check; no signal sent.
var ErrLeaseExpired = errors.New("the stop lease expired before the signal; nothing was sent")

// ErrUnsupportedPlatform: this platform has no pidfd and boot clock, so the
// handler refuses before any claim rather than signal a bare pid.
var ErrUnsupportedPlatform = errors.New("worker.reset-context needs Linux (pidfd and CLOCK_BOOTTIME); this host refuses it")

// ErrHandleClosed: the handle was closed; nothing was sent.
var ErrHandleClosed = errors.New("the process handle is closed; nothing was sent")
