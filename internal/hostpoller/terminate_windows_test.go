//go:build windows

package hostpoller

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
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
	runTaskkill = func(context.Context, int) ([]byte, error) {
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
	old := runTaskkill
	calls := 0
	runTaskkill = func(context.Context, int) ([]byte, error) { calls++; return nil, nil }
	t.Cleanup(func() { runTaskkill = old })
	for _, viaDone := range []bool{true, false} {
		a := &execAgent{done: make(chan struct{}), pid: 4242}
		released := 0
		stopped, detail := a.TerminateWith(func() (func(), bool) {
			if viaDone {
				close(a.done)
			} else {
				a.exited.Store(true)
			}
			return func() { released++ }, true
		})
		if !stopped || calls != 0 || released != 1 || !strings.Contains(detail, "already exited") || !strings.Contains(detail, "4242") || !strings.Contains(detail, "not touched") {
			t.Fatalf("viaDone=%v stopped=%v taskkill calls=%d releases=%d detail=%q", viaDone, stopped, calls, released, detail)
		}
	}
}

func TestTerminateWithBlockingTaskkillHoldsAuthorizationAndHasDeadline(t *testing.T) {
	a := &execAgent{done: make(chan struct{}), pid: 4242}
	var held atomic.Bool
	entered, finish := make(chan struct{}), make(chan struct{})
	old := runTaskkill
	runTaskkill = func(ctx context.Context, pid int) ([]byte, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > taskkillTimeout {
			t.Error("Windows taskkill did not receive the cancellation deadline")
		}
		if pid != 4242 || !held.Load() {
			t.Error("taskkill called outside this launch's authorization")
		}
		close(entered)
		<-finish
		return nil, nil
	}
	finished := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
		<-finished
		runTaskkill = old
	})
	go func() {
		defer close(finished)
		stopped, _ := a.TerminateWith(func() (func(), bool) {
			held.Store(true)
			return func() { held.Store(false) }, true
		})
		if !stopped {
			t.Error("the successful injected taskkill was not confirmed")
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("taskkill was never called")
	}
	if !held.Load() {
		t.Error("binding authority was released while taskkill was blocked")
	}
	close(finish)
	<-finished
	if held.Load() {
		t.Error("binding authority outlived taskkill")
	}
}
