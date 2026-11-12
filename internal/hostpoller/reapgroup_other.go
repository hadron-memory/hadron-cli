//go:build !linux

package hostpoller

// noteGroupBeforeReap is the non-Linux stub: without /proc there is no
// membership to read at reap time, so the group is never marked empty and
// Terminate keeps probing the group id (the pre-P2-1 behaviour). Windows
// takes this stub too and gates Terminate on Done instead
// (detach_windows.go).
func (a *execAgent) noteGroupBeforeReap() {}
