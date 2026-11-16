//go:build linux

package hostpoller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// cli#2161 (C12): when an agent this poller launched exits after a context
// reset, the poller relaunches it at once (no second sweep), through C5's own
// Runner. The stamp is worker-written: it can only shorten the wait for a
// relaunch C5 would make anyway (Wolfgang #109662, Rook's §10 check).

type exitFixture2161 struct {
	*loopFixture2060
	old      *loopAgent2060
	stamp    string
	done     chan error
	stop     context.CancelFunc
	launched atomic.Int32  // creates, counted race-free across the pass goroutine
	sweeps   atomic.Uint64 // generations the sweep journal claimed
	newAgent atomic.Pointer[loopAgent2060]
}

func newExitFixture2161(t *testing.T) *exitFixture2161 {
	t.Helper()
	f := &exitFixture2161{loopFixture2060: newLoopFixture2060(t)}
	if err := f.p.state.setHostID("h1"); err != nil {
		t.Fatal(err)
	}
	tree := f.p.cfg.WorktreePath("repo/iris")
	if err := os.Mkdir(filepath.Join(tree, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.stamp = filepath.Join(tree, ".git", delivery.RelaunchStampFile)
	// An exit pass observes one worker, not a sweep: it opens the journal
	// without claiming a generation.
	f.p.relaunch.OpenJournal = func() (hostrelaunch.Journal, uint64, func(), error) {
		return f.journal, f.sweeps.Add(1), func() {}, nil
	}
	f.p.relaunch.OpenExitJournal = func() (hostrelaunch.Journal, uint64, func(), error) {
		return f.journal, f.sweeps.Load(), func() {}, nil
	}
	launch := f.p.orcaLaunch
	f.p.orcaLaunch = func(ctx context.Context, s OrcaLaunchSpec) (Agent, error) {
		a, err := launch(ctx, s)
		f.newAgent.Store(f.agent)
		f.launched.Add(1)
		return a, err
	}
	f.old = &loopAgent2060{done: make(chan struct{})}
	f.p.trackAgent("id-iris", "repo/iris", f.old)
	f.p.mu.Lock()
	f.p.agentSessions["id-iris"] = "still-open"
	f.p.mu.Unlock()
	return f
}

func (f *exitFixture2161) writeStamp(t *testing.T, worker, session string, age time.Duration) {
	t.Helper()
	b, err := json.Marshal(delivery.RelaunchStamp{Version: 1, WorkerID: worker, SessionID: session, Checkpoint: checkpoint2161, RequestedAt: f.p.now().Add(-age)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.stamp, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

const checkpoint2161 = "hrn:node:acme.com:iris:checkpoints:2026-10-14-120000"

// run starts the poller with an interval sleep that never ends by itself, so
// a second sweep can only come from the exit, never from the clock.
func (f *exitFixture2161) run(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	f.stop = cancel
	f.p.sleep = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
	f.done = make(chan error, 1)
	go func() { f.done <- f.p.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-f.done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	f.waitFor(t, func() bool { return f.sweeps.Load() == 1 }, "the first sweep")
}

func (f *exitFixture2161) waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.p.mu.Lock()
		ok := cond()
		f.p.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (f *exitFixture2161) launchCount() int { return int(f.launched.Load()) }

func TestAContextResetExitRelaunchesAtOnceWithoutASecondSweep2161(t *testing.T) {
	f := newExitFixture2161(t)
	f.run(t)
	if f.launchCount() != 0 {
		t.Fatal("a running agent was relaunched")
	}
	f.writeStamp(t, "id-iris", "still-open", 5*time.Second)
	start := time.Now()
	close(f.old.done) // the old agent's pidfd fires
	f.waitFor(t, func() bool { return f.launched.Load() == 1 }, "the relaunch")
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the relaunch took %v", took)
	}
	if g := f.sweeps.Load(); g != 1 {
		t.Fatalf("the exit pass claimed a sweep generation: %d", g)
	}
	f.waitFor(t, func() bool {
		a := f.newAgent.Load()
		return a != nil && f.p.agents["id-iris"] == Agent(a) && f.p.agentSessions["id-iris"] == "still-open"
	}, "the new agent to be tracked")
	f.stop()
	if err := <-f.done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	f.done <- nil // for the cleanup
	if _, err := os.Lstat(f.stamp); !os.IsNotExist(err) {
		t.Fatalf("the stamp was not consumed: %v", err)
	}
	// `session recover` does not read checkpoints: the first prompt names it
	// (found while answering Jibril's claim P3).
	if p := f.launches[0].Prompt; !strings.Contains(p, "session recover") || !strings.Contains(p, "hadron node get "+checkpoint2161) {
		t.Fatalf("the relaunch prompt does not point at the checkpoint: %q", p)
	}
	if rec := f.journal["id-iris"]; len(rec.Attempts) != 1 {
		t.Fatalf("the relaunch was not charged to the cap: %+v", rec)
	}
}

// Every way the exit proof can fail leaves the worker to the ordinary
// two-sweep path: nothing launches at the exit.
func TestAnExitWithoutAMatchingFreshStampLaunchesNothing2161(t *testing.T) {
	for name, c := range map[string]struct {
		stamp  func(f *exitFixture2161, t *testing.T)
		mutate func(f *exitFixture2161)
	}{
		"no stamp (an ordinary exit)": {},
		"another session's stamp":     {stamp: func(f *exitFixture2161, t *testing.T) { f.writeStamp(t, "id-iris", "other-session", time.Second) }},
		"another worker's stamp":      {stamp: func(f *exitFixture2161, t *testing.T) { f.writeStamp(t, "id-other", "still-open", time.Second) }},
		"a stale stamp":               {stamp: func(f *exitFixture2161, t *testing.T) { f.writeStamp(t, "id-iris", "still-open", 5*time.Minute) }},
		"a paused worker": {
			stamp:  func(f *exitFixture2161, t *testing.T) { f.writeStamp(t, "id-iris", "still-open", time.Second) },
			mutate: func(f *exitFixture2161) { f.source.snapshot.Workers[0].Reason = "PAUSED" },
		},
		"a worker C5 would not relaunch": {
			stamp:  func(f *exitFixture2161, t *testing.T) { f.writeStamp(t, "id-iris", "still-open", time.Second) },
			mutate: func(f *exitFixture2161) { f.source.snapshot.Workers[0].Desired = "STOPPED" },
		},
		"the cap is spent": {
			stamp: func(f *exitFixture2161, t *testing.T) { f.writeStamp(t, "id-iris", "still-open", time.Second) },
			mutate: func(f *exitFixture2161) {
				now := f.p.now().UTC()
				f.journal["id-iris"] = hostrelaunch.Record{Attempts: []time.Time{now.Add(-3 * time.Minute), now.Add(-2 * time.Minute), now.Add(-time.Minute)}}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newExitFixture2161(t)
			// Before the poller runs: nothing else touches the fixture yet.
			if c.mutate != nil {
				c.mutate(f)
			}
			f.run(t)
			if c.stamp != nil {
				c.stamp(f, t)
			}
			close(f.old.done)
			time.Sleep(500 * time.Millisecond)
			if n := f.launchCount(); n != 0 {
				t.Fatalf("%s: launched %d", name, n)
			}
		})
	}
}

// The proof is the exit of the agent THIS poller tracks: a stamp beside a
// worker whose tracked agent is still running launches nothing, and neither
// does the exit of an agent that a later launch already replaced.
func TestAStampAloneNeverLaunches2161(t *testing.T) {
	f := newExitFixture2161(t)
	f.run(t)
	f.writeStamp(t, "id-iris", "still-open", time.Second)
	replaced := &loopAgent2060{done: make(chan struct{})}
	f.p.trackAgent("id-iris", "repo/iris", replaced)
	close(f.old.done) // the old one's exit is no longer this worker's agent
	time.Sleep(500 * time.Millisecond)
	if n := f.launchCount(); n != 0 {
		t.Fatalf("launched %d while the tracked agent runs", n)
	}
}

// holdSource blocks a relaunch job's source read until released.
type holdSource2161 struct {
	inner   RelaunchSource
	entered chan struct{}
	release chan struct{}
	once    atomic.Bool
}

func (h *holdSource2161) RelaunchSnapshot(ctx context.Context, host string) (RelaunchSnapshot, error) {
	if h.once.CompareAndSwap(false, true) {
		close(h.entered)
		<-h.release
	}
	return h.inner.RelaunchSnapshot(ctx, host)
}

// Jibril's and Julius's P2: an exit while another relaunch job holds the
// lock. The run loop takes the wake, its exit step finds the lock busy, and
// the exit must be woken again once that job ends, not left for the next
// check-in (which never comes here: the interval sleep never ends).
func TestAnExitDuringABusyRelaunchJobIsWokenAgain2161(t *testing.T) {
	f := newExitFixture2161(t)
	f.run(t)
	// The first sweep's job has released the lock before the source is
	// swapped (the mutex orders the write before the next job's read).
	for !f.p.relaunchMu.TryLock() {
		time.Sleep(5 * time.Millisecond)
	}
	hold := &holdSource2161{inner: f.p.relaunch.Source, entered: make(chan struct{}), release: make(chan struct{})}
	f.p.relaunch.Source = hold
	f.p.relaunchMu.Unlock()
	f.p.relaunchStep(context.Background()) // a sweep job, now blocked in its source read
	<-hold.entered
	f.writeStamp(t, "id-iris", "still-open", time.Second)
	close(f.old.done)
	// The wake is taken by the run loop, whose exit step finds the lock busy.
	deadline := time.Now().Add(5 * time.Second)
	for len(f.p.exitWake) > 0 || !f.p.exitsPending() {
		if time.Now().After(deadline) {
			t.Fatal("the exit was never recorded and its wake never taken")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // the woken exit step runs and returns on the busy lock
	if f.launchCount() != 0 {
		t.Fatal("launched while the other job held the lock")
	}
	close(hold.release)
	f.waitFor(t, func() bool { return f.launched.Load() == 1 }, "the exit to be relaunched once the busy job ended")
}
