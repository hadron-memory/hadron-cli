//go:build !linux

package hostpoller

import "errors"

// leaderHoldCapable is false on the platforms without the zombie hold:
// there is no /proc membership oracle to gate the reap on, and x/sys wraps
// no waitid on them, so the waiter reaps the leader at its exit and the
// stop signals only while the leader is live, aborting to unconfirmed at
// the first observation of the exit — the number's release cannot be
// serialized with a stop there, which is the documented residual in
// host_run's help (#1474). Windows takes these stubs too and gates its
// stop on Done instead (detach_windows.go).
const leaderHoldCapable = false

func waitLeaderExit(int) error {
	return errors.New("no leader hold on this platform")
}

// readLeaderExitStatus, leaderZombie and pollLeaderState exist only to
// keep the shared waiter compiling here; every caller is gated on
// leaderHoldCapable, so they never run (Windows runs waitExit for its
// Done/ExitDetail semantics only, through waitReaping).
func readLeaderExitStatus(int) leaderExit { return leaderExit{kind: unknownStatus} }

func leaderZombie(int) bool { return false }

func pollLeaderState(int) (byte, error) { return 0, errors.New("no /proc on this platform") }

// groupHasOtherMembers has no oracle here; the stop never asks (every call
// site is gated on leaderHoldCapable) and the stub exists only so the
// shared code type-checks.
func groupHasOtherMembers(pgid, leader int) bool { return true }
