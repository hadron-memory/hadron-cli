//go:build windows

package hostpoller

import (
	"context"
	"os"
	"testing"
	"time"
)

// #1474 delta review P1-1: a launch that already exited is never taskkilled.
// Windows reuses pids aggressively and cmd.Wait releases the handle, so
// taskkill /PID <pid> /T /F on the exited launch's pid would force-kill
// whatever process now holds the number — and its whole descendant tree.
// The exited-before-binding path always reaches Terminate with Done closed.
// Terminate must confirm the stop without running taskkill at all (and
// without reporting it unconfirmed, which would keep the records forever).
func TestTerminateAfterExitRunsNoTaskkill(t *testing.T) {
	a, err := ExecHost{}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"cmd", "/c", "exit 0"}, Dir: t.TempDir(), Env: os.Environ(),
	})
	if err != nil {
		t.Skipf("cmd not available: %v", err)
	}
	select {
	case <-a.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the launch did not exit")
	}
	calls := 0
	real := runTaskkill
	runTaskkill = func(int) ([]byte, error) {
		calls++
		return nil, nil
	}
	defer func() { runTaskkill = real }()
	stopped, detail := a.Terminate()
	if !stopped || detail != "" {
		t.Errorf("an exited launch is a silent confirmed stop: stopped=%v %q", stopped, detail)
	}
	if calls != 0 {
		t.Errorf("an exited launch is never taskkilled: %d calls", calls)
	}
}

// #2099: exercise the Windows adapter as well as the portable decision. The
// callback models a binding-lock wait that finishes after this launch exited.
func TestTerminateWithExitDuringAuthorizationRunsNoTaskkill(t *testing.T) {
	a := &execAgent{done: make(chan struct{}), pid: 4242}
	old := runTaskkill
	calls := 0
	runTaskkill = func(int) ([]byte, error) { calls++; return nil, nil }
	t.Cleanup(func() { runTaskkill = old })
	released := 0
	stopped, detail := a.TerminateWith(func() (func(), bool) {
		close(a.done)
		return func() { released++ }, true
	})
	if !stopped || calls != 0 || released != 1 || detail == "" {
		t.Fatalf("stopped=%v taskkill calls=%d releases=%d detail=%q", stopped, calls, released, detail)
	}
}
