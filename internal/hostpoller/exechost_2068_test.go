//go:build unix

package hostpoller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// hadron-cli#2068: a launched agent's stop asks its authorization before
// SIGTERM and again before SIGKILL, and a refusal sends nothing further.
func TestExecAgentTerminateWithAsksBeforeEachSignal(t *testing.T) {
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "trap-installed")
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"sh", "-c", `trap '' TERM; touch "$0"; while :; do sleep 1; done`, ready}, Dir: dir, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = a.Terminate() }()
	waitFor(t, "the TERM trap to be installed", func() bool { _, err := os.Stat(ready); return err == nil })
	asked, released := 0, 0
	stopped, detail := a.(*execAgent).TerminateWith(func() (func(), bool) {
		asked++
		return func() { released++ }, asked == 1 // TERM allowed; the KILL refused
	})
	if stopped || !strings.Contains(detail, "declined") {
		t.Fatalf("stopped=%v %q", stopped, detail)
	}
	if asked != 2 || released != 1 {
		t.Fatalf("asked=%d released=%d: asked before TERM and again before KILL; released after each signal that went out", asked, released)
	}
	if err := syscall.Kill(a.PID(), 0); err != nil {
		t.Fatalf("a declined KILL must leave the process: %v", err)
	}
}

func TestExecAgentTerminateWithRefusedFirstSignalsNothing(t *testing.T) {
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	a, err := ExecHost{TerminateGrace: time.Second}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"sh", "-c", "while :; do sleep 1; done"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = a.Terminate() }()
	time.Sleep(100 * time.Millisecond)
	if stopped, _ := a.(*execAgent).TerminateWith(func() (func(), bool) { return nil, false }); stopped {
		t.Fatal("a refused stop is not a stop")
	}
	if err := syscall.Kill(a.PID(), 0); err != nil {
		t.Fatalf("nothing may be signalled: %v", err)
	}
	// A later plain Terminate is unaffected by the refused attempt.
	if stopped, _ := a.Terminate(); !stopped {
		t.Fatal("the agent must still be stoppable")
	}
}

// Blind P2: each call's authorization is its own. Concurrent stops of one
// agent never consult one another's callback, and a plain Terminate after
// them consults none.
func TestExecAgentConcurrentTerminateWithKeepsEachAuthorizationItsOwn(t *testing.T) {
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "up")
	a, err := ExecHost{TerminateGrace: 200 * time.Millisecond}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"sh", "-c", `touch "$0"; while :; do sleep 1; done`, ready}, Dir: dir, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = a.Terminate() }()
	waitFor(t, "the agent to start", func() bool { _, err := os.Stat(ready); return err == nil })
	const n = 8
	var wg sync.WaitGroup
	var asked [n]atomic.Int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a.(*execAgent).TerminateWith(func() (func(), bool) { asked[i].Add(1); return func() {}, true })
		}(i)
	}
	wg.Wait()
	total := int32(0)
	for i := range asked {
		if asked[i].Load() > 2 {
			t.Errorf("call %d was asked %d times: it consulted another call's authorization", i, asked[i].Load())
		}
		total += asked[i].Load()
	}
	if total == 0 {
		t.Fatal("no call asked its authorization before signalling")
	}
	if stopped, _ := a.Terminate(); !stopped {
		t.Fatal("the agent must be stopped")
	}
}

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
