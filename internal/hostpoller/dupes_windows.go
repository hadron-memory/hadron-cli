//go:build windows

package hostpoller

// ScanWorktreeAgents cannot tell on Windows (no /proc): it reports none, and
// the health sweep's duplicate detection degrades to its reports, as
// occupied does for worktree.remove.
func ScanWorktreeAgents(string) []WorktreeAgent { return nil }

// WorktreeAgent is documented in dupes_unix.go.
type WorktreeAgent struct {
	PID        int
	Display    string
	Base       string
	Supervised bool
	StartTicks int64
}
