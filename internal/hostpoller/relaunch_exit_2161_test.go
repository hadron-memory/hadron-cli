//go:build linux

package hostpoller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	passes   chan string // exit passes that finished, by worker id
	mu       sync.Mutex
	reasons  []string // relaunch results the Runner logged
}

// awaitPass waits for one exit pass for the worker to finish.
func (f *exitFixture2161) awaitPass(t *testing.T) {
	t.Helper()
	select {
	case id := <-f.passes:
		if id != "id-iris" {
			t.Fatalf("an exit pass for %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no exit pass ran")
	}
}

func (f *exitFixture2161) loggedReasons() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reasons...)
}

func newExitFixture2161(t *testing.T) *exitFixture2161 {
	t.Helper()
	f := &exitFixture2161{loopFixture2060: newLoopFixture2060(t)}
	if err := f.p.state.setHostID("h1"); err != nil {
		t.Fatal(err)
	}
	tree := f.p.startCfg.WorktreePath("repo/iris")
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
	f.passes = make(chan string, 8)
	f.p.afterExitPass = func(id string) { f.passes <- id }
	f.p.emit = func(e Event) {
		if e.Kind == "relaunch" {
			f.mu.Lock()
			f.reasons = append(f.reasons, e.Reason)
			f.mu.Unlock()
		}
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
	// ...and its job has finished, so nothing it logs lands later.
	f.awaitIdle(t)
}

// awaitIdle waits, bounded, until no relaunch job holds the lock.
func (f *exitFixture2161) awaitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f.p.relaunchMu.TryLock() {
		if time.Now().After(deadline) {
			t.Fatal("a relaunch job never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.p.relaunchMu.Unlock()
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
	stamp := func(worker, session string, age time.Duration) func(f *exitFixture2161, t *testing.T) {
		return func(f *exitFixture2161, t *testing.T) { f.writeStamp(t, worker, session, age) }
	}
	fresh := stamp("id-iris", "still-open", time.Second)
	for name, c := range map[string]struct {
		stamp  func(f *exitFixture2161, t *testing.T)
		mutate func(f *exitFixture2161)
		// want is the reason the Runner logged; "" for a pass that ends before
		// the Runner (an ordinary exit, with no stamp, costs no network call).
		want string
	}{
		"no stamp (an ordinary exit)":    {},
		"another session's stamp":        {stamp: stamp("id-iris", "other-session", time.Second), want: hostrelaunch.NoExitProof},
		"another worker's stamp":         {stamp: stamp("id-other", "still-open", time.Second), want: hostrelaunch.NoExitProof},
		"a stale stamp":                  {stamp: stamp("id-iris", "still-open", 5*time.Minute), want: hostrelaunch.NoExitProof},
		"a paused worker":                {stamp: fresh, mutate: func(f *exitFixture2161) { f.source.snapshot.Workers[0].Reason = "PAUSED" }, want: "PAUSED"},
		"a worker C5 would not relaunch": {stamp: fresh, mutate: func(f *exitFixture2161) { f.source.snapshot.Workers[0].Desired = "STOPPED" }, want: "NOT_SHOULD_RUN"},
		"the cap is spent": {stamp: fresh, want: hostrelaunch.RateLimited, mutate: func(f *exitFixture2161) {
			now := f.p.now().UTC()
			f.journal["id-iris"] = hostrelaunch.Record{Attempts: []time.Time{now.Add(-3 * time.Minute), now.Add(-2 * time.Minute), now.Add(-time.Minute)}}
		}},
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
			before := len(f.loggedReasons())
			close(f.old.done)
			f.awaitPass(t)
			if n := f.launchCount(); n != 0 {
				t.Fatalf("%s: launched %d", name, n)
			}
			got := f.loggedReasons()[before:]
			if c.want == "" && len(got) != 0 || c.want != "" && (len(got) != 1 || got[0] != c.want) {
				t.Fatalf("%s: the exit pass logged %v, want %q", name, got, c.want)
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
	f.awaitPass(t)
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
	f.awaitIdle(t)
	if !f.p.relaunchMu.TryLock() {
		t.Fatal("the lock was taken again")
	}
	hold := &holdSource2161{inner: f.p.relaunch.Source, entered: make(chan struct{}), release: make(chan struct{})}
	f.p.relaunch.Source = hold
	f.p.relaunchMu.Unlock()
	var released atomic.Bool
	release := func() {
		if released.CompareAndSwap(false, true) {
			close(hold.release)
		}
	}
	t.Cleanup(release)                     // a failing test never leaves the job blocked
	f.p.relaunchStep(context.Background()) // a sweep job, now blocked in its source read
	select {
	case <-hold.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep job never reached its source read")
	}
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
	release()
	f.waitFor(t, func() bool { return f.launched.Load() == 1 }, "the exit to be relaunched once the busy job ended")
}

// Jibril's P3: an exit wake runs the exit pass and then sleeps on to the SAME
// deadline; it never brings the next check-in forward.
func TestAnExitWakeKeepsTheCheckInDeadline2161(t *testing.T) {
	f := newExitFixture2161(t)
	var asked []time.Duration
	f.p.sleep = func(ctx context.Context, d time.Duration) error {
		asked = append(asked, d)
		if len(asked) == 1 {
			<-ctx.Done() // woken by the exit
			return ctx.Err()
		}
		return nil // the rest of the interval
	}
	ran := make(chan struct{}, 1)
	f.p.afterRelaunchJob = func() { ran <- struct{}{} }
	f.p.wakeForExit()
	const interval = 30 * time.Second
	if err := f.p.pause(context.Background(), interval); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the wake ran no exit step")
	}
	if len(asked) != 2 || asked[0] > interval || asked[1] >= asked[0] || asked[1] <= 0 {
		t.Fatalf("sleeps asked %v: the second must be what is left of the first", asked)
	}
}

// C1 x C12 (#2056 merged into #2167, Wolfgang #110164): an exit pass records
// C1's S10 recovery row for the one worker it relaunched (never unrecorded),
// and a refused exit pass records nothing (never an empty pass).
func TestAnExitRelaunchIsRecordedAsOneS10RowAndARefusedOneIsNot2161(t *testing.T) {
	for _, c := range []struct {
		name   string
		paused bool
		want   int
	}{{"relaunched", false, 1}, {"refused", true, 0}} {
		t.Run(c.name, func(t *testing.T) {
			f := newExitFixture2161(t)
			var mu sync.Mutex
			var reports []SweepReport
			f.p.relaunch.ReportSweep = func(_ context.Context, r SweepReport) error {
				mu.Lock()
				reports = append(reports, r)
				mu.Unlock()
				return nil
			}
			if c.paused {
				f.source.snapshot.Workers[0].Reason = "PAUSED"
			}
			f.run(t)
			mu.Lock()
			reports = nil // the first sweep's own report, if any, is C1's
			mu.Unlock()
			f.writeStamp(t, "id-iris", "still-open", time.Second)
			close(f.old.done)
			f.awaitPass(t)
			mu.Lock()
			defer mu.Unlock()
			if len(reports) != c.want {
				t.Fatalf("%s: %d S10 reports, want %d: %+v", c.name, len(reports), c.want, reports)
			}
			if c.want == 1 {
				r := reports[0]
				if r.Applied != 1 || r.Failed != 0 || len(r.Lines) != 1 || r.Lines[0] != (SweepReportLine{"id-iris", "APPLIED", "TERMINAL_MISSING"}) || !strings.HasPrefix(r.RunID, "recovery:") || r.Mode != "APPLY" || r.AppID != "app-id" || r.HostID != "h1" {
					t.Fatalf("report %+v", r)
				}
			}
		})
	}
}
