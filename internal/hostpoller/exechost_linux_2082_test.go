//go:build linux

package hostpoller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// holdlessLaunch launches a long-running agent as a launch that took no zombie
// hold (the waitid refusal forced), counting every signal the stop sends. The
// returned cleanup restores the package hooks. The agent's shell keeps a live
// child (sleep) in the group, and a process-group number is never reused while
// any member of the group exists, so the test's own final group kill below can
// only ever reach this launch's own group.
func holdlessLaunch(t *testing.T, script string) (*execAgent, *atomic.Int32, func()) {
	t.Helper()
	realWait, realSignal := waitLeaderExit, signalGroup
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
		waitLeaderExit, signalGroup = realWait, realSignal
		t.Fatal(err)
	}
	waitFor(t, "the agent to start", func() bool { _, err := os.Stat(ready); return err == nil })
	return a.(*execAgent), signals, func() {
		_ = realSignal(a.PID(), syscall.SIGKILL) // the group still holds our sleep child: its number is ours
		waitLeaderExit, signalGroup = realWait, realSignal
	}
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
