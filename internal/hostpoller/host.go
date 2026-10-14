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

func isScrubbed(name string) bool {
	for _, s := range scrubbedEnv {
		if name == s {
			return true
		}
	}
	return false
}
