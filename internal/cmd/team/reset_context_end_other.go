//go:build !linux

package team

// endOwnAgent signals nothing off Linux: there is no pidfd to pin the agent
// by, and the exit relaunch is Linux only. The agent is told to exit.
func endOwnAgent(string) string { return agentExitNow }

func ownAgentPID(string) bool { return false }
