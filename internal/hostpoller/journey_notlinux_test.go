//go:build !linux && !windows

package hostpoller

import "testing"

// The #1970 post-exit journey tolerance is Linux-only: it is the held
// zombie's ladder (a stop of an already-exited, still-held launch may
// TERM, KILL, or keep its records under a churning /proc). This platform
// has no hold — the waiter reaps the leader at its exit — so
// detach_unix_test.go keeps the settled strict baseline verbatim and the
// ladder runner below never runs here.
func journeyLadderApplies() bool { return false }

func runPostExitJourneyLadder(t *testing.T) {
	t.Skip("the ladder tolerance is Linux-only; the strict baseline is this platform's contract")
}
