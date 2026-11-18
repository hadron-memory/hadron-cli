//go:build !linux

package team

// pinOwnAgent pins nothing off Linux: there is no pidfd to pin the agent by,
// and the exit relaunch is Linux only. The agent is told to exit.
func pinOwnAgent(string) (ownAgent, bool) { return nil, false }
