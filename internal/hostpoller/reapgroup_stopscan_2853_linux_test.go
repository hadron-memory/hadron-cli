//go:build linux

package hostpoller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

// A real stop under churn that outlasts the ladder's own waits but not the final scan: the storm clears
// 400ms after the SIGKILL, the post-kill wait is 100ms and every ladder scan is 20ms, so only the stop's
// last (longer) scan can prove the group empty. It must CONFIRM (two signals, TERM then KILL), where the
// first fold reported it unconfirmed ("did not exit within ... of SIGKILL (last membership scan: unseen-pid)").
func TestAStopUnderChurnThatOutlastsTheLadderConfirmsOnTheFinalScan2853(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	f := newStormFixture(false)
	f.flipAfterKill = 400 * time.Millisecond
	f.install(t)
	oldKill, oldStop := postKillWait, groupStopScanDeadline
	postKillWait, groupStopScanDeadline = 100*time.Millisecond, 3*time.Second
	t.Cleanup(func() { postKillWait, groupStopScanDeadline = oldKill, oldStop })
	a, err := ExecHost{TerminateGrace: 30 * time.Millisecond}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	f.rec.bind(a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
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
