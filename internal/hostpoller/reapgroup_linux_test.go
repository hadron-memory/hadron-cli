//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A leader-alone launch is a SILENT confirmed stop that touches nothing: the
// waiter waited the leader out without reaping it (waitid WNOWAIT), the
// /proc scan found no other member, and only then did it reap — so the stop
// confirms from that proof (emptyAtReap) without probing or signalling a
// number that has been public since the reap (#1474 delta round 3, Ada
// #102351; replacing the pidfd pin, whose probes==1 contract encoded the
// unsafe probe — Jonas N1).
func TestLeaderAloneExitIsASilentConfirmedStop(t *testing.T) {
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
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	stopped, detail := a.Terminate()
	if !stopped || detail != "" {
		t.Fatalf("an empty-at-reap launch is a silent confirmed stop: stopped=%v %q", stopped, detail)
	}
	if probes != 0 || signals != 0 {
		t.Fatalf("a number that went public at the empty reap is never touched: %d probes, %d signals", probes, signals)
	}
}

// The reuse model, restored from the round-2 deletion (Jonas N1): after the
// empty-at-reap, the group's number is public and ANOTHER process may hold
// it. A stop then must not reach for the number even though a group now
// answers it — this is Jane's probe shape under Ada's #102149/#102351
// ruling: signals=0 once the group number could be reused. The modeled
// probe reports a group on the number; the stop must still confirm from the
// empty-at-reap proof alone.
func TestStopAfterAnEmptyReapNeverSignalsAReusedNumber(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	a, err := ExecHost{}.Launch(context.Background(), LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	// The number is public now. Model a stranger that took it: the probe
	// answers "a group is there".
	signals, probes := 0, 0
	realSignal, realProbe := signalGroup, probeGroup
	defer func() { signalGroup, probeGroup = realSignal, realProbe }()
	probeGroup = func(int) error {
		probes++
		return nil // a group holds the number — a reused one
	}
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals++
		return realSignal(pgid, sig)
	}
	stopped, detail := a.Terminate()
	if !stopped || detail != "" {
		t.Fatalf("an empty-at-reap stop confirms without touching the reused number: stopped=%v %q", stopped, detail)
	}
	if probes != 0 || signals != 0 {
		t.Fatalf("a reused number answering the probe is never probed or signalled: %d probes, %d signals", probes, signals)
	}
}

// A wrapper's child that outlives its leader keeps the GROUP alive — and the
// UNREAPED leader holds the group's number allocated until the stop reaps
// it, so Terminate still reaches and kills the child and can never reach a
// process that took a reused number. This is the original #1474 shape
// restated under the zombie hold (#1474 delta round 3, Ada #102351; Cameron
// round 3: the leader-first child cleanup must survive the redesign).
//
// The member's aliveness is observed by a DIRECT signal-0 probe (outside
// Terminate) and the stop's drain oracle is the /proc membership scan, not
// the number probe: after the leader's exit a held zombie answers
// kill(-pgid, 0) forever, so the number probe CANNOT mean "the group lives"
// there — the round-2 interceptor instability (Jonas N2: a wrapped
// probeGroup read a killed group alive through the whole postKillWait while
// an external probe read ESRCH) is one face of exactly that: an unreaped
// member is a zombie that still answers the number probe, so the probe
// conflates "alive" with "unreaped", and its answer through a wrapper was a
// coin flip on reaping timing. The scan removes the question: it asks which
// processes hold the pgid, and a zombie that has not been reaped by its own
// parent keeps answering it too — which is why the drain waits for the
// member to be GONE, and an unconfirmed stop keeps records rather than
// guessing.
func TestHeldZombieWithALateMemberIsStillStopped(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	dir := t.TempDir()
	ready := dir + "/child-armed"
	signals := 0
	realSignal := signalGroup
	defer func() { signalGroup = realSignal }()
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals++
		return realSignal(pgid, sig)
	}
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sh", "-c", `sh -c 'trap "" TERM; touch "$0"; sleep 30' "$0" & exit 0`, ready},
		Dir:  dir, Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ea := a.(*execAgent)
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
	// Done is closed, the group still has a member — so the leader must be
	// the HELD ZOMBIE: exited, not reaped, holding the group's number. That
	// zombie is the whole ownership proof; pin it where it is cheapest to
	// see.
	if ea.reaped.Load() {
		t.Fatal("a launch whose group still has members is not reaped at its exit")
	}
	if state := procState(ea.pid); state != 'Z' {
		t.Fatalf("the exited leader is held as a zombie while its group lives: state %q", string(state))
	}
	// The member lives on in the group the zombie keeps allocated, and the
	// direct probe finds it — the hold is why this probe can only mean this
	// launch's group.
	if err := syscall.Kill(-ea.pid, 0); err != nil {
		t.Fatalf("the member outlived its leader on the held group: %v", err)
	}
	stopped, detail := a.Terminate()
	if !stopped || !strings.Contains(detail, "killed the launched process group") {
		t.Fatalf("a group whose member outlived its leader is stopped: stopped=%v %q", stopped, detail)
	}
	if signals != 2 {
		t.Fatalf("TERM then KILL to the held group: %d signals", signals)
	}
	// The stop confirmed and reaped the zombie: the number is public again
	// and nothing of this launch answers it.
	if !ea.reaped.Load() {
		t.Fatal("a confirmed stop reaps the held zombie")
	}
	if err := syscall.Kill(-ea.pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the killed group is gone from the released number: %v", err)
	}
}

// A kernel or policy where the hold cannot be taken (waitid fails) gets no
// proof: the waiter reaps at the exit, emptyAtReap stays unset, and the stop
// reports UNCONFIRMED — records kept — rather than signal a number the
// launch may no longer own (#1474 delta round 3: the fail-safe direction;
// Jonas N1's "no proof can be taken" case).
func TestWaitidFailureIsAnUnconfirmedStop(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	signals := 0
	realWait, realSignal := waitLeaderExit, signalGroup
	defer func() { waitLeaderExit, signalGroup = realWait, realSignal }()
	waitLeaderExit = func(int) error { return syscall.ENOSYS }
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals++
		return realSignal(pgid, sig)
	}
	a, err := ExecHost{}.Launch(context.Background(), LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	stopped, detail := a.Terminate()
	if stopped || !strings.Contains(detail, "could not be tracked through its exit") {
		t.Fatalf("without the hold the stop is unconfirmed and keeps records: stopped=%v %q", stopped, detail)
	}
	if signals != 0 {
		t.Fatalf("an unprovable launch is never signalled by number: %d signals", signals)
	}
}

// The forced-reuse regression the round-3 verdicts demand (Jane's and
// Cameron's reproductions, Ada #102351): in a private PID namespace, after
// an empty-at-reap launch, force the allocator to hand the launch's old
// number to a replacement process — and the old agent's Terminate must
// leave that replacement UNTOUCHED while confirming the stop. This is the
// executed form of TestStopAfterAnEmptyReapNeverSignalsAReusedNumber.
//
// It needs to write /proc/sys/kernel/ns_last_pid, so it runs only as PID 1
// of a private user+PID namespace (Jane's and Cameron's harness shape):
//
//	go test -c ./internal/hostpoller
//	unshare --user --map-root-user --pid --fork --mount-proc \
//	  env HADRON_PRIVATE_PIDNS=1 ./hostpoller.test -test.run '^TestForcedPidReuseNeverSignalsTheReplacement$' -test.v
func TestForcedPidReuseNeverSignalsTheReplacement(t *testing.T) {
	if os.Getenv("HADRON_PRIVATE_PIDNS") != "1" || os.Getpid() != 1 {
		t.Skip("only inside a private PID namespace (see the comment)")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
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
	a, err := ExecHost{TerminateGrace: 50 * time.Millisecond}.Launch(context.Background(), LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	old := a.PID()
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	if err := os.WriteFile("/proc/sys/kernel/ns_last_pid", []byte(strconv.Itoa(old-1)), 0o600); err != nil {
		t.Fatalf("forcing ns_last_pid: %v", err)
	}
	repl := exec.Command("sleep", "30")
	repl.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := repl.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repl.Process.Kill(); _, _ = repl.Process.Wait() }()
	if repl.Process.Pid != old {
		t.Fatalf("the reuse fixture missed: old=%d replacement=%d", old, repl.Process.Pid)
	}
	stopped, detail := a.Terminate()
	if !stopped || detail != "" {
		t.Fatalf("the empty-at-reap stop confirms silently: stopped=%v %q", stopped, detail)
	}
	if signals != 0 || probes != 0 {
		t.Fatalf("the reused number is never touched: %d probes, %d signals", probes, signals)
	}
	// The replacement lives: the old launch's stop reached nothing but its
	// own proof.
	if err := syscall.Kill(repl.Process.Pid, 0); err != nil {
		t.Fatalf("the unrelated replacement was killed by the old launch's stop: %v", err)
	}
}

// The held zombie keeps the number ALLOCATED: while a member of the launch's
// group lives, even a forced allocator (ns_last_pid set to the number) can
// not hand it to a new process — the allocator skips a number a zombie
// still holds. Same private-namespace harness as above.
//
// The test binary is PID 1 here, so the killed member's zombie reparents to
// IT, and nothing reaps it unless the test does — a host's init reaps
// orphans, and an unreaped zombie answers the /proc membership scan exactly
// as the old number-probe answered it (Jonas N2/P3-6: "alive" and "unreaped"
// are indistinguishable to both oracles). The targeted reaper below plays
// the host init's part — it reaps ONLY this launch's orphaned group members,
// never the held leader (whose zombie is the ownership proof) and never the
// replacement; without it the stop is — correctly — unconfirmed.
func TestTheHeldZombieKeepsTheNumberAllocated(t *testing.T) {
	if os.Getenv("HADRON_PRIVATE_PIDNS") != "1" || os.Getpid() != 1 {
		t.Skip("only inside a private PID namespace (see the comment)")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
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
	old := a.PID()
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
	// The member lives, so the leader is the held zombie; force the
	// allocator at the held number.
	if err := os.WriteFile("/proc/sys/kernel/ns_last_pid", []byte(strconv.Itoa(old-1)), 0o600); err != nil {
		t.Fatalf("forcing ns_last_pid: %v", err)
	}
	repl := exec.Command("sleep", "30")
	repl.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := repl.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repl.Process.Kill(); _, _ = repl.Process.Wait() }()
	if repl.Process.Pid == old {
		t.Fatalf("the held number was handed to a new process: old=%d replacement=%d", old, repl.Process.Pid)
	}
	// The init part: reap this launch's orphaned members as they die, never
	// the held leader (pid old) and never the replacement.
	reapStop := make(chan struct{})
	reapDone := make(chan struct{})
	go func() {
		defer close(reapDone)
		var ws syscall.WaitStatus
		for {
			select {
			case <-reapStop:
				return
			default:
			}
			entries, err := os.ReadDir("/proc")
			if err == nil {
				for _, e := range entries {
					pid, err := strconv.Atoi(e.Name())
					if err != nil || pid <= 1 || pid == old || pid == repl.Process.Pid {
						continue
					}
					if procState(pid) != 'Z' {
						continue
					}
					if pgrp, err := readPgrp(fmt.Sprintf("/proc/%d/stat", pid)); err == nil && pgrp == old {
						_, _ = syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
					}
				}
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	defer func() { close(reapStop); <-reapDone }()
	// The stop still kills the launch's own member and confirms, and the
	// replacement — on its own different number — is untouched.
	stopped, detail := a.Terminate()
	if !stopped || !strings.Contains(detail, "killed the launched process group") {
		t.Fatalf("the held group is stopped: stopped=%v %q", stopped, detail)
	}
	if err := syscall.Kill(repl.Process.Pid, 0); err != nil {
		t.Fatal("the unrelated replacement was killed by the stop")
	}
}

// procState is /proc/<pid>/stat field 3, the process state.
func procState(pid int) byte {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	stat := string(b)
	i := strings.LastIndexByte(stat, ')')
	if i < 0 || i+2 >= len(stat) {
		return 0
	}
	return stat[i+2]
}
