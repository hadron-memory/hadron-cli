//go:build linux

package hostpoller

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// cli#2853, the stop's last scan. The first fold (a 1s wall-clock deadline) could not confirm a stop
// under churn that outlasts it (QA at 839afece: the held-exit test failed 3 of 3 under 24 spawners, as
// on main). Within the ruling's form (scan until a wall-clock deadline, else report unconfirmed with its
// code) the deadline's value is the builder's: the STOP gets a longer final scan (groupStopScanDeadline,
// about 5s) while the watcher's periodic probes keep the short one, so a stop under saturation waits for
// a clean pass instead of failing fast.

// With the package deadline a scan gives up where a longer one still finds the clean pass: 300 void
// passes (3s of modelled time) then a clean one. (Red while groupMembershipWithin ignores its deadline.)
func TestAScanWithALongerDeadlineSettlesWhereTheShortOneGivesUp2853(t *testing.T) {
	const voidPasses = 300
	f := churnHost(3)
	f.install(t)
	withFreshVanisher(f, voidPasses)
	withScanClock(t, 10*time.Millisecond, time.Second)
	if m, why := groupMembershipWith(churnPgid, churnPgid, func(d string) ([]os.DirEntry, error) { return readProcDir(d) }); !m || why != whyUnseenPid {
		t.Fatalf("the package deadline (1s) gives up inside a 3s run of void passes: %v/%s after %d listings", m, why, f.lists)
	}
	f.lists, f.pass = 0, 0
	if m, why := groupMembershipWithin(churnPgid, churnPgid, func(d string) ([]os.DirEntry, error) { return readProcDir(d) }, 5*time.Second); m {
		t.Fatalf("a 5s deadline reaches the clean pass at listing %d: still members (%s) after %d listings", voidPasses+1, why, f.lists)
	}
	if f.lists != voidPasses+1 {
		t.Fatalf("settles on the first clean pass: %d listings, want %d", f.lists, voidPasses+1)
	}
}

// The longer deadline is still a deadline: endless churn ends at it, as members with the void pass's code.
func TestTheLongerDeadlineStillEndsAtItsOwnBound2853(t *testing.T) {
	f := churnHost(3)
	f.install(t)
	withFreshVanisher(f, 1<<30)
	elapsed := withScanClock(t, 10*time.Millisecond, time.Second)
	m, why := groupMembershipWithin(churnPgid, churnPgid, func(d string) ([]os.DirEntry, error) { return readProcDir(d) }, 4*time.Second)
	if !m || why != whyUnseenPid {
		t.Fatalf("endless churn is members/unseen-pid at the longer bound too: %v/%s", m, why)
	}
	if elapsed() < 4*time.Second || elapsed() > 4*time.Second+50*time.Millisecond {
		t.Fatalf("it must run to ITS deadline (4s), not the package's (1s): %s", elapsed())
	}
}

// modelledHost2853 is a MODELLED /proc for a real held launch: nothing the stop scans is the host's own
// /proc (which, on a busy host, is itself churn and made the earlier wall-clock version of these pins
// flaky). Listings advance a modelled clock 10ms each; a vanisher (a pid listed and gone before its stat
// read) is present from the start, so every pass is void, until `clearAfterKill` listings after the first
// recorded SIGKILL (0: never). The launch's leader (a real, held zombie) is read as a Z member of its own
// group; two unrelated processes sit in another group.
type modelledHost2853 struct {
	leader         atomic.Int64
	lists          atomic.Int64
	sinceKill      atomic.Int64
	clearAfterKill int64
	killed         *atomic.Bool
	elapsedNs      atomic.Int64
	vanish         atomic.Bool
}

func (m *modelledHost2853) install(t *testing.T, shortDeadline time.Duration) {
	t.Helper()
	m.vanish.Store(true)
	oldDir, oldStat, oldClock, oldDeadline, oldPause, oldWatch := readProcDir, readGroupStatFn, scanClock, groupScanDeadline, scanPause, drainWatchInterval
	t.Cleanup(func() {
		// Runs before the fixture's settle (LIFO): the churn ends, so the watcher's next tick can drain
		// the held zombie and the fixture can reap it.
		m.vanish.Store(false)
		m.clearAfterKill = 0
		readProcDir, readGroupStatFn, scanClock, groupScanDeadline, scanPause, drainWatchInterval = oldDir, oldStat, oldClock, oldDeadline, oldPause, oldWatch
	})
	base := time.Unix(1_700_000_000, 0)
	scanClock = func() time.Time { return base.Add(time.Duration(m.elapsedNs.Load())) }
	groupScanDeadline, scanPause = shortDeadline, 0
	// The watcher must not drain and reap the zombie behind the stop's back: its first tick comes
	// well after the stop (which is a few hundred ms of real time on the modelled host).
	drainWatchInterval = 2 * time.Second
	readProcDir = func(string) ([]os.DirEntry, error) {
		n := m.lists.Add(1)
		m.elapsedNs.Add(int64(10 * time.Millisecond))
		if m.killed.Load() && m.clearAfterKill > 0 && m.sinceKill.Add(1) >= m.clearAfterKill {
			m.vanish.Store(false)
		}
		out := []os.DirEntry{churnEntry{"200"}, churnEntry{"201"}}
		if l := m.leader.Load(); l != 0 {
			out = append(out, churnEntry{strconv.Itoa(int(l))})
		}
		if m.vanish.Load() {
			out = append(out, churnEntry{strconv.Itoa(int(9000 + n))})
		}
		return out, nil
	}
	readGroupStatFn = func(path string) (int, byte, int, error) {
		pid, _ := strconv.Atoi(path[len("/proc/") : len(path)-len("/stat")])
		switch {
		case int64(pid) == m.leader.Load():
			return pid, 'Z', 1, nil // the held leader, alone in its group
		case pid == 200 || pid == 201:
			return 7, 'S', 1, nil
		}
		return 0, 0, 0, fs.ErrNotExist // a vanisher: listed, gone before its own stat read
	}
}

func (m *modelledHost2853) lister(d string) ([]os.DirEntry, error) { return readProcDir(d) }

func launchHeld2853(t *testing.T, f *stopFixture, m *modelledHost2853) Agent {
	t.Helper()
	a, err := ExecHost{TerminateGrace: 30 * time.Millisecond, procList: m.lister}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	m.leader.Store(int64(a.PID()))
	f.rec.bind(a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	return a
}

// A real stop under churn that outlasts the ladder's own short scans but not the final scan, on the MODELLED
// host (no wall-clock or host-/proc dependence): the ladder's scans give up after 50ms (5 listings), the final
// scan has 3s (300 listings), the vanisher clears 100 listings after the SIGKILL. Only the final scan can reach
// the clean pass, so it must CONFIRM (two signals, TERM then KILL); without it, with it on the short deadline,
// or with a members answer read as drained, the stop is unconfirmed or wrongly confirmed.
func TestAStopUnderChurnThatOutlastsTheLadderConfirmsOnTheFinalScan2853(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	f := newJourneyFixture()
	f.install(t)
	m := &modelledHost2853{clearAfterKill: 100, killed: &f.killed}
	m.install(t, 50*time.Millisecond)
	oldKill, oldStop := postKillWait, groupStopScanDeadline
	postKillWait, groupStopScanDeadline = 100*time.Millisecond, 3*time.Second
	t.Cleanup(func() { postKillWait, groupStopScanDeadline = oldKill, oldStop })
	a := launchHeld2853(t, f, m)
	stopped, detail := a.Terminate()
	ea := f.rec.bound()
	want := fmt.Sprintf("killed the launched process group (pid %d) after it ignored SIGTERM for %s", a.PID(), ea.grace)
	if !stopped || detail != want {
		t.Fatalf("the final scan outlasts the churn and confirms: stopped=%v detail=%q want %q", stopped, detail, want)
	}
	if n := f.rec.count(); n != 2 || f.rec.at(0).sig != syscall.SIGTERM || f.rec.at(1).sig != syscall.SIGKILL {
		t.Fatalf("the ladder is still exactly one TERM then one KILL: %d signals", f.rec.count())
	}
	if !ea.reaped.Load() || !ea.confirmed.Load() {
		t.Fatalf("a confirmed stop completed its reap: reaped=%v confirmed=%v", ea.reaped.Load(), ea.confirmed.Load())
	}
}

// The same stop when the churn never clears inside the final scan's window: unconfirmed, the wording says the
// final scan was tried (how long) and names its code, and the records are kept (nothing reaped).
func TestAnUnconfirmedStopSaysItTriedTheFinalScanAndNamesTheCode2853(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	f := newJourneyFixture()
	f.install(t)
	m := &modelledHost2853{clearAfterKill: 0 /* never */, killed: &f.killed}
	m.install(t, 50*time.Millisecond)
	oldKill, oldStop := postKillWait, groupStopScanDeadline
	postKillWait, groupStopScanDeadline = 100*time.Millisecond, 600*time.Millisecond
	t.Cleanup(func() { postKillWait, groupStopScanDeadline = oldKill, oldStop })
	a := launchHeld2853(t, f, m)
	stopped, detail := a.Terminate()
	want := fmt.Sprintf("the launched process group (pid %d) did not exit within %s of SIGKILL, nor in a final scan of up to %s (last membership scan: %s)",
		a.PID(), postKillWait, groupStopScanDeadline, whyUnseenPid)
	if stopped || detail != want {
		t.Fatalf("an unprovable stop says what it tried: stopped=%v detail=%q want %q", stopped, detail, want)
	}
	if ea := f.rec.bound(); ea.reaped.Load() || ea.confirmed.Load() {
		t.Fatalf("an unconfirmed stop keeps its records: reaped=%v confirmed=%v", ea.reaped.Load(), ea.confirmed.Load())
	}
}

// The watcher's periodic probe (execAgent.members) keeps the SHORT package deadline: only the stop's
// last scan is longer, so a held zombie under endless churn is not scanned for seconds every 200ms.
func TestTheWatcherProbeKeepsTheShortDeadline2853(t *testing.T) {
	f := churnHost(3)
	f.install(t)
	withFreshVanisher(f, 1<<30)
	elapsed := withScanClock(t, 10*time.Millisecond, time.Second)
	oldStop := groupStopScanDeadline
	groupStopScanDeadline = 5 * time.Second
	t.Cleanup(func() { groupStopScanDeadline = oldStop })
	// Both branches of members(): the package seam (no lister) and a launch's own lister.
	own := func(d string) ([]os.DirEntry, error) { return readProcDir(d) }
	for name, a := range map[string]*execAgent{"seam": {pid: churnPgid}, "launch lister": {pid: churnPgid, procList: own}} {
		before := elapsed()
		if m, why := a.members(); !m || why != whyUnseenPid {
			t.Fatalf("%s: endless churn is members/unseen-pid: %v/%s", name, m, why)
		}
		if d := elapsed() - before; d < time.Second || d > time.Second+50*time.Millisecond {
			t.Fatalf("%s: the watcher's probe runs to the package deadline (1s), not the stop's (5s): %s", name, d)
		}
	}
}

// The stop's last scan records the code of what it found, so an unconfirmed stop names it.
func TestTheFinalScanRecordsItsCodeForTheUnconfirmedWording2853(t *testing.T) {
	f := churnHost(3)
	f.install(t)
	withFreshVanisher(f, 1<<30)
	withScanClock(t, 10*time.Millisecond, time.Second)
	m, why := (&execAgent{pid: churnPgid}).membersWithin(2 * time.Second)
	if !m || why != whyUnseenPid {
		t.Fatalf("endless churn is members/unseen-pid at the final scan: %v/%s", m, why)
	}
	ag := &execAgent{pid: churnPgid}
	ag.membersWithin(2 * time.Second)
	if got := ag.lastMembersWhy(); got != whyUnseenPid {
		t.Fatalf("the final scan must record its code for the stop's wording, got %q", got)
	}
}
