package hostpoller

import "context"

// RelaunchSource reads the canonical App and this host's current desired rows.
// Missing/malformed identity is an error, never an empty desired-state proof.
type RelaunchSource interface {
	RelaunchSnapshot(context.Context, string) (RelaunchSnapshot, error)
}
type RelaunchSnapshot struct {
	AppID, HostID, Server, SweepMode string
	Workers                          []RelaunchWorker
}
type RelaunchWorker struct {
	WorkerID, Family, Desired, Reason, SessionID string
	Worktrees                                    []string
}
