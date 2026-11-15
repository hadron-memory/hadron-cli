package hostpoller

import (
	"context"
	"strings"
)

// Host is everything the poller does to the machine: git worktrees and agent
// processes. The real one shells out to git and execs the configured argv;
// tests fake it.
type Host interface {
	// CreateWorktree adds a worktree at path on a new branch from base.
	CreateWorktree(ctx context.Context, repoPath, path, branch, base string) error
	// Inspect reads a worktree's state; Exists is false for a missing path.
	Inspect(ctx context.Context, path string) (WorktreeState, error)
	// RemoveWorktree removes a clean worktree.
	RemoveWorktree(ctx context.Context, path string) error
	// Launch starts an agent and returns at once; the agent keeps running.
	Launch(ctx context.Context, spec LaunchSpec) (Agent, error)
	// Prompt runs a short command (worker.end's prompt) to completion.
	Prompt(ctx context.Context, argv []string, dir string, env []string) error
	// Occupied names a process (other than the poller) working inside path,
	// or "" when none is seen. Where the host cannot tell (no /proc), it
	// reports none, and worktree.remove leans on the recorded agents alone.
	Occupied(ctx context.Context, path string) (string, error)
	// Alive reports whether a process id is still running.
	Alive(pid int) bool
	// ResetWorkspace brings an existing worktree back to a clean base at task
	// start (hadron-cli#2057 C2b): refused when dirty or when HEAD is on no
	// remote branch, rescue refs kept, nothing created or removed. A directory
	// with no Git is left alone (Reset false, no refusal).
	ResetWorkspace(ctx context.Context, path string) (ResetOutcome, error)
	// StopAgents stops every agent process working inside path that this
	// host did not launch itself (hadron-cli#2057 C2a: an agent started from
	// a terminal is not the poller's child), each with its process group:
	// SIGTERM, a grace, then SIGKILL. One entry per agent found; none when
	// no agent works there. An error means the host cannot tell (no /proc,
	// no pidfd) and nothing was signalled. authorize is asked before EVERY signal (after the agent is pinned and its group pinned): it answers whether the stop is still authorized (the worktree's session binding has not moved on to another session) and, when it is, returns a release that the caller calls right after that one signal — the binding is held unchanged from the check through the signal (hadron-cli#2068), before any Unix grace wait; Windows instead holds authority until synchronous taskkill returns, including cancellation/drain. A refusal
	// signals nothing and says so; a nil authorize always allows.
	StopAgents(ctx context.Context, path string, authorize Authorize) ([]AgentStop, error)
}

// AgentStop is one agent process StopAgents handled.
type AgentStop struct {
	PID int
	// Stopped is true only when everything that was signalled is observed
	// gone.
	Stopped bool
	// Detail names the process (pid and a short redacted argv) and what
	// happened to it.
	Detail string
}

// WorktreeState is what worktree.create and worktree.remove decide on.
type WorktreeState struct {
	Exists bool
	// IsWorktree is false for a path that exists but is not a git worktree.
	IsWorktree bool
	Branch     string
	// Dirty is uncommitted or untracked changes.
	Dirty bool
	// Upstream is the branch's upstream, empty when it has none.
	Upstream string
	// Ahead counts commits not on the upstream.
	Ahead int
}

// LaunchSpec is one agent start: the argv from host configuration, run in
// the worktree, with an environment that carries the launch grant and no
// Hadron credential of the host's.
type LaunchSpec struct {
	Argv []string
	Dir  string
	Env  []string
}

// Agent is a launched agent process.
type Agent interface {
	// Done is closed when the process exits.
	Done() <-chan struct{}
	// ExitDetail describes how it exited, once Done is closed.
	ExitDetail() string
	// PID is its process id (0 when unknown).
	PID() int
	// Terminate stops the agent and everything it started — the process
	// group (Unix) or tree (Windows) the launch created — and reports
	// whether the stop is CONFIRMED: the whole group observed gone, not
	// only the launched process. stopped=false means it could not be
	// confirmed (a signal failed, or the group outlived the waits), and the
	// caller must then keep every record of the agent, or the
	// one-agent-per-worktree guard loses a process it knows is running
	// (#1474). A group that is already gone is a confirmed stop that
	// signalled nothing. On Linux the launch's group number is held for the
	// agent's whole tracked life (the unreaped leader, reapgroup_linux.go),
	// so a stop can only ever signal this launch's group.
	Terminate() (stopped bool, detail string)
}

// Environment variables a launched agent must never inherit: the host key's
// own channel, and the CLI token channel the host process might carry. The
// agent binds on its holder's own stored token (#1883, option A), and the
// grant is the only launch credential it gets.
var scrubbedEnv = []string{HostKeyEnv, "HADRON_TOKEN", "HADRON_LAUNCH_GRANT"}

// agentEnv builds a launched agent's environment from the poller's: every
// variable but the scrubbed ones and any whose value carries the host key,
// plus the given additions.
func agentEnv(parent []string, hostKey string, add ...string) []string {
	out := make([]string, 0, len(parent)+len(add))
	for _, kv := range parent {
		name, value, _ := strings.Cut(kv, "=")
		if isScrubbed(name) || (hostKey != "" && strings.Contains(value, hostKey)) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, add...)
}

// ScrubbedEnv is parent without the credential variables and any variable
// whose value carries hostKey: the environment for a child that must not see
// the host's credentials (the Orca CLI a status read runs).
func ScrubbedEnv(parent []string, hostKey string) []string { return agentEnv(parent, hostKey) }

func isScrubbed(name string) bool {
	for _, s := range scrubbedEnv {
		if name == s {
			return true
		}
	}
	return false
}

// Authorize is the host's check before one signal: ok means the stop is still
// authorized, and release must then be called after the action: a Unix signal
// syscall or the synchronous Windows taskkill runner. Not ok
// means nothing is signalled. The poller's implementation holds the worktree's
// session-binding lock between the check and the release.
type Authorize func() (release func(), ok bool)

// allow is the Authorize that always permits and holds nothing.
func allow() (func(), bool) { return func() {}, true }
