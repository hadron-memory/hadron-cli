//go:build !linux

package hostpoller

import (
	"context"
	"errors"
)

// StopAgents cannot identify a process it did not launch without Linux's
// /proc and pidfd, so it signals nothing and says so: worker.end's stop then
// reports that no process was touched (hadron-cli#2057 C2a).
func (ExecHost) StopAgents(context.Context, string, Authorize) ([]AgentStop, error) {
	return nil, errors.New("finding a worker's agent process needs Linux (/proc and pidfd)")
}
