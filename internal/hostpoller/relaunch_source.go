package hostpoller

import "context"

// RelaunchSource reads the canonical App and this host's current desired rows.
// Missing/malformed identity is an error, never an empty desired-state proof.
type RelaunchSource interface {
	RelaunchSnapshot(context.Context, string) (RelaunchSnapshot, error)
	// RelaunchWorker is the fresh read about ONE worker through
	// hadron-server#4693's `worker` selector (cli#2816): the identity fields
	// describe the App and host, and the rows carry at most the named worker.
	// The read is about this host key only: an empty row list is the
	// selector's own definite answer — this host key is not shown the worker
	// as desired on this host — and says nothing about the worker anywhere
	// else (another host's, a retired and an unknown worker all read the
	// same; cor:agt:020:08). Any error is unknown, never an empty answer
	// (cor:agt:020:03).
	RelaunchWorker(ctx context.Context, hostID, workerID string) (RelaunchSnapshot, error)
}
type RelaunchSnapshot struct {
	AppID, HostID, Server, SweepMode string
	Workers                          []RelaunchWorker
}
type RelaunchWorker struct {
	WorkerID, Family, Desired, Reason, SessionID string
	Worktrees                                    []string
}
