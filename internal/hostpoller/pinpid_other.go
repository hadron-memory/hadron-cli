//go:build !linux

package hostpoller

// pinPid is the Linux pidfd reservation. The other Unix platforms have no
// pidfd, so nothing is reserved and the stop probes the group id alone; what
// that leaves open is documented with stop in detach_unix and in host_run's
// help. (Windows never reaches it: its stop is gated on Done.)
func pinPid(int) (release func()) { return nil }
