package hostpoller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	// TerminateGrace is how long a failed start's SIGTERM gets before the
	// escalation to SIGKILL (zero: 5s).
	TerminateGrace time.Duration
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(out.String()), nil
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
	top, err := git(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil || !samePath(top, path) {
		return st, nil
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

// Launch starts the agent and returns at once. On Unix it gets its own
// session (setsid), so the poller's signals do not reach it; on Windows it
// stays in the poller's console group.
func (h ExecHost) Launch(_ context.Context, spec LaunchSpec) (Agent, error) {
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	grace := h.TerminateGrace
	if grace <= 0 {
		grace = 5 * time.Second
	}
	a := &execAgent{done: make(chan struct{}), pid: cmd.Process.Pid, grace: grace, cmd: cmd}
	go a.waitExit()
	return a, nil
}

type execAgent struct {
	done   chan struct{}
	detail string
	pid    int
	grace  time.Duration
	cmd    *exec.Cmd

	// mu serializes the stop against the waiter's reap decision: a stop
	// holding it may signal the group id because the leader (live, or the
	// zombie the waiter holds) keeps the number allocated, so the waiter's
	// reap must not be able to release that number mid-stop.
	mu sync.Mutex
	// exited is set once the leader has exited. On a hold-capable platform
	// the zombie exists before that store, so a stop that observes exited
	// knows the number is still this launch's: a zombie holds its pid —
	// the pgid — allocated until the reap (reapgroup_linux.go).
	exited atomic.Bool
	reaped atomic.Bool
	// emptyAtReap is set when the waiter reaped the leader after a scan
	// found the group EMPTY: nothing of this launch was running at the
	// reap, the number became public then, and a later stop confirms
	// without touching it. The no-proof fallback reap never sets it.
	emptyAtReap atomic.Bool
	confirmed   atomic.Bool
}

// waitExit is the launch's waiter goroutine. On a hold-capable platform it
// waits the leader out WITHOUT reaping it (waitid WNOWAIT): the leader
// stays a zombie, and the zombie holds the launch's pid — the group id —
// allocated, so nothing can reuse the group's number from the exit until
// the reap (#1474 delta round 3, Ada #102351; a pidfd does NOT do this —
// free_pid drops the allocator's idr entry at the reap while the fd stays
// valid, Jane D1 / Jonas N1 / Cameron's forced-reuse reproductions). It
// reaps at once when a scan finds no other member (the common exit:
// nothing left to keep the zombie for) and otherwise LEAVES the zombie
// held: that reap then happens only inside the stop, after the group has
// drained, so every later probe or signal by number goes out while the
// launch provably owns the number. Platforms without the hold (non-Linux
// Unix) reap at the exit and keep the stop's documented probe position;
// Windows runs this same waiter for its Done/ExitDetail semantics only.
func (a *execAgent) waitExit() {
	holdFailed := false
	if leaderHoldCapable {
		if err := waitLeaderExit(a.pid); err != nil {
			holdFailed = true // no proof can be taken: reap, and the stop reports what it knows
		}
	}
	a.exited.Store(true)
	a.mu.Lock()
	switch {
	case !leaderHoldCapable || holdFailed:
		a.reap()
	case groupHasOtherMembers(a.pid, a.pid):
		// Members remain: hold the zombie for the stop (its reap
		// finalizes this provisional detail — the authoritative exit
		// status is read by cmd.Wait at that reap, not here). If a stop
		// already reaped (it confirmed and released the number first),
		// whatever the scan sees on the number now is not this launch's,
		// and the finalized status stays.
		if !a.reaped.Load() {
			a.detail = "the leader exited; its process group still has members"
		}
	default:
		a.reap() // empty group: nothing to keep the number for
		a.emptyAtReap.Store(true)
	}
	a.mu.Unlock()
	close(a.done)
}

// reap reaps the leader at most once and finalizes the exit detail. Always
// called with a.mu held (the waiter's decision, or a confirmed stop).
func (a *execAgent) reap() {
	if !a.reaped.CompareAndSwap(false, true) {
		return
	}
	if err := a.cmd.Wait(); err != nil {
		a.detail = err.Error()
	} else {
		a.detail = "exit status 0"
	}
}

func (a *execAgent) Done() <-chan struct{} { return a.done }

// ExitDetail describes the exit. Under mu: a stop reaps the held zombie
// after Done is closed and finalizes the status then, so a read concurrent
// with a stop must not race that write.
func (a *execAgent) ExitDetail() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.detail
}

func (a *execAgent) PID() int { return a.pid }

// Terminate stops the process group this launch created and confirms the
// whole group is gone. On a hold-capable platform the group id stays this
// launch's own for the agent's whole tracked life — the waiter keeps the
// exited leader as an unreaped zombie while any member lives, and the reap
// happens only here, once the group has drained — so a signal by number can
// only ever reach this launch's group, never a process that took a reused
// number (#1474; delta round 3, Ada #102351, replacing the pidfd pin Jane
// D1 / Jonas N1 / Cameron's reproduction disproved). The platform stop in
// detach_unix/detach_windows carries the per-OS details.
func (a *execAgent) Terminate() (bool, string) {
	return a.stop()
}

// Occupied looks for a process whose working directory is inside path, via
// /proc (Linux). Without /proc it cannot tell and reports none.
func (ExecHost) Occupied(_ context.Context, path string) (string, error) {
	return occupied(path)
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
