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
	// watcherDone closes when the drain watcher exits (nil when none was
	// armed), so tests can wait out a watcher before swapping the scan
	// package vars it reads through.
	watcherDone chan struct{}

	// mu serializes the stop against every reap on a hold-capable
	// platform: a stop holding it may signal the group id because the
	// leader — live, or the zombie the hold keeps — cannot be reaped by
	// the waiter while mu is held, so the number cannot be released
	// mid-stop. All three reapers (the hold's empty reap, the drain
	// watcher, the stop itself) reap under mu; the only path that frees
	// the number outside mu is the no-hold platform's waitReaping, whose
	// blocking wait must not hold mu (a live launch would be
	// unstoppable), which is why that platform's stop refuses to signal
	// by number once it has seen the exit instead. Nothing may block on
	// the leader's life while holding mu — the hold's reaps wait on an
	// already-exited zombie, which returns at once.
	mu sync.Mutex
	// exited is set once the leader has ACTUALLY exited: on a hold-capable
	// platform the zombie exists before that store, so a stop that observes
	// exited knows the number is still this launch's — a zombie holds its
	// pid, the pgid, allocated until the reap (reapgroup_linux.go). It is
	// never stored while the leader may still be live, even on the paths
	// that must reap: a live leader keeps the number allocated, which is
	// exactly what makes the stop's probe oracle real while it runs.
	exited atomic.Bool
	// reaped flips only inside a reap decision. On a hold-capable platform
	// every reap runs under mu, so a stop observing reaped there knows the
	// release was serialized with it; on a no-hold platform it flips as
	// the waiter enters its blocking reap — possibly before the exit — and
	// nothing there reads it as proof about the number (the stop gates on
	// exited instead, and only while the leader is provably live).
	reaped atomic.Bool
	// emptyAtReap is set when the leader was reaped after a scan found the
	// group EMPTY: nothing of this launch was running at the reap, the
	// number became public then, and a later stop confirms without
	// touching it. A reap without that proof never sets it.
	emptyAtReap atomic.Bool
	confirmed   atomic.Bool
}

// The kinds of leader exit readLeaderExitStatus reports, decoded off the
// exit_code word /proc renders — the same word a reaping wait would
// report, so the detail reads identically before and after the reap.
const (
	unknownStatus = iota // the word could not be read: never guessed at
	exitStatus           // WIFEXITED: the status is the exit code
	killStatus           // WIFSIGNALED without a core: the status is the signal
	dumpStatus           // WIFSIGNALED with WCOREDUMP
)

// leaderExit is how the leader's exit happened, read off /proc's
// exit_code field while the zombie is held (readLeaderExitStatus). This is
// the leader's authoritative outcome, available WITHOUT reaping; the reap
// later reports the same value (#1474).
type leaderExit struct {
	kind   int
	status int
}

// describe renders the exit the way a reaped wait reports the same one
// ("exit status N", "signal: killed", "signal: aborted (core dumped)"),
// so the detail reads the same before and after the reap.
func (e leaderExit) describe() string {
	switch e.kind {
	case exitStatus:
		return fmt.Sprintf("exit status %d", e.status)
	case killStatus:
		return fmt.Sprintf("signal: %v", syscall.Signal(e.status))
	case dumpStatus:
		return fmt.Sprintf("signal: %v (core dumped)", syscall.Signal(e.status))
	default:
		return "exit (the leader's status could not be read)"
	}
}

// waitExit is the launch's waiter goroutine. On a hold-capable platform it
// waits the leader out WITHOUT reaping it: the leader stays a zombie, and
// the zombie holds the launch's pid — the group id — allocated, so nothing
// can reuse the group's number from the exit until the reap (#1474). It
// reaps at once when a scan finds no other member (the common exit:
// nothing left to keep the zombie for) and otherwise LEAVES the zombie
// held, recording the leader's real exit — read from /proc while the
// zombie is held — as the detail right away, with a drain watcher that
// reaps once the group empties on its own: a launch whose members outlive
// it must not need a stop to release the number. Every later probe or
// signal by number still goes out while the launch provably owns the
// number. The hold is taken by waitid WNOWAIT where the kernel allows it
// and — when a policy refuses waitid — by POLLING /proc for the zombie
// instead (waitHeldByPolling): either way the reap happens only under the
// stop mutex, so no Linux path ever frees the number while a stop may be
// mid-signal. Platforms without any hold (non-Linux Unix) reap at the
// exit — outside the stop mutex, so a live launch stays stoppable — and
// their stop aborts to unconfirmed at the first sight of the exit, the
// reuse residual documented in host_run's help; Windows runs this same
// waiter for its Done/ExitDetail semantics only.
func (a *execAgent) waitExit() {
	if leaderHoldCapable {
		if a.waitHeld() {
			return
		}
		a.waitHeldByPolling()
		return
	}
	a.waitReaping()
}

// waitHeld is the waitid hold: it returns once the leader is an UNREAPED
// zombie, or false when the hold could not be taken (a kernel or policy
// that refuses waitid) and the caller must fall back to the polling hold.
// EINTR interrupts the wait without proving the exit, so it retries.
func (a *execAgent) waitHeld() bool {
	for {
		err := waitLeaderExit(a.pid)
		if errors.Is(err, syscall.EINTR) {
			continue // interrupted without a status: the leader is neither exited nor reaped
		}
		if err != nil {
			return false // waitid is refused: the polling hold takes over
		}
		break
	}
	a.holdExit(readLeaderExitStatus(a.pid))
	return true
}

// waitHeldByPolling is the hold for a kernel or policy that refuses
// waitid: the zombie itself is the ownership proof, and /proc shows it,
// so the waiter polls for the leader's state to turn Z and then takes the
// same exit branch the waitid hold does. Nothing reaps the leader outside
// the stop mutex on this path either — the reap is reap(), under mu — so
// a stop's by-number signals stay serialized with the number's release
// exactly as with the waitid hold (#1474: the fallback this replaces reaped
// from a blocking Wait outside mu, which could free the number while a
// stop was between its probe and its signal, and the signal then reached
// whatever took the reused number). A leader that vanishes from /proc
// without ever showing Z (nothing but this process can reap it, so that
// is a pathological host) is treated as an exit whose status cannot be
// read, never as liveness. The reap itself needs a wait syscall; if the
// host refuses those too, waitOut fails, the zombie stays held — the
// number never becomes public, which is the safe direction — and the
// launch's records keep the unconfirmed posture.
func (a *execAgent) waitHeldByPolling() {
	t := time.NewTicker(drainWatchInterval)
	defer t.Stop()
	var gone bool
	for range t.C {
		state, err := pollLeaderState(a.pid)
		switch {
		case err == nil && state == 'Z':
			gone = true
		case errors.Is(err, syscall.ENOENT):
			gone = true // vanished without a zombie: an exit nobody can describe
		}
		if gone {
			break
		}
	}
	a.holdExit(readLeaderExitStatus(a.pid))
}

// holdExit is the shared tail of both holds: the leader has exited and is
// the unreaped zombie that keeps the group's number allocated. An empty
// group reaps at once (nothing to keep the zombie for); members keep it
// held, the leader's real exit becomes the detail NOW — a reader that
// sees Done closed gets the authoritative status even if no stop ever
// comes — and a drain watcher releases the number once the group empties
// on its own.
func (a *execAgent) holdExit(exit leaderExit) {
	a.exited.Store(true)
	a.mu.Lock()
	switch {
	case groupHasOtherMembers(a.pid, a.pid):
		// If a stop already reaped (it confirmed and released the number
		// first), its finalized detail stands.
		if !a.reaped.Load() {
			a.detail = exit.describe()
		}
	default:
		a.reap() // empty group: nothing to keep the number for
		a.emptyAtReap.Store(true)
	}
	a.mu.Unlock()
	if !a.reaped.Load() {
		a.startWatchDrain()
	}
	close(a.done)
}

// waitReaping is the waiter of the platforms with NO hold (non-Linux
// Unix): the leader is reaped at its exit because that is the only way to
// learn its status there. The reap's wait can block for the leader's
// WHOLE life, so it runs WITHOUT the stop mutex — a stop must still be
// able to terminate a live launch — which is exactly why the reap cannot
// be serialized with a stop's signals on these platforms: the number
// turns public at a moment no mutex governs. The stop therefore never
// signals by number once it has seen the exit (stopAuthorityGone, in
// terminateGroup): while the leader is live the number is bound to it and
// the probe oracle is a real one, and after the exit the stop reports
// unconfirmed and keeps records instead of reaching for a number the
// launch may no longer own. The instant between the stop's last
// exit-check and its signal — an exit landing inside it frees the number
// mid-signal — is these platforms' un-closable residual, documented in
// host_run's help: without a hold there is no syscall that both proves
// the number is bound and signals it. exited is stored only once the
// leader has actually exited, so the probe oracle is real for as long as
// the stop uses it.
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
// emptyAtReap, so a later stop confirms without touching the number. The
// scan itself runs WITHOUT mu: a scan is up to eight /proc listings, and
// running it under mu would hand every stop a mutex held for the whole
// scan on busy hosts (a stop deferred for the watcher's /proc churn); the
// re-check of reaped after re-taking mu is what keeps the reap decision
// sound, and an empty scan cannot be invalidated between the scan and the
// reap — a new member can only come from a member that was alive at the
// scan, and an empty scan saw none. It returns as soon as the zombie is
// reaped, by a stop or by itself; watcherDone lets a test wait for that.
func (a *execAgent) watchDrain() {
	defer close(a.watcherDone)
	t := time.NewTicker(drainWatchInterval)
	defer t.Stop()
	for range t.C {
		if a.reaped.Load() {
			return
		}
		if groupHasOtherMembers(a.pid, a.pid) {
			continue
		}
		a.mu.Lock()
		if a.reaped.Load() {
			a.mu.Unlock()
			return
		}
		a.reap()
		a.emptyAtReap.Store(true)
		a.mu.Unlock()
		return
	}
}

// startWatchDrain arms the drain watcher once, recording its done channel
// for tests that swap package vars (a watcher left running by an earlier
// test would read through the swap; #1474).
func (a *execAgent) startWatchDrain() {
	a.watcherDone = make(chan struct{})
	go a.watchDrain()
}

// waitWatchers blocks until the drain watcher (if one was armed) has
// exited, so a test may swap scan/waiter package vars without racing a
// watcher this agent left behind.
func (a *execAgent) waitWatchers() {
	if ch := a.watcherDone; ch != nil {
		<-ch
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
