//go:build linux

package team

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// endOwnAgent ends the agent that runs this command (cli#2161), by exact pid
// and never by pattern: the pid the Orca launch exported (AgentPIDEnv), and
// only while it is an ANCESTOR of this process. The ancestry is checked again
// after the pidfd pins the process, so a pid reused by anything that is not
// this command's ancestor is never signalled. Anything short of that proof
// signals nothing and the agent is told to exit.
func endOwnAgent(value string) string {
	pid, ok := parseAgentPID(value)
	if !ok || !isAncestor(pid) {
		return agentExitNow
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return agentExitNow
	}
	defer func() { _ = unix.Close(fd) }()
	if !isAncestor(pid) {
		return agentExitNow
	}
	if unix.PidfdSendSignal(fd, unix.SIGTERM, nil, 0) != nil {
		return agentExitNow
	}
	return agentEnded
}

// ownAgentPID says whether value names this command's own agent: a plain
// pid that is an ancestor now. endOwnAgent proves it again on the pinned
// process before it signals.
func ownAgentPID(value string) bool {
	pid, ok := parseAgentPID(value)
	return ok && isAncestor(pid)
}

// parseAgentPID accepts only a plain decimal pid above 1 that is not this
// process: no sign, no spaces, no leading zero.
func parseAgentPID(v string) (int, bool) {
	if v == "" || len(v) > 10 || v[0] == '0' || strings.Trim(v, "0123456789") != "" {
		return 0, false
	}
	pid, err := strconv.Atoi(v)
	if err != nil || pid <= 1 || pid == os.Getpid() {
		return 0, false
	}
	return pid, true
}

// isAncestor walks this process's parents through /proc.
func isAncestor(pid int) bool {
	p := os.Getppid()
	for i := 0; i < 256 && p > 1; i++ {
		if p == pid {
			return true
		}
		next, ok := parentOf(p)
		if !ok {
			return false
		}
		p = next
	}
	return false
}

func parentOf(pid int) (int, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	// The command name may hold spaces and parentheses: fields start after
	// the LAST ')': state, then ppid.
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(f[1])
	return ppid, err == nil
}
