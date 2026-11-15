//go:build unix

package hostpoller

import (
	"context"
	"os"
	"strings"
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
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"sh", "-c", "trap '' TERM; while :; do sleep 1; done"}, Dir: dir, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = a.Terminate() }()
	time.Sleep(150 * time.Millisecond) // the trap is in place
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
