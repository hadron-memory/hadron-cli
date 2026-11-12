//go:build !linux

package hostpoller

import "errors"

// leaderHoldCapable is false on the platforms without the zombie hold:
// there is no /proc membership oracle to gate the reap on, and x/sys wraps
// no waitid on them, so the waiter reaps the leader at its exit and the
// stop keeps the probe-and-signal position — a reused number can answer a
// probe there, which is the documented residual in host_run's help
// (#1474). Windows takes these stubs too and gates its stop on Done
// instead (detach_windows.go).
const leaderHoldCapable = false

func waitLeaderExit(int) (leaderExit, error) {
	return leaderExit{}, errors.New("no leader hold on this platform")
}

// groupHasOtherMembers has no oracle here; the stop never asks (every call
// site is gated on leaderHoldCapable) and the stub exists only so the
// shared code type-checks.
func groupHasOtherMembers(pgid, leader int) bool { return true }
