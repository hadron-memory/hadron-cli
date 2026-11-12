//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A launch reserves its pid for its whole tracked life (pinPid): the
// reservation exists from Launch (before anything can exit), survives the
// process's exit and reap, and ends exactly at Release — after which the
// number is ordinary again. Terminate on the reaped-alone launch probes
// once (ESRCH — the group is gone; a reserved number with no group answers
// nothing else) and confirms silently, signalling nothing (#1474 delta
// round 2, Jane D1: durable lifetime ownership replaces the reuse-model
// probes of the earlier rounds — a reused number answering a probe can no
// longer happen while the pin is held).
func TestLaunchReservesThePidUntilRelease(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	signals, probes := 0, 0
	realSignal, realProbe := signalGroup, probeGroup
	defer func() { signalGroup, probeGroup = realSignal, realProbe }()
	probeGroup = func(pgid int) error {
		probes++
		return realProbe(pgid)
	}
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals++
		return realSignal(pgid, sig)
	}
	a, err := ExecHost{}.Launch(context.Background(), LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	ea := a.(*execAgent)
	if ea.releasePid == nil {
		t.Fatal("a launch on a pidfd kernel reserves its pid at Launch")
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	// The reservation outlives the exit and the reap.
	if ea.releasePid == nil {
		t.Fatal("the reservation survives the process's exit")
	}
	stopped, detail := a.Terminate()
	if !stopped || detail != "" {
		t.Fatalf("a reaped leader-alone launch is a silent confirmed stop: stopped=%v %q", stopped, detail)
	}
	if probes != 1 || signals != 0 {
		t.Fatalf("one probe (ESRCH) confirms the empty group; nothing is signalled: %d probes, %d signals", probes, signals)
	}
	a.Release()
	if ea.releasePid != nil {
		t.Fatal("Release drops the reservation")
	}
	a.Release() // idempotent: forgetting twice must not close twice
}

// A kernel or seccomp policy without pidfd_open gets no reservation; the
// stop degrades to the probe path and still confirms a gone group (#1474
// delta round 2: the fallback is the documented position, never a failure).
func TestTerminateWithoutAPidfdStillConfirms(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	realOpen := pidfdOpen
	pidfdOpen = func(int, int) (int, error) { return -1, syscall.ENOSYS }
	defer func() { pidfdOpen = realOpen }()
	a, err := ExecHost{}.Launch(context.Background(), LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	if ea := a.(*execAgent); ea.releasePid != nil {
		t.Fatal("an unavailable pidfd_open reserves nothing")
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	stopped, detail := a.Terminate()
	if !stopped || detail != "" {
		t.Fatalf("without a reservation the probe still confirms the gone group: stopped=%v %q", stopped, detail)
	}
	a.Release()
}

// A wrapper's child that outlives its reaped leader keeps the GROUP alive —
// and the pin keeps the NUMBER that group lives on, so Terminate still
// reaches and kills it. This is the original #1474 shape, restated under
// the reservation: leader reaped, member alive on the reserved number
// (asserted by a DIRECT signal-0 probe — the pin is what makes that probe
// mean THIS launch's group — so the probe a reviewer would run is the
// test), stop confirmed by the member's death, and the reserved number
// answers ESRCH after the confirmed stop.
//
// The member's aliveness is deliberately NOT observed by wrapping
// probeGroup during Terminate: an interceptor installed on the package var
// while terminateGroup's ticker loop runs through it proved unstable on
// this kernel (go1.26.10/linux 6.12: the wrapped run kept reading the
// group alive for the full postKillWait while a same-pgid probe from a
// concurrent process read ESRCH within 100ms of the kill, and any added
// work inside the wrapper flipped the outcome) — delta round 2,
// 2026-11-12. The direct probe below asserts the same fact without going
// through the interceptor.
func TestPinnedGroupWithALateMemberIsStillStopped(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	dir := t.TempDir()
	ready := dir + "/child-armed"
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sh", "-c", `sh -c 'trap "" TERM; touch "$0"; sleep 30' "$0" & exit 0`, ready},
		Dir:  dir, Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ea := a.(*execAgent)
	if ea.releasePid == nil {
		t.Fatal("a launch on a pidfd kernel reserves its pid")
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the leader must exit at once")
	}
	armed := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(armed) {
			t.Fatal("the child never armed its trap")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The leader is reaped; the member lives on in the reserved number's
	// group, and the direct probe finds it — the reservation is why this
	// probe can only mean this launch's group (#1474 delta round 2, Jane
	// D1: durable lifetime ownership).
	if err := syscall.Kill(-ea.pid, 0); err != nil {
		t.Fatalf("the member outlived its reaped leader on the reserved group: %v", err)
	}
	stopped, detail := a.Terminate()
	if !stopped || !strings.Contains(detail, "killed the launched process group") {
		t.Fatalf("a group whose member outlived its leader is stopped: stopped=%v %q", stopped, detail)
	}
	// The stop is confirmed and the member is dead: the reserved number
	// answers ESRCH — no group holds it, and none can until Release.
	if err := syscall.Kill(-ea.pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the killed group is gone from the reserved number: %v", err)
	}
	a.Release()
}
