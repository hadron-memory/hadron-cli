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

// hadron-cli#2082: on a launch with no zombie hold, the leader's exit frees the
// group's number unserialized with a stop. The authorization can wait (the
// binding lock, up to 2 s); if the leader exits during that wait, the number
// may be reused, so authority is proved again once the authorization is held
// and nothing is signalled.
func TestExecAgentTerminateWithDoesNotSignalAfterTheLeaderExitedDuringTheLockWait(t *testing.T) {
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	realWait, realSignal := waitLeaderExit, signalGroup
	defer func() { waitLeaderExit, signalGroup = realWait, realSignal }()
	waitLeaderExit = func(int) (leaderExit, error) { return leaderExit{kind: unknownStatus}, syscall.ENOSYS } // no hold
	var signals atomic.Int32
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals.Add(1)
		return realSignal(pgid, sig)
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "up")
	a, err := ExecHost{TerminateGrace: 200 * time.Millisecond}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"sh", "-c", `touch "$0"; while :; do sleep 1; done`, ready}, Dir: dir, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-a.PID(), syscall.SIGKILL) }()
	waitFor(t, "the agent to start", func() bool { _, err := os.Stat(ready); return err == nil })
	ea := a.(*execAgent)
	stopped, detail := ea.TerminateWith(func() (func(), bool) {
		// The binding lock is busy for a while; the leader exits meanwhile.
		_ = syscall.Kill(a.PID(), syscall.SIGKILL)
		waitFor(t, "the leader's exit to be observed", func() bool { return ea.exited.Load() })
		return func() {}, true
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
}
