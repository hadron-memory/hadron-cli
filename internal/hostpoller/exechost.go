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
	"syscall"
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
	// reap must not be able to release that number mid-stop. Nothing may
	// block on the leader's life while holding it.
	mu sync.Mutex
	// exited is set once the leader has ACTUALLY exited: on a hold-capable
	// platform the zombie exists before that store, so a stop that observes
	// exited knows the number is still this launch's — a zombie holds its
	// pid, the pgid, allocated until the reap (reapgroup_linux.go). It is
	// never stored while the leader may still be live, even on the paths
	// that must reap: a live leader keeps the number allocated, which is
	// exactly what makes the stop's probe oracle real while it runs.
	exited atomic.Bool
	reaped atomic.Bool
	// emptyAtReap is set when the waiter reaped the leader after a scan
	// found the group EMPTY: nothing of this launch was running at the
	// reap, the number became public then, and a later stop confirms
	// without touching it. The no-proof fallback reap never sets it.
	emptyAtReap atomic.Bool
	confirmed   atomic.Bool
}

// The SIGCHLD si_code values of a waitid answer (include/uapi/linux/wait.h;
// exported by neither Go's syscall nor x/sys). For WEXITED waits only
// CLD_EXITED, CLD_KILLED and CLD_DUMPED can appear.
const (
	cldExited = 1
	cldKilled = 2
	cldDumped = 3
)

// leaderExit is how the leader's exit happened, read off the hold's waitid
// answer: the SIGCHLD code (si_code) and the status it carries (si_status)
// — the exit code for CLD_EXITED, the signal for CLD_KILLED/CLD_DUMPED.
// This is the leader's authoritative outcome, available WHILE the zombie is
// held; the reap later reports the same value (#1474).
type leaderExit struct {
	code   int32
	status uint32
}

// describe renders the hold's answer the way a reaped wait reports the same
// exit ("exit status N", "signal: killed"), so the detail reads the same
// before and after the reap.
func (e leaderExit) describe() string {
	switch e.code {
	case cldExited:
		return fmt.Sprintf("exit status %d", e.status)
	case cldKilled, cldDumped:
		return fmt.Sprintf("signal: %v", syscall.Signal(int32(e.status)))
	default:
		return fmt.Sprintf("exit (waitid code %d, status %d)", e.code, e.status)
	}
}

// waitExit is the launch's waiter goroutine. On a hold-capable platform it
// waits the leader out WITHOUT reaping it (waitid WNOWAIT): the leader
// stays a zombie, and the zombie holds the launch's pid — the group id —
// allocated, so nothing can reuse the group's number from the exit until
// the reap (#1474). It reaps at once when a scan finds no other member (the
// common exit: nothing left to keep the zombie for) and otherwise LEAVES
// the zombie held, recording the leader's real exit — read from the hold's
// waitid answer — as the detail right away, with a drain watcher that reaps
// once the group empties on its own: a launch whose members outlive it must
// not need a stop to release the number. Every later probe or signal by
// number still goes out while the launch provably owns the number.
// Platforms without the hold (non-Linux Unix) and a failed waitid reap at
// the exit — outside the stop mutex, so a live launch stays stoppable — and
// keep the stop's documented probe position; Windows runs this same waiter
// for its Done/ExitDetail semantics only.
func (a *execAgent) waitExit() {
	if leaderHoldCapable && a.waitHeld() {
		return
	}
	a.waitReaping()
}

// waitHeld is the hold-capable waiter: it returns once the leader is an
// UNREAPED zombie, or false when the hold could not be taken (a kernel or
// policy that refuses waitid) and the caller must fall back to reaping.
// EINTR interrupts the wait without proving the exit, so it retries.
func (a *execAgent) waitHeld() bool {
	var exit leaderExit
	for {
		var err error
		exit, err = waitLeaderExit(a.pid)
		if errors.Is(err, syscall.EINTR) {
			continue // interrupted without a status: the leader is neither exited nor reaped
		}
		if err != nil {
			return false // no proof can ever be taken for this launch
		}
		break
	}
	a.exited.Store(true)
	a.mu.Lock()
	switch {
	case groupHasOtherMembers(a.pid, a.pid):
		// Members remain: hold the zombie for the stop or the drain
		// watcher. The leader's real exit — already read off the hold —
		// is the detail NOW, not a placeholder: a reader that sees Done
		// closed gets the authoritative status even if no stop ever
		// comes. If a stop already reaped (it confirmed and released
		// the number first), its finalized detail stands.
		if !a.reaped.Load() {
			a.detail = exit.describe()
		}
	default:
		a.reap() // empty group: nothing to keep the number for
		a.emptyAtReap.Store(true)
	}
	a.mu.Unlock()
	if !a.reaped.Load() {
		go a.watchDrain()
	}
	close(a.done)
	return true
}

// waitReaping is the waiter without a hold: the leader is reaped because
// that is the only way to learn its status, on the platforms with no hold
// and wherever taking one failed. The reap's wait can block for the
// leader's WHOLE life, so it runs WITHOUT the stop mutex — a stop must
// still be able to terminate a live launch — and exited is stored only once
// the leader has actually exited: until then the number is still allocated
// to the live leader and the stop's probe oracle is a real one. After the
// reap nothing proves the number is still this launch's, so on a
// hold-capable platform the stop takes its no-proof branch (unconfirmed,
// records kept); elsewhere the probe position is the documented platform
// residual.
func (a *execAgent) waitReaping() {
	detail, reaped := a.waitOut()
	a.exited.Store(true)
	if reaped {
		a.mu.Lock()
		a.detail = detail
		a.mu.Unlock()
	}
	close(a.done)
}

// drainWatchInterval is how often a held zombie's watcher looks for the
// group to have emptied on its own. It is on no stop's critical path: its
// only job is releasing the number (and finalizing nothing — the detail was
// authoritative from the exit) once the last member is gone.
var drainWatchInterval = 200 * time.Millisecond

// watchDrain reaps the held zombie once its group has emptied without a
// stop: a launch whose members outlive its leader still owns the number
// until they are gone, but once they are, the zombie must not sit unreaped
// waiting for a Terminate that may never come — that is one leaked zombie
// per such launch in a long-running poller. The reap uses the same proof
// the waiter's own empty reap uses — a clean scan — and sets the same
// emptyAtReap, so a later stop confirms without touching the number. It
// returns as soon as the zombie is reaped, by a stop or by itself.
func (a *execAgent) watchDrain() {
	t := time.NewTicker(drainWatchInterval)
	defer t.Stop()
	for range t.C {
		if a.reaped.Load() {
			return
		}
		a.mu.Lock()
		if a.reaped.Load() {
			a.mu.Unlock()
			return
		}
		if !groupHasOtherMembers(a.pid, a.pid) {
			a.reap()
			a.emptyAtReap.Store(true)
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
	}
}

// waitOut reaps the leader at most once and returns the exit detail the
// reap observed (false when something else had already reaped). It BLOCKS
// until the leader exits: the caller may hold a.mu across it only when the
// leader is already the held zombie — reap() is that caller's wrapper —
// because a live leader's wait would hold the stop mutex for the leader's
// whole life and make the launch unstoppable.
func (a *execAgent) waitOut() (string, bool) {
	if !a.reaped.CompareAndSwap(false, true) {
		return "", false
	}
	if err := a.cmd.Wait(); err != nil {
		return err.Error(), true
	}
	return "exit status 0", true
}

// reap finalizes a HELD zombie's exit under the stop mutex. The leader has
// already exited when this runs, so the wait returns at once. Always called
// with a.mu held.
func (a *execAgent) reap() {
	if detail, ok := a.waitOut(); ok {
		a.detail = detail
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
// happens only once the group has drained (in a stop, or in the waiter's
// drain watcher) — so a signal by number can only ever reach this launch's
// group, never a process that took a reused number (#1474). The platform
// stop in detach_unix/detach_windows carries the per-OS details.
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
