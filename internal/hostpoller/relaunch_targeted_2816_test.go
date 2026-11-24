//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// hadron-cli#2816: C5's fresh reads about ONE worker — the guards under the
// lock, after the persisted claim and after a launch's proof, the cleanup's,
// the agentless end's, the exit pass's and the repair report's — use the
// targeted desired-worker read of hadron-server#4693
// (`hostDesiredState(appRef, hostId, worker)`) under the host key. The
// host-wide list stays what it was for: the discovery read that starts a sweep
// and names the workers to consider. The first guard of each worker uses that
// discovery snapshot, as before.
//
// A fresh read has three outcomes, and they stay apart all the way to the
// event (cor:agt:020:03, cor:agt:020:08; hadron-cli#2816 item 5):
//
//   - it names the worker: that row is the fresh state;
//   - it names NO row: this host key is not shown the worker as desired on
//     this host — NOT_DESIRED_ON_HOST, a definite answer. It is not an error,
//     and it says nothing about the worker anywhere else (another host's
//     worker, a retired and an unknown one all read the same);
//   - it FAILS: unknown — RELAUNCH_SOURCE_UNAVAILABLE (after a launch's proof:
//     RELAUNCH_UNVERIFIED and a rollback), carrying the read's cause. It is
//     never read as an empty answer, and nothing launches on it.
//
// The read is a read (cor:agt:070:04: the host key drives no session): a
// worker whose fresh row names no session is not relaunched, and nothing
// starts or binds a session for it; normal worker.start owns a new one. What
// the read asks for on the wire is pinned in internal/cmd/team.

// step2816 runs one check-in and waits for its relaunch job.
func step2816(t *testing.T, f *loopFixture2060) {
	t.Helper()
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
}

// eventsOf2816 collects the events of one kind the poller emits from now on.
func eventsOf2816(f *loopFixture2060, kind string) *[]Event {
	var events []Event
	f.p.emit = func(e Event) {
		if e.Kind == kind {
			events = append(events, e)
		}
	}
	return &events
}

// nothingTracked2816 fails if anything is tracked for the worker: no agent
// this host launched and no session.
func nothingTracked2816(t *testing.T, f *loopFixture2060) {
	t.Helper()
	f.p.mu.Lock()
	agent, session := f.p.agents["id-iris"], f.p.agentSessions["id-iris"]
	f.p.mu.Unlock()
	if agent != nil || session != "" {
		t.Fatalf("the freshness check left an agent or a session behind: agent=%v session=%q", agent, session)
	}
}

// One relaunched worker costs two host-wide discovery reads (one per sweep)
// and four per-worker reads: under the lock of each sweep, after the persisted
// claim, and after the launch's process proof. Every one names the worker.
func TestFreshReadsAfterDiscoveryNameOneWorkerNotTheWholeHost2816(t *testing.T) {
	f := newLoopFixture2060(t)
	step2816(t, f) // the first absence observation
	if s, w := f.source.snapshotReads, f.source.workerReads; s != 1 || w != 1 {
		t.Fatalf("first sweep: host-wide reads=%d want 1, per-worker reads=%d want 1", s, w)
	}
	step2816(t, f) // the launch
	if len(f.launches) != 1 {
		t.Fatalf("launches=%d", len(f.launches))
	}
	if s, w := f.source.snapshotReads, f.source.workerReads; s != 2 || w != 4 {
		t.Fatalf("host-wide reads=%d want 2 (one discovery per sweep); per-worker reads=%d want 4", s, w)
	}
	for i, ask := range f.source.workerAsks {
		if ask != [2]string{"h1", "id-iris"} {
			t.Fatalf("per-worker read %d asked for %v, want the host and the worker", i+1, ask)
		}
	}
}

// freshReadCase2816 is one way a per-worker read answers in one guard of a
// relaunch. read is the per-worker read (1-based) that answers that way; step
// is the check-in (1-based) whose logged result says so. Four reads belong to
// one relaunch: under the lock of the first sweep, under the lock of the
// launching sweep, after the persisted claim, after the launch's proof.
type freshReadCase2816 struct {
	name, outcome, reason string
	read, step            int
	attempts, launched    int
}

// runFreshRead2816 scripts the per-worker read `c.read` to answer as `answer`
// says, runs the two check-ins a relaunch needs, and checks the result.
func runFreshRead2816(t *testing.T, c freshReadCase2816, answer func(f *loopFixture2060)) Event {
	t.Helper()
	f := newLoopFixture2060(t)
	events := eventsOf2816(f, "relaunch")
	answer(f)
	step2816(t, f)
	step2816(t, f)
	if len(*events) != 2 {
		t.Fatalf("the two check-ins logged %d results, want one each: %+v", len(*events), *events)
	}
	got := (*events)[c.step-1]
	if got.Outcome != c.outcome || got.Reason != c.reason || got.Detail != c.reason {
		t.Errorf("logged %s/%s (detail %q), want %s/%s", got.Outcome, got.Reason, got.Detail, c.outcome, c.reason)
	}
	if got.Attempt != c.attempts || len(f.journal["id-iris"].Attempts) != c.attempts {
		t.Errorf("attempt=%d journal=%+v, want %d spent", got.Attempt, f.journal["id-iris"].Attempts, c.attempts)
	}
	if len(f.launches) != c.launched || (f.agent != nil) != (c.launched == 1) {
		t.Fatalf("launches=%d, want %d", len(f.launches), c.launched)
	}
	if c.launched == 1 {
		// What was launched on the strength of a target no longer proven is
		// rolled back, and is not left tracked as the worker's agent.
		if f.agent.stops != 1 {
			t.Errorf("the launch was rolled back %d times, want once", f.agent.stops)
		}
		nothingTracked2816(t, f)
	}
	return got
}

// A failed fresh read is unknown. At no guard does it launch anything, and it
// is never read as an empty answer.
func TestAFailedFreshReadIsUnknownAndNeverLaunches2816(t *testing.T) {
	const unknown = "RELAUNCH_SOURCE_UNAVAILABLE"
	for _, c := range []freshReadCase2816{
		{"under the lock of the first sweep", "skipped", unknown, 1, 1, 0, 0},
		{"under the lock of the launching sweep", "skipped", unknown, 2, 2, 0, 0},
		{"after the persisted claim: the attempt is spent and nothing launches", "skipped", unknown, 3, 2, 1, 0},
		{"after the launch's proof: the launch is rolled back", "failed", hostrelaunch.VerifyFailed, 4, 2, 1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			const cause = "the host key's read failed"
			got := runFreshRead2816(t, c, func(f *loopFixture2060) {
				f.source.workerFailAt, f.source.workerErr = c.read, errors.New(cause)
			})
			if !strings.Contains(got.LocalCause, cause) {
				t.Errorf("the unknown result lost the read's cause: local cause %q", got.LocalCause)
			}
		})
	}
}

// An empty fresh read is NOT unknown: this host key is not shown the worker as
// desired on this host, and the sweep says so. It is a definite answer, so it
// carries no cause, and it launches nothing either.
func TestAnEmptyFreshReadSaysNotDesiredOnThisHostNotUnknownAndLaunchesNothing2816(t *testing.T) {
	const notDesired = "NOT_DESIRED_ON_HOST"
	for _, c := range []freshReadCase2816{
		{"under the lock of the first sweep", "skipped", notDesired, 1, 1, 0, 0},
		{"under the lock of the launching sweep", "skipped", notDesired, 2, 2, 0, 0},
		{"after the persisted claim: the attempt is spent and nothing launches", "skipped", notDesired, 3, 2, 1, 0},
		{"after the launch's proof: the launch is rolled back", "failed", hostrelaunch.VerifyFailed, 4, 2, 1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := runFreshRead2816(t, c, func(f *loopFixture2060) { emptyFrom2816(f, c.read) })
			if c.read != 4 && got.LocalCause != "" {
				t.Errorf("a definite answer carries a cause: %q", got.LocalCause)
			}
		})
	}
	// The two outcomes are different, and only the failure is the unknown one.
	const at = 2
	failed := freshReadCase2816{"a failed read under the lock of the launching sweep", "skipped", "RELAUNCH_SOURCE_UNAVAILABLE", at, at, 0, 0}
	empty := freshReadCase2816{"an empty read under the lock of the launching sweep", "skipped", notDesired, at, at, 0, 0}
	unknown := runFreshRead2816(t, failed, func(f *loopFixture2060) {
		f.source.workerFailAt, f.source.workerErr = at, errors.New("the host key's read failed")
	})
	answer := runFreshRead2816(t, empty, func(f *loopFixture2060) { emptyFrom2816(f, at) })
	if unknown.Reason == answer.Reason || unknown.LocalCause == "" || answer.LocalCause != "" {
		t.Fatalf("an empty answer and a failed read must differ: empty=%s/%q failed=%s/%q", answer.Reason, answer.LocalCause, unknown.Reason, unknown.LocalCause)
	}
}

// emptyFrom2816 scripts the per-worker read `from` and every one after it to
// name no row, keeping the App, host, server and sweep mode of the answer.
func emptyFrom2816(f *loopFixture2060, from int) {
	snap := f.source.snapshot
	snap.Workers = []RelaunchWorker{}
	f.source.workerNth = func(read int) RelaunchSnapshot {
		if read < from {
			return narrow2816(f.source.snapshot, "id-iris")
		}
		return snap
	}
}

// A fresh read that succeeds with no session keeps today's contract: the
// relaunch target is the open session, so a row without one is a different
// target and nothing launches. The worker is left to normal worker.start,
// which owns a new session; this path starts or binds none (cor:agt:070:04).
//
// The behaviour is today's; what changes is the read that sees the row. On
// main the first per-worker read is never made and the worker is relaunched.
func TestAFreshReadWithNoOpenSessionRelaunchesNothing2816(t *testing.T) {
	withoutSession := func(snap RelaunchSnapshot) RelaunchSnapshot {
		snap.Workers = append([]RelaunchWorker(nil), snap.Workers...)
		snap.Workers[0].SessionID = ""
		return snap
	}
	t.Run("the discovery row names no session", func(t *testing.T) {
		f := newLoopFixture2060(t)
		f.source.snapshot = withoutSession(f.source.snapshot)
		step2816(t, f)
		step2816(t, f)
		_, _, skip, ok := f.p.relaunchRunner(f.p.relaunch, f.source.snapshot, f.source.snapshot.Workers[0], f.journal, 1, nil)
		if ok || skip != "NO_OPEN_SESSION" {
			t.Fatalf("a worker without an open session: ok=%v skip=%q, want NO_OPEN_SESSION", ok, skip)
		}
		if f.source.workerReads != 0 || len(f.launches) != 0 || len(f.journal) != 0 {
			t.Fatalf("launches=%d per-worker reads=%d journal=%+v; a worker without an open session is not looked at again", len(f.launches), f.source.workerReads, f.journal)
		}
		nothingTracked2816(t, f)
	})
	t.Run("the fresh read under the lock names no session", func(t *testing.T) {
		f := newLoopFixture2060(t)
		events := eventsOf2816(f, "relaunch")
		// The first per-worker read (the first sweep's, under its lock) still
		// names the session; the launching sweep's does not.
		f.source.workerNth = func(read int) RelaunchSnapshot {
			if read == 1 {
				return narrow2816(f.source.snapshot, "id-iris")
			}
			return withoutSession(narrow2816(f.source.snapshot, "id-iris"))
		}
		step2816(t, f) // the first absence observation
		step2816(t, f) // the launch
		if len(*events) != 2 {
			t.Fatalf("the two check-ins logged %d results, want one each: %+v", len(*events), *events)
		}
		first, got := (*events)[0], (*events)[1]
		if first.Outcome != "skipped" || first.Reason != hostrelaunch.Pending {
			t.Errorf("the first sweep logged %s/%s, want the absence observation %s/%s", first.Outcome, first.Reason, "skipped", hostrelaunch.Pending)
		}
		if got.Outcome != "skipped" || got.Reason != hostrelaunch.IdentityChanged {
			t.Errorf("logged %s/%s, want skipped/%s: a row without the session is not the target", got.Outcome, got.Reason, hostrelaunch.IdentityChanged)
		}
		if len(f.launches) != 0 || len(f.journal["id-iris"].Attempts) != 0 {
			t.Errorf("launches=%d attempts=%d, want none", len(f.launches), len(f.journal["id-iris"].Attempts))
		}
		if s, w := f.source.snapshotReads, f.source.workerReads; s != 2 || w != 2 {
			t.Errorf("host-wide reads=%d want 2 (one discovery per sweep); per-worker reads=%d want 2 (one under each sweep's lock)", s, w)
		}
		nothingTracked2816(t, f)
	})
}

// The agentless end's fresh read names its one worker. An unknown read and an
// empty one both end nothing (STATE_CHANGED is the sweep's line for both; a
// pass that only skips records no run, so the event is where it is read).
func TestAnAgentlessEndReReadsItsOneWorker2816(t *testing.T) {
	for _, c := range []struct {
		name, outcome, reason string
		ends, failAt          int
		empty                 bool
	}{
		{"the same worker, session and worktree", "APPLIED", AgentAbsent, 1, 0, false},
		{"the fresh read failed", "SKIPPED", agentlessStateChanged, 0, 2, false},
		{"the fresh read named no row", "SKIPPED", agentlessStateChanged, 0, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newAgentlessFixture(t, stoppedRow("still-open"))
			events := eventsOf2816(f.loopFixture2060, "agentless-end")
			f.src.failAt = c.failAt
			if c.empty {
				first := f.src.nth(1)
				f.src.nth = func(read int) RelaunchSnapshot {
					if read == 1 {
						return first
					}
					next := first
					next.Workers = []RelaunchWorker{}
					return next
				}
			}
			f.sweep(t)
			if len(*events) != 1 || (*events)[0].Outcome != c.outcome || (*events)[0].Reason != c.reason {
				t.Errorf("agentless-end events=%+v, want one %s/%s", *events, c.outcome, c.reason)
			}
			if len(f.ends) != c.ends {
				t.Errorf("ends=%+v, want %d", f.ends, c.ends)
			}
			if c.outcome == "APPLIED" {
				if lines := f.lines(); len(lines) != 1 || lines[0] != (SweepReportLine{"id-iris", c.outcome, c.reason}) {
					t.Errorf("lines=%+v, want one APPLIED/%s line", lines, c.reason)
				}
			}
			f.src.mu.Lock()
			wide, one := f.src.snapshotReads, f.src.workerReads
			f.src.mu.Unlock()
			if wide != 1 || one != 1 {
				t.Errorf("host-wide reads=%d want 1 (the discovery); per-worker reads=%d want 1 (the end's fresh read)", wide, one)
			}
		})
	}
}

// The cleanup after a relaunch re-reads its one worker too, and cleans nothing
// on a read that is unknown or empty.
func TestCleanupReReadsItsOneWorker2816(t *testing.T) {
	run := func(t *testing.T, script func(f *loopFixture2060)) (calls int, receipts []Event) {
		t.Helper()
		f := newLoopFixture2060(t)
		f.p.relaunch.Cleanup = func(context.Context, Agent, hostrelaunch.Target) { calls++ }
		receiptsOf := eventsOf2816(f, "relaunch-terminal-cleanup")
		script(f)
		step2816(t, f)
		step2816(t, f)
		receipts = *receiptsOf
		if len(f.launches) != 1 {
			t.Fatalf("launches=%d, want the relaunch the cleanup follows", len(f.launches))
		}
		if s, w := f.source.snapshotReads, f.source.workerReads; s != 2 || w != 5 {
			t.Errorf("host-wide reads=%d want 2 (one discovery per sweep); per-worker reads=%d want 5 (the relaunch's four and the cleanup's)", s, w)
		}
		return calls, receipts
	}
	t.Run("the worker is still the target", func(t *testing.T) {
		calls, receipts := run(t, func(*loopFixture2060) {})
		if calls != 1 || len(receipts) != 0 {
			t.Fatalf("cleanup calls=%d receipts=%+v, want one cleanup and no receipt", calls, receipts)
		}
	})
	for _, c := range []struct {
		name  string
		empty bool
	}{
		{"the cleanup's read failed", false},
		{"the cleanup's read named no row", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls, receipts := run(t, func(f *loopFixture2060) {
				if c.empty {
					emptyFrom2816(f, 5)
					return
				}
				f.source.workerFailAt, f.source.workerErr = 5, errors.New("the host key's read failed")
			})
			if calls != 0 {
				t.Errorf("cleanup calls=%d on a read that proves nothing", calls)
			}
			if len(receipts) != 1 || receipts[0].Outcome != "skipped" || receipts[0].Reason != "CLEANUP_TARGET_UNPROVEN" {
				t.Errorf("receipts=%+v, want one skipped/CLEANUP_TARGET_UNPROVEN", receipts)
			}
		})
	}
}

// An exit pass (cli#2161) reads its one worker — the host-wide list is not
// asked for at all — and an unknown or empty read launches nothing, leaving
// the worker to the ordinary two-sweep path.
func TestAnExitPassReadsOnlyTheExitedWorker2816(t *testing.T) {
	t.Run("a relaunch", func(t *testing.T) {
		f := newExitFixture2161(t)
		f.run(t)
		f.writeStamp(t, "id-iris", "still-open", time.Second)
		close(f.old.done)
		f.awaitPass(t)
		if n := f.launchCount(); n != 1 {
			t.Fatalf("launched %d", n)
		}
		if s, w := f.source.snapshotReads, f.source.workerReads; s != 1 || w != 4 {
			t.Fatalf("host-wide reads=%d want 1 (the first sweep's discovery); per-worker reads=%d want 4 (the exit pass's own, then under the lock, after the persisted claim, after the launch's proof)", s, w)
		}
		for i, ask := range f.source.workerAsks {
			if ask != [2]string{"h1", "id-iris"} {
				t.Fatalf("per-worker read %d asked for %v, want the host and the exited worker", i+1, ask)
			}
		}
	})
	for _, c := range []struct {
		name  string
		empty bool
	}{
		{"the exit pass's read failed", false},
		{"the exit pass's read named no row", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newExitFixture2161(t)
			f.run(t)
			// Collect from here, after the first sweep's job is done: its own
			// relaunch event (the live tracked agent reads
			// RELAUNCH_AGENT_PRESENT) is the sweep's line, not the exit pass's.
			var events []Event
			logged := f.p.emit
			f.p.emit = func(e Event) { events = append(events, e); logged(e) }
			if c.empty {
				emptyFrom2816(f.loopFixture2060, 1)
			} else {
				f.source.workerFailAt, f.source.workerErr = 1, errors.New("the host key's read failed")
			}
			f.writeStamp(t, "id-iris", "still-open", time.Second)
			close(f.old.done)
			f.awaitPass(t)
			if n := f.launchCount(); n != 0 || len(f.launches) != 0 {
				t.Fatalf("launched %d", n)
			}
			var unproven []Event
			for _, e := range events {
				if e.Kind == "relaunch" || e.Kind == "relaunch-unproven" {
					unproven = append(unproven, e)
				}
			}
			if c.empty {
				if len(unproven) != 0 {
					t.Fatalf("the exit pass logged %+v; a worker the platform does not show as desired here is nothing to relaunch", unproven)
				}
			} else if len(unproven) != 1 || unproven[0].Kind != "relaunch-unproven" || unproven[0].Outcome != "skipped" || unproven[0].Detail != hostrelaunch.Unproven {
				// p.log carries the code in Detail; the relaunch kind's own
				// logger is what fills Reason as well.
				t.Fatalf("the exit pass logged %+v, want one skipped/%s", unproven, hostrelaunch.Unproven)
			}
			if f.source.workerReads != 1 || f.source.snapshotReads != 1 {
				t.Fatalf("host-wide reads=%d per-worker reads=%d, want 1 and 1", f.source.snapshotReads, f.source.workerReads)
			}
		})
	}
}
