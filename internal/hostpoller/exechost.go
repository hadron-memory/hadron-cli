package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ExecHost is the real Host: git for worktrees, exec for agents. Every
// command is a fixed argv — git's own, or the one in host configuration —
// never a shell string, and nothing in it comes from an operation's
// arguments except git ref and path names, which git receives as separate
// arguments after the option they belong to.
type ExecHost struct {
	// PromptTimeout bounds worker.end's prompt command.
	PromptTimeout time.Duration
	// TerminateGrace is how long worker.end's SIGTERM gets before the
	// escalation to SIGKILL (zero: 5s).
	TerminateGrace time.Duration
}

// UntrustedConfigError is a repository refused because its own git config holds
// a key outside the allowlist of inert ones (hadron-cli#2057 §10). It is not
// "this is not a worktree": Inspect reports it, and nothing was run.
type UntrustedConfigError struct{ Why string }

func (e *UntrustedConfigError) Error() string { return "untrusted repo config: " + e.Why }

// BrokenRepoError is a worker directory with its own .git entry that git cannot
// use as a repository whose top level is that directory (hadron-cli#2119): not "a
// directory without Git". Nothing was run on it.
type BrokenRepoError struct{ Why string }

func (e *BrokenRepoError) Error() string { return "broken repository: " + e.Why }

// git runs one command on a repository the worker can write to, with the
// hardened configuration and the transport allowlist (hadron-cli#2057 §10): the
// same rule as the reset's for EVERY git call this host makes. A repository whose
// own config holds a key outside the allowlist is refused outright, before the
// command runs: `status` never runs a worker's clean filter, a fetch never
// follows a worker's proxy, `worktree add` never runs a smudge filter.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	if why := untrustedRepoConfig(ctx, dir); why != "" {
		return "", &UntrustedConfigError{Why: why}
	}
	out, errOut, err := runGitRaw(ctx, dir, args...)
	if err != nil {
		msg := strings.TrimSpace(errOut)
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(out), nil
}

// CreateWorktree fetches the base from origin, then adds the worktree on a
// new branch from origin/<base> (or the local <base> when origin has none).
func (ExecHost) CreateWorktree(ctx context.Context, repoPath, path, branch, base string) error {
	start := base
	if _, err := git(ctx, repoPath, "fetch", "--quiet", "origin", "--", base); err == nil {
		start = "origin/" + base
	}
	if err := os.MkdirAll(parentDir(path), 0o755); err != nil {
		return err
	}
	// --no-track: started from origin/<base>, git would otherwise make the
	// new branch track the BASE, and worktree.remove's "pushed" check would
	// then measure the branch against main rather than against its own push.
	_, err := git(ctx, repoPath, "worktree", "add", "--no-track", "-b", branch, "--", path, start)
	return err
}

func parentDir(p string) string {
	if i := strings.LastIndexByte(p, os.PathSeparator); i > 0 {
		return p[:i]
	}
	return p
}

// Inspect reads a worktree's branch, cleanliness and push state.
func (ExecHost) Inspect(ctx context.Context, path string) (WorktreeState, error) {
	var st WorktreeState
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return st, nil
	} else if err != nil {
		return st, err
	}
	st.Exists = true
	// Not a worktree root (a plain directory, or a subdirectory, inside some
	// enclosing repository): that repository's config is not this worker's
	// business, so nothing about it is read or refused.
	if root, err := isWorktreeRoot(ctx, path); err != nil {
		return st, err // could not tell: refused, not "not a worktree"
	} else if !root {
		return st, nil
	}
	// The directory has a .git entry of its own: the config allowlist first (an
	// untrusted config runs nothing), then it must resolve to a valid repository
	// whose work tree IS the directory. Anything else is a broken repository,
	// refused with the reason, never "not a worktree" (hadron-cli#2119).
	if why := untrustedRepoConfig(ctx, path); why != "" {
		return st, &UntrustedConfigError{Why: why}
	}
	if why := ownRepoProblem(ctx, path); why != "" {
		return st, &BrokenRepoError{Why: why}
	}
	st.IsWorktree = true
	if b, err := git(ctx, path, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		st.Branch = b
	}
	status, err := git(ctx, path, "status", "--porcelain")
	if err != nil {
		return st, err
	}
	st.Dirty = status != ""
	if up, err := git(ctx, path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		st.Upstream = up
		n, err := git(ctx, path, "rev-list", "--count", "@{upstream}..HEAD")
		if err != nil {
			return st, err
		}
		if st.Ahead, err = strconv.Atoi(n); err != nil {
			return st, fmt.Errorf("reading commits ahead of %s: %w", up, err)
		}
	}
	return st, nil
}

func samePath(a, b string) bool {
	ra, errA := realPath(a)
	rb, errB := realPath(b)
	return errA == nil && errB == nil && ra == rb
}

// RemoveWorktree removes a worktree git considers clean; git refuses a
// dirty one on its own, as a second line behind Inspect.
func (ExecHost) RemoveWorktree(ctx context.Context, path string) error {
	_, err := git(ctx, path, "worktree", "remove", "--", path)
	return err
}

// Occupied looks for a process whose working directory is inside path, via
// /proc (Linux). Without /proc it cannot tell and reports none.
func (ExecHost) Occupied(_ context.Context, path string) (string, error) {
	return occupied(path)
}

// ResetWorkspace is ResetWorktree at the current time.
func (ExecHost) ResetWorkspace(ctx context.Context, path string) (ResetOutcome, error) {
	return ResetWorktree(ctx, path, time.Now())
}

// Alive reports whether pid is a running process.
func (ExecHost) Alive(pid int) bool { return processAlive(pid) }

// Prompt runs worker.end's prompt command to completion, bounded.
func (h ExecHost) Prompt(ctx context.Context, argv []string, dir string, env []string) error {
	d := h.PromptTimeout
	if d == 0 {
		d = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}
