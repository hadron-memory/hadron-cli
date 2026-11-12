//go:build linux

package hostpoller

import "golang.org/x/sys/unix"

// pidfdOpen is the syscall shim, a package var so a test can model a kernel
// or a seccomp policy without PidfdOpen.
var pidfdOpen = unix.PidfdOpen

// pinPid reserves the process's id until the returned release function is
// called: a pidfd holds a reference to the struct pid, and a number with a
// live reference is never handed to a new process or a new process group
// (#1474 delta round 2, Jane D1: durable lifetime ownership). Called at
// launch, before the process can be reaped, so the pin is race-free —
// nothing can take the number between the launch and the reservation.
//
// A kernel or policy without pidfds (before 5.3, or a blocked pidfd_open)
// returns no pin and the stop falls back to probing alone — the position of
// the non-Linux Unix platforms, documented with detach_unix's stop.
func pinPid(pid int) (release func()) {
	fd, err := pidfdOpen(pid, 0)
	if err != nil {
		return nil
	}
	return func() { _ = unix.Close(fd) }
}
