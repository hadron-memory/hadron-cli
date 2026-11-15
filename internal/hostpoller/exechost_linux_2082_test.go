//go:build linux

package hostpoller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// holdlessReadyTimeout is how long holdlessLaunch waits for the agent's ready
// file. A variable so the failure-path test below can force the timeout fast.
var holdlessReadyTimeout = 5 * time.Second

// holdlessLaunchedPID is the pid of the last agent holdlessLaunch started, so
// the failure-path test can look for a leaked group after a t.Fatal.
var holdlessLaunchedPID atomic.Int32

// holdlessLaunch launches a long-running agent as a launch that took no zombie
// hold (the waitid refusal forced), counting every signal the stop sends. The
// agent's shell keeps a live child (sleep) in the group, and a process-group
// number is never reused while any member of the group exists, so the test's
// own final group kill below can only ever reach this launch's own group.
//
// Cleanup is registered with t.Cleanup BEFORE anything that can fail the test:
// the hooks are restored and the launch's own group is killed even when the
// readiness wait times out and t.Fatalf leaves before this returns
// (hadron-cli#2089). The returned func runs the same cleanup early and is
// idempotent, so a caller's `defer done()` is still fine. These hooks are
// package variables shared by every test; none in this package is parallel
// (`git grep t.Parallel -- internal/hostpoller` is empty), which is a property
// of the suite today, not a protection.
func holdlessLaunch(t *testing.T, script string) (*execAgent, *atomic.Int32, func()) {
	t.Helper()
	realWait, realSignal := waitLeaderExit, signalGroup
	var once sync.Once
	var pid atomic.Int32
	cleanup := func() {
		once.Do(func() {
			if p := int(pid.Load()); p > 0 {
				_ = realSignal(p, syscall.SIGKILL) // the group still holds our sleep child: its number is ours
			}
			waitLeaderExit, signalGroup = realWait, realSignal
		})
	}
	t.Cleanup(cleanup)
	waitLeaderExit = func(int) (leaderExit, error) { return leaderExit{kind: unknownStatus}, syscall.ENOSYS } // no hold
	signals := &atomic.Int32{}
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals.Add(1)
		return realSignal(pgid, sig)
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "up")
	a, err := ExecHost{TerminateGrace: 200 * time.Millisecond}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"sh", "-c", script, ready}, Dir: dir, Env: os.Environ()})
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	pid.Store(int32(a.PID()))
	holdlessLaunchedPID.Store(int32(a.PID()))
	waitForWithin(t, holdlessReadyTimeout, "the agent to start", func() bool { _, err := os.Stat(ready); return err == nil })
	return a.(*execAgent), signals, cleanup
}

// hadron-cli#2089: a readiness timeout inside holdlessLaunch must still restore
// both hooks and collect the launch's own group. The failing case runs in a
// child test process (it ends in t.Fatalf), and reports what its last cleanup
// found; the parent checks the report.
func TestHoldlessLaunchCleansUpWhenTheReadinessWaitTimesOut(t *testing.T) {
	if os.Getenv("HADRON_HOLDLESS_FAIL_CHILD") == "1" {
		holdlessFailureChild(t)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHoldlessLaunchCleansUpWhenTheReadinessWaitTimesOut$", "-test.v")
	cmd.Env = append(os.Environ(), "HADRON_HOLDLESS_FAIL_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the child was meant to fail on its readiness timeout:\n%s", out)
	}
	got := string(out)
	if !strings.Contains(got, "timed out waiting for the agent to start") {
		t.Fatalf("the child did not fail the way the test needs:\n%s", got)
	}
	for _, want := range []string{"HOOKS-RESTORED", "GROUP-GONE"} {
		if !strings.Contains(got, want) {
			t.Fatalf("after a readiness timeout the cleanup did not report %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "LEAK") {
		t.Fatalf("the child reported a leak:\n%s", got)
	}
}

func holdlessFailureChild(t *testing.T) {
	origWait, origSignal := reflect.ValueOf(waitLeaderExit).Pointer(), reflect.ValueOf(signalGroup).Pointer()
	// Registered first, so it runs last: after the helper's own cleanup.
	t.Cleanup(func() {
		if reflect.ValueOf(waitLeaderExit).Pointer() == origWait && reflect.ValueOf(signalGroup).Pointer() == origSignal {
			fmt.Println("HOOKS-RESTORED")
		} else {
			fmt.Println("LEAK hooks still replaced")
		}
		pid := int(holdlessLaunchedPID.Load())
		for i := 0; i < 100; i++ {
			if err := syscall.Kill(-pid, 0); err == syscall.ESRCH {
				fmt.Println("GROUP-GONE")
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		fmt.Println("LEAK group still alive")
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	})
	holdlessReadyTimeout = 300 * time.Millisecond
	// The agent never touches its ready file: the readiness wait times out.
	holdlessLaunch(t, `sleep 300 & while :; do sleep 1; done`)
}

// hadron-cli#2082: on a launch with no zombie hold, the leader's exit frees the
// group's number unserialized with a stop. The authorization can wait (the
// binding lock, up to 2 s); if the leader exits during that wait, the number
// may be reused, so authority is proved again once the authorization is held
// and nothing is signalled, and the grant is released.
func TestExecAgentTerminateWithDoesNotSignalAfterTheLeaderExitedDuringTheLockWait(t *testing.T) {
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	ea, signals, done := holdlessLaunch(t, `touch "$0"; sleep 300 & while :; do sleep 1; done`)
	defer done()
	released := 0
	stopped, detail := ea.TerminateWith(func() (func(), bool) {
		// The binding lock is busy for a while; the leader exits meanwhile.
		_ = syscall.Kill(ea.PID(), syscall.SIGKILL)
		waitFor(t, "the leader's exit to be observed", func() bool { return ea.exited.Load() })
		return func() { released++ }, true
	})
	if stopped {
		t.Fatalf("a stop whose group number may have been reused is not a confirmed stop: %q", detail)
	}
	if signals.Load() != 0 {
		t.Fatalf("%d signal(s) were sent to a group number the exited leader no longer owned", signals.Load())
	}
	if !strings.Contains(detail, "without the ownership hold") {
		t.Fatalf("the report must say the process was not touched, unconfirmed and its records kept: %q", detail)
	}
	if released != 1 {
		t.Fatalf("the binding grant was released %d times on the lost-authority path, want once", released)
	}
}

// The same, at the SIGKILL arm: SIGTERM goes out, the agent ignores it, and the
// leader exits during the SECOND authorization's wait; SIGKILL is not sent.
func TestExecAgentTerminateWithDoesNotKillAfterTheLeaderExitedDuringTheSecondLockWait(t *testing.T) {
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	ea, signals, done := holdlessLaunch(t, `trap '' TERM; touch "$0"; sleep 300 & while :; do sleep 1; done`)
	defer done()
	asked, released := 0, 0
	stopped, detail := ea.TerminateWith(func() (func(), bool) {
		asked++
		if asked == 2 {
			_ = syscall.Kill(ea.PID(), syscall.SIGKILL)
			waitFor(t, "the leader's exit to be observed", func() bool { return ea.exited.Load() })
		}
		return func() { released++ }, true
	})
	if stopped || signals.Load() != 1 {
		t.Fatalf("stopped=%v signals=%d: only the SIGTERM may have been sent (%q)", stopped, signals.Load(), detail)
	}
	if !strings.Contains(detail, "without the ownership hold") || released != 2 {
		t.Fatalf("detail=%q released=%d, want unconfirmed and both grants released", detail, released)
	}
}
