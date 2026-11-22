//go:build linux

package hostpoller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// cli#2954: follow-ups to the cli#2853 final scan. Item 1: a platform without the zombie hold has a
// final-scan deadline of 0 and runs no final scan, so the unconfirmed wording must not claim one
// ("nor in a final scan of up to 0s"). Item 2: the modelled /proc is installed until the fixture's settle
// has run (settle drains over the SAME modelled listing, never the host's real /proc). Item 5: the
// authority re-check in the final branch.

// installAround2954 installs the model AROUND the fixture's own install, in the order its cleanups must
// run: the model's seam restore is registered FIRST (so it runs LAST, after the fixture's settle, which
// drains over the modelled listing), and the churn's end is registered LAST (so it runs FIRST: the held
// zombie must be drainable when settle looks).
func (m *modelledHost2853) installAround2954(t *testing.T, f *stopFixture, shortDeadline time.Duration) {
	t.Helper()
	m.install(t, shortDeadline)
	f.install(t)
	t.Cleanup(func() {
		m.vanish.Store(false)
		m.clearAfterKill = 0
	})
}

func skipWithoutHeldHost2954(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
}

// With no final scan (deadline 0) an unconfirmed stop says only what ran: it did not exit within the
// post-SIGKILL wait. No "final scan" and no "up to 0s".
func TestAnUnconfirmedStopWithNoFinalScanDoesNotClaimOne2954(t *testing.T) {
	skipWithoutHeldHost2954(t)
	f := newJourneyFixture()
	m := &modelledHost2853{clearAfterKill: 0 /* never */, killed: &f.killed}
	m.installAround2954(t, f, 50*time.Millisecond)
	oldKill, oldStop := postKillWait, groupStopScanDeadline
	postKillWait, groupStopScanDeadline = 100*time.Millisecond, 0
	t.Cleanup(func() { postKillWait, groupStopScanDeadline = oldKill, oldStop })
	a := launchHeld2853(t, f, m)
	stopped, detail := a.Terminate()
	if stopped {
		t.Fatalf("churn that never clears is unconfirmed: %q", detail)
	}
	if want := fmt.Sprintf("the launched process group (pid %d) did not exit within %s of SIGKILL", a.PID(), postKillWait); !strings.HasPrefix(detail, want) {
		t.Fatalf("the detail starts with what ran: %q, want prefix %q", detail, want)
	}
	for _, bad := range []string{"final scan", "up to 0s"} {
		if strings.Contains(detail, bad) {
			t.Fatalf("no final scan ran, so the wording must not mention %q: %q", bad, detail)
		}
	}
}

// The model must still be installed when the fixture settles (settle drains an unconfirmed stop's
// leftovers; over the host's real /proc, under saturation, it can outlast the watcher's settle bound).
// The hook lists /proc through the seam at that moment: a modelled listing advances the model's counter.
func TestTheModelStaysInstalledUntilTheFixtureSettles2954(t *testing.T) {
	skipWithoutHeldHost2954(t)
	f := newJourneyFixture()
	m := &modelledHost2853{clearAfterKill: 0 /* never */, killed: &f.killed}
	f.beforeSettle = func() {
		before := m.lists.Load()
		_, _ = readProcDir("/proc")
		if m.lists.Load() <= before {
			t.Errorf("settle ran over the host's real /proc: the modelled listing was uninstalled before it")
		}
	}
	f.install(t)
	m.install(t, 50*time.Millisecond)
	oldKill, oldStop := postKillWait, groupStopScanDeadline
	postKillWait, groupStopScanDeadline = 100*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { postKillWait, groupStopScanDeadline = oldKill, oldStop })
	a := launchHeld2853(t, f, m)
	if stopped, detail := a.Terminate(); stopped {
		t.Fatalf("never-clearing churn is unconfirmed: %q", detail)
	}
}

// The authority re-check in the final branch: a launch WITHOUT the hold whose leader exits during the final
// scan must answer unconfirmed, never "killed ..." (the number may have been reused: nothing proves the
// group that went was this launch's). Deterministic: the signals are swallowed (the leader stays alive
// and the launch keeps its authority), postKillWait is 1ns so the post-SIGKILL probes are exactly two (the
// poll's head and its timeout return), and the THIRD probe after the SIGKILL is the final scan's: there the
// leader's exit is observed (exited set) while the probe answers ESRCH. Kills: the re-check removed.
func TestAFinalScanThatSeesTheLeaderExitAnswersUnconfirmedNotKilled2954(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
	}
	realWait, realSignal, realProbe, realKill := waitLeaderExit, signalGroup, probeGroup, postKillWait
	realStop := groupStopScanDeadline
	waitLeaderExit = func(int) (leaderExit, error) { return leaderExit{kind: unknownStatus}, syscall.ENOSYS }
	var killed atomic.Bool
	var afterKill atomic.Int64
	var ea *execAgent
	signalGroup = func(pgid int, sig syscall.Signal) error {
		if sig == syscall.SIGKILL {
			killed.Store(true)
		}
		return nil // swallowed: the leader keeps running and the launch keeps its authority
	}
	probeGroup = func(int) error {
		if !killed.Load() {
			return nil // the group is there
		}
		if afterKill.Add(1) == 3 {
			ea.exited.Store(true) // the leader's exit is observed during the final scan...
			return syscall.ESRCH  // ...and the group is gone
		}
		return nil
	}
	postKillWait, groupStopScanDeadline = time.Nanosecond, time.Second
	a, err := ExecHost{TerminateGrace: 30 * time.Millisecond}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"sleep", "30"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	ea = a.(*execAgent)
	t.Cleanup(func() {
		waitLeaderExit, signalGroup, probeGroup, postKillWait, groupStopScanDeadline = realWait, realSignal, realProbe, realKill, realStop
		_ = syscall.Kill(-a.PID(), syscall.SIGKILL) // the leader never died: the number is still this launch's
		select {
		case <-a.Done():
		case <-time.After(5 * time.Second):
		}
	})
	for deadline := time.Now().Add(5 * time.Second); !ea.noHold.Load(); {
		if time.Now().After(deadline) {
			t.Fatal("the refused-waitid waiter never recorded the missing hold")
		}
		time.Sleep(5 * time.Millisecond)
	}
	stopped, detail := a.Terminate()
	if stopped || strings.Contains(detail, "killed") || !strings.Contains(detail, "unconfirmed") {
		t.Fatalf("a leader exit seen during the final scan is unconfirmed, never killed: stopped=%v %q", stopped, detail)
	}
	if afterKill.Load() < 3 {
		t.Fatalf("the final scan was never reached (probes after the kill: %d)", afterKill.Load())
	}
}
