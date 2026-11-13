//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
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
	ea.waitWatchers()
}

// A kernel or policy that refuses waitid takes the NO-HOLD contract: the
// waiter reaps the leader at its exit (outside the stop mutex), and a stop
// of a LIVE launch signals the group only while the leader is live, then
// reports the stop UNCONFIRMED at the first sight of the exit and keeps
// every record — it never waits on the reap, never holds the stop mutex
// across a blocking wait, and never reaches for a number the launch may no
// longer own (#1474; the polling hold this replaces blocked Terminate in
// cmd.Wait under the mutex when a Z-looking leader still had live
// threads, and the gate does not need a confirmed stop on a host that
// exotic — the coordinator's disposition for the round-7 delta).
func TestLiveLaunchIsStoppableWhenTheHoldFails(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
	}
	realWait := waitLeaderExit
	defer func() { waitLeaderExit = realWait }()
	waitLeaderExit = func(int) (leaderExit, error) {
		return leaderExit{kind: unknownStatus}, syscall.ENOSYS
	}
	signals := 0
	realSignal := signalGroup
	defer func() { signalGroup = realSignal }()
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals++
		return realSignal(pgid, sig)
	}
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sleep", "30"}, Dir: t.TempDir(), Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ea := a.(*execAgent)
	defer stopAndWait(t, a)
	if ea.exited.Load() {
		t.Fatal("a live leader is not exited: the probe oracle depends on it")
	}
	waitReaperEntry(t, ea)
	stopped := false
	detail := ""
	terminated := make(chan struct{})
	go func() { stopped, detail = a.Terminate(); close(terminated) }()
	select {
	case <-terminated:
	case <-time.After(15 * time.Second):
		t.Fatal("Terminate never returned for a live launch whose hold failed")
	}
	if stopped {
		t.Fatalf("a stop without the ownership hold is never confirmed: stopped=%v %q", stopped, detail)
	}
	if !strings.Contains(detail, "unconfirmed") || signals == 0 {
		t.Fatalf("the stop signals while the leader is live and then reports unconfirmed: signals=%d %q", signals, detail)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter finishes after the stop ends the launch")
	}
	if got := a.ExitDetail(); !strings.Contains(got, "signal: terminated") {
		t.Fatalf("the finalized detail is the leader's real death: %q", got)
	}
}

// The refused-waitid launch that is ALREADY GONE when the stop arrives
// still confirms, through the one oracle that stays sound after the
// number went public: an ESRCH probe answer means no process group holds
// the number at all, so nothing was signalled and nothing of the launch
// runs. This is the only confirmation a no-hold launch's stop can reach
// (#1474) — the mid-stop authority abort in the test above owns every
// other case.
func TestARefusedWaitidConfirmsOnlyAnAlreadyGoneLaunch(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	signals, probes := 0, 0
	realWait, realSignal, realProbe := waitLeaderExit, signalGroup, probeGroup
	defer func() { waitLeaderExit, signalGroup, probeGroup = realWait, realSignal, realProbe }()
	waitLeaderExit = func(int) (leaderExit, error) {
		return leaderExit{kind: unknownStatus}, syscall.ENOSYS
	}
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals++
		return realSignal(pgid, sig)
	}
	probeGroup = func(pgid int) error {
		probes++
		return realProbe(pgid)
	}
	a, err := ExecHost{}.Launch(context.Background(), LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	defer stopAndWait(t, a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once; the reaping waiter notices within the interval")
	}
	ea := a.(*execAgent)
	if !ea.reaped.Load() || ea.noHold.Load() != true {
		t.Fatal("the refused-waitid waiter reaps the leader at its exit and records the missing hold")
	}
	stopped, detail := a.Terminate()
	if !stopped || detail != "" {
		t.Fatalf("a launch whose group is already gone confirms silently: stopped=%v %q", stopped, detail)
	}
	if signals != 0 || probes != 1 {
		t.Fatalf("the already-gone launch is probed once and never signalled: %d probes, %d signals", probes, signals)
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
// IT and nothing reaps it. That is exactly the subreaper world the scan is
// written for: a zombie member cannot run and keeps the number allocated
// until reaped, so it does not count as a member while draining, and the
// stop confirms with the orphan's zombie still on the books — the launch's
// own records release the leader only after the whole group stopped.
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
	// The stop still kills the launch's own member and confirms — with the
	// killed member's orphan zombie still present under PID 1, which the
	// scan must not count as a member — and the replacement, on its own
	// different number, is untouched.
	stopped, detail := a.Terminate()
	if !stopped || !strings.Contains(detail, "killed the launched process group") {
		t.Fatalf("the held group is stopped: stopped=%v %q", stopped, detail)
	}
	if err := syscall.Kill(repl.Process.Pid, 0); err != nil {
		t.Fatal("the unrelated replacement was killed by the stop")
	}
	if ea := a.(*execAgent); ea.reaped.Load() {
		ea.waitWatchers()
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

// stopAndWait terminates the launch and waits out the waiter's watcher
// decision, so no drain watcher a test armed survives into a later test
// that swaps the package vars it reads through. The wait is bounded: an
// unsettled channel after a Terminate means the waiter itself never
// finished (a leader stuck in uninterruptible IO), and no watcher can be
// running then — holdExit arms it — so there is nothing left to leak.
func stopAndWait(t *testing.T, a Agent) {
	t.Helper()
	_, _ = a.Terminate()
	select {
	case <-a.(*execAgent).watcherDone:
	case <-time.After(10 * time.Second):
		t.Log("the waiter's watcher decision did not settle; no watcher can be running")
	}
}

// A held exit's detail is the leader's REAL outcome, read from the hold's
// waitid answer at Done time — not a placeholder about the group's shape
// (#1474: the operator reading the exit reason must see the leader's own
// status even while members keep the group alive and no stop ever comes).
func TestHeldExitCarriesTheLeadersRealExit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sh", "-c", `sleep 30 & exit 7`}, Dir: t.TempDir(), Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stopAndWait(t, a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the leader must exit at once")
	}
	if got := a.ExitDetail(); got != "exit status 7" {
		t.Fatalf("ExitDetail() = %q, want the leader's real exit status", got)
	}
	ea := a.(*execAgent)
	if ea.reaped.Load() {
		t.Fatal("a member still runs: the leader stays held")
	}
	stopped, detail := a.Terminate()
	if !stopped || !strings.Contains(detail, "launched process group") {
		t.Fatalf("the held group is still stopped: stopped=%v %q", stopped, detail)
	}
	if got := a.ExitDetail(); got != "exit status 7" {
		t.Fatalf("the stop's reap reports the same exit: %q", got)
	}
}

// A leader that DIES BY A SIGNAL reports the signal through the waitid
// answer itself — si_code CLD_KILLED with si_status the signal — the same
// word a reaping wait would report. The leader keeps a member alive, so
// the zombie is still HELD when the detail is read: this pins the siginfo
// decode (and its per-architecture si_status offset) on the shape an
// empty-group reap would bypass, where the reap's own wait would supply
// the wording instead of the hold's answer (#1474).
func TestHeldExitReportsASignalDeath(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sh", "-c", `sleep 30 & kill -KILL $$`}, Dir: t.TempDir(), Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stopAndWait(t, a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the leader must kill itself at once")
	}
	held := a.ExitDetail()
	if held != "signal: killed" {
		t.Fatalf("ExitDetail() = %q, want the leader's signal death read from the held answer", held)
	}
	// The stop's own reap must report the SAME death: the hold's decode
	// and the reap's wait agree on the word, whatever the kernel did with
	// the core.
	stopped, _ := a.Terminate()
	if !stopped {
		t.Fatal("the member-bearing group still stops")
	}
	if got := a.ExitDetail(); got != held {
		t.Fatalf("the stop's reap reports a different death than the hold's answer: %q vs %q", got, held)
	}
}

// A core-dumping death renders with the dump marker, matching what a
// reaping wait's own wording would say for the same death. The dump
// marker is the kernel's decision (a host may refuse cores); the pin is
// the AGREEMENT between the hold's decode and the reap's report, read
// before and after the reap of the same leader (#1474).
func TestHeldExitReportsACoreDumpingDeath(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sh", "-c", `ulimit -c unlimited 2>/dev/null; sleep 30 & kill -ABRT $$`}, Dir: t.TempDir(), Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stopAndWait(t, a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the leader must kill itself at once")
	}
	held := a.ExitDetail()
	if !strings.HasPrefix(held, "signal: aborted") {
		t.Fatalf("ExitDetail() = %q, want the leader's abort death", held)
	}
	stopped, _ := a.Terminate()
	if !stopped {
		t.Fatal("the member-bearing group still stops")
	}
	if got := a.ExitDetail(); got != held {
		t.Fatalf("the stop's reap reports a different death than the hold's answer: %q vs %q", got, held)
	}
}

// The siginfo si_status offset is derived from the build's word size, not
// hardcoded: 24 on a 64-bit build, 20 on a 32-bit one. The held-exit pins
// above prove the derivation end to end on the running build; this pins
// the constant the derivation must land on per word size.
func TestSiginfoStatusOffsetIsDerivedFromWordSize(t *testing.T) {
	ptr := int(unsafe.Sizeof(uintptr(0)))
	want := map[int]int{4: 20, 8: 24}[ptr]
	if want == 0 {
		t.Skipf("no pinned offset for word size %d", ptr)
	}
	union := (12 + ptr - 1) &^ (ptr - 1)
	if union+8 != want {
		t.Fatalf("si_status offset = %d, want %d for word size %d", union+8, want, ptr)
	}
}

// The held zombie is reaped once the group empties ON ITS OWN: a launch
// whose member outlives its leader by a moment must not keep a zombie (and
// with it the group's number) until a Terminate that may never come — one
// leaked zombie per such launch in a long-running poller (#1474). The
// watcher's reap uses the waiter's own proof (a clean empty scan), so the
// later stop confirms silently, touching nothing.
func TestHeldZombieIsReapedWhenTheGroupDrainsAlone(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	signals, probes := 0, 0
	realSignal, realProbe := signalGroup, probeGroup
	defer func() { signalGroup, probeGroup = realSignal, realProbe }()
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals++
		return realSignal(pgid, sig)
	}
	probeGroup = func(pgid int) error {
		probes++
		return realProbe(pgid)
	}
	a, err := ExecHost{}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sh", "-c", `sleep 1 & exit 3`}, Dir: t.TempDir(), Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stopAndWait(t, a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the leader must exit at once")
	}
	ea := a.(*execAgent)
	deadline := time.Now().Add(10 * time.Second)
	for !ea.reaped.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the held zombie is never reaped after the group drains on its own")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ea.emptyAtReap.Load() {
		t.Fatal("the watcher's reap is an empty-scan reap: a later stop confirms from it")
	}
	if got := a.ExitDetail(); got != "exit status 3" {
		t.Fatalf("the watcher's reap finalizes the same exit: %q", got)
	}
	stopped, detail := a.Terminate()
	if !stopped || detail != "" {
		t.Fatalf("a drained-then-reaped launch is a silent confirmed stop: stopped=%v %q", stopped, detail)
	}
	if signals != 0 || probes != 0 {
		t.Fatalf("a number that went public at the watcher's reap is never touched: %d probes, %d signals", probes, signals)
	}
}

// startGroupWithMembers runs a session leader running script and returns the
// command: the leader's pid IS the group id, and its members are its
// background children, unreaped by it for as long as it lives.
func startGroupWithMembers(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// spawnGroupMember starts a child of the TEST process in process group
// leader — leader 0 starts a fresh group at the child's own pid — staying
// in this session, so setpgid is permitted. A shell cannot hold the
// unreaped-member shape this file needs — a shell reaps its background
// children from inside its wait for the foreground job, so the corpse is
// gone in microseconds — so a member whose parent does not reap it is
// built here: this test process never waits on the child until cleanup,
// which is exactly the subreaper world's leftover zombie.
func spawnGroupMember(t *testing.T, leader int, argv ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: leader}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// groupMemberStates returns the states of the group's members other than
// the leader itself.
func groupMemberStates(leader int) []byte {
	var states []byte
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == leader {
			continue
		}
		pgrp, state, _, err := readGroupStat(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil || pgrp != leader {
			continue
		}
		states = append(states, state)
	}
	return states
}

func hasState(states []byte, want byte) bool {
	for _, s := range states {
		if s == want {
			return true
		}
	}
	return false
}

// A group whose only remaining member is a ZOMBIE reads as drained: a
// zombie cannot run, and it keeps the number allocated until reaped, so
// treating it as drained can never open reuse — while counting it made a
// stop under a subreaper poller (the container world, where the poller is
// PID 1 and never reaps orphans) permanently unconfirmable (#1474).
func TestZombieMemberDoesNotKeepAGroupUndrained(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
	}
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	// A live group leader (a child of this test in its own process GROUP,
	// not a setsid session — a foreign session's group cannot be joined)
	// holding two members placed straight into its group: one running, one
	// already dead and never reaped by its parent (this test) — the zombie
	// member.
	leader := spawnGroupMember(t, 0, "sleep", "30")
	runner := spawnGroupMember(t, leader.Process.Pid, "sleep", "30")
	dead := spawnGroupMember(t, leader.Process.Pid, "true")
	defer func() {
		for _, c := range []*exec.Cmd{leader, runner, dead} {
			if c.Process != nil {
				_ = c.Process.Kill()
				_, _ = c.Process.Wait()
			}
		}
	}()
	lg := leader.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for !hasState(groupMemberStates(lg), 'Z') {
		if time.Now().After(deadline) {
			t.Fatal("the zombie member never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A running member still reads as a member...
	if !groupHasOtherMembers(lg, lg) {
		t.Fatal("a group with a running member is not drained")
	}
	// ...and once only zombies remain, the group reads as drained.
	if err := syscall.Kill(-lg, syscall.SIGKILL); err != nil {
		t.Fatalf("killing the group's running members: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for func() bool {
		states := groupMemberStates(lg)
		return len(states) == 0 || hasNonZombie(states)
	}() {
		if time.Now().After(deadline) {
			t.Fatal("the running members never drained")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if groupHasOtherMembers(lg, lg) {
		t.Fatal("a group whose only other members are zombies reads as drained")
	}
}

func hasNonZombie(states []byte) bool {
	for _, s := range states {
		if s != 'Z' {
			return true
		}
	}
	return false
}

// zLookingMemberWithThreads reports whether the group holds a member in
// state Z whose thread count is ABOVE one — the pthread_exit shape, whose
// thread-group leader has exited while its other threads still run.
func zLookingMemberWithThreads(leader int) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == leader {
			continue
		}
		pgrp, state, threads, err := readGroupStat(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil || pgrp != leader {
			continue
		}
		if state == 'Z' && threads > 1 {
			return true
		}
	}
	return false
}

// A member whose thread-group leader has exited but whose other threads
// still run shows state Z with num_threads > 1: the process can still act,
// so the scan counts it as a live member instead of reading a group whose
// tree escaped as drained (#1474, delta round 6, Jonas P2-B — pthread_exit
// leaves a running process behind a Z-looking /proc entry).
func TestAZombieLookingMultithreadedMemberKeepsTheGroupLive(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	leader := spawnGroupMember(t, 0, "sleep", "30")
	member := spawnGroupMember(t, leader.Process.Pid, "python3", "-c",
		"import threading,time,ctypes\n"+
			"threading.Thread(target=lambda: time.sleep(30)).start()\n"+
			"ctypes.pythonapi.PyThread_exit_thread()")
	defer func() {
		for _, c := range []*exec.Cmd{leader, member} {
			if c.Process != nil {
				_ = c.Process.Kill()
				_, _ = c.Process.Wait()
			}
		}
	}()
	lg := leader.Process.Pid
	deadline := time.Now().Add(10 * time.Second)
	for !zLookingMemberWithThreads(lg) {
		if time.Now().After(deadline) {
			t.Fatal("the Z-looking multithreaded member never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !groupHasOtherMembers(lg, lg) {
		t.Fatal("a member whose threads still run was read as drained")
	}
}

// waitReaperEntry waits until the no-hold waiter has ENTERED its blocking
// reap — the reaped flag flips as the waiter starts its wait, possibly
// long before the leader exits.
func waitReaperEntry(t *testing.T, ea *execAgent) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ea.reaped.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the no-hold waiter never entered its blocking reap")
		}
		time.Sleep(time.Millisecond)
	}
}

// zLeaderThreadShape waits for the launch LEADER to reach the exited-main-
// thread shape: /proc state Z with a thread count above one — the group
// leader itself, not a member, still able to act through its threads
// (#1474). It reports the pid it was given for the caller's assertions.
func zLeaderThreadShape(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		state, f, err := readProcStat(fmt.Sprintf("/proc/%d/stat", pid))
		if err == nil && state == 'Z' && len(f) > 17 {
			if threads, _ := strconv.Atoi(f[17]); threads > 1 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the Z-looking multithreaded leader never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The LAUNCH LEADER in the Z-looking multithreaded shape is not treated
// as the drainable held zombie either: its threads still run, so the stop
// must SIGNAL the group (a Z leader alone is not proof its whole thread
// group left) and may confirm only once nothing of the launch answers
// any more (#1474 — the stop that confirmed with ZERO signals while the
// leader's live thread kept running was the round-7 defect; the leader
// gets the same predicate the member shape already had).
func TestAZombieLeaderWithLiveThreadsIsSignalledAndOnlyThenConfirmed(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	signals := 0
	realSignal := signalGroup
	defer func() { signalGroup = realSignal }()
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals++
		return realSignal(pgid, sig)
	}
	a, err := ExecHost{TerminateGrace: time.Second}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"python3", "-c",
			"import threading,time,ctypes\n" +
				"threading.Thread(target=lambda: time.sleep(30)).start()\n" +
				"ctypes.pythonapi.PyThread_exit_thread()"},
		Dir: t.TempDir(), Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stopAndWait(t, a)
	zLeaderThreadShape(t, a.PID())
	stopped, detail := a.Terminate()
	if signals == 0 {
		t.Fatalf("a leader whose threads still run is never confirmed without signalling: stopped=%v %q", stopped, detail)
	}
	if !stopped {
		t.Fatalf("the signalled group still stops: stopped=%v %q", stopped, detail)
	}
	// A confirmed stop leaves nothing of the launch: the leader is reaped
	// (gone from /proc), not parked as a zombie with live threads.
	if _, _, err := readProcStat(fmt.Sprintf("/proc/%d/stat", a.PID())); err == nil {
		t.Fatal("a confirmed stop leaves the leader on the books")
	}
}

// The same Z-looking multithreaded leader under a REFUSED waitid: the
// stop must return PROMPTLY and unconfirmed — the no-hold contract never
// waits on the leader's reap, and nothing holds the stop mutex across the
// waiter's blocking wait (#1474: the polling hold this replaces called
// the reap under the mutex and blocked Terminate until an external KILL).
func TestARefusedWaitidDoesNotBlockOnAZombieLeader(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	realWait := waitLeaderExit
	defer func() { waitLeaderExit = realWait }()
	waitLeaderExit = func(int) (leaderExit, error) {
		return leaderExit{kind: unknownStatus}, syscall.ENOSYS
	}
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"python3", "-c",
			"import threading,time,ctypes\n" +
				"threading.Thread(target=lambda: time.sleep(30)).start()\n" +
				"ctypes.pythonapi.PyThread_exit_thread()"},
		Dir: t.TempDir(), Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Kill(-a.PID(), syscall.SIGKILL)
		select {
		case <-a.Done():
		case <-time.After(5 * time.Second):
		}
	}()
	ea := a.(*execAgent)
	waitReaperEntry(t, ea)
	zLeaderThreadShape(t, a.PID())
	stopped := false
	detail := ""
	terminated := make(chan struct{})
	go func() { stopped, detail = a.Terminate(); close(terminated) }()
	select {
	case <-terminated:
	case <-time.After(10 * time.Second):
		t.Fatal("Terminate blocked on a Z-looking leader whose threads still run")
	}
	if stopped || !strings.Contains(detail, "unconfirmed") {
		t.Fatalf("the no-hold stop of a live-threaded leader is prompt and unconfirmed: stopped=%v %q", stopped, detail)
	}
}

// fakeDirEntry is a /proc listing entry that names a pid and nothing else.
type fakeDirEntry struct{ name string }

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return false }
func (f fakeDirEntry) Type() fs.FileMode          { return 0 }
func (f fakeDirEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }

// A listing in which an entry VANISHES before its stat read proves nothing:
// the member that forked after the listing and exited before the read is
// gone from between the two, and its child — forked before the parent
// vanished — first appears in the NEXT listing. A vanishing pass must be
// retried, and only a whole pass with nothing vanishing may answer "empty"
// (#1474: the double-fork daemonise pattern reads as an empty, escaped,
// group otherwise).
func TestScanRescansWhenAListedEntryVanishes(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	cmd := startGroupWithMembers(t, `sleep 30 & sleep 30`)
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	leader := cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for len(groupMemberStates(leader)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the running members never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
	realRead := readProcDir
	defer func() { readProcDir = realRead }()
	calls := 0
	readProcDir = func(dir string) ([]os.DirEntry, error) {
		calls++
		if calls == 1 {
			// One pass listing only a pid that is not there when its stat
			// is read: without the rescan this pass would answer "empty".
			return []os.DirEntry{fakeDirEntry{name: "99999999"}}, nil
		}
		return realRead(dir)
	}
	if !groupHasOtherMembers(leader, leader) {
		t.Fatal("a vanishing pass is not an empty group: the scan retried and found the live member")
	}
	if calls != 2 {
		t.Fatalf("a vanishing pass is retried exactly once here: %d listings", calls)
	}
}

// A member that forked after the listing and only THEN became Z is still
// listed — nothing vanished — but its new child, born after the listing,
// is absent from that pass: reading such a quiet-Z pass as empty let the
// waiter reap while the child ran. A Z member seen for the FIRST time
// voids the pass; only a Z STABLE across two consecutive passes — its
// children were born before its death, so any that run appear in the
// second listing — may answer "empty" (#1474, delta round 6, Jane D6).
// Deterministic: both listings are frozen to the real, stable zombie's
// pid, so the only void available to the first pass is that its Z is new.
func TestAZFirstSeenInAPassVoidsItUntilStable(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
	}
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	leader := spawnGroupMember(t, 0, "sleep", "30")
	dead := spawnGroupMember(t, leader.Process.Pid, "true")
	defer func() {
		for _, c := range []*exec.Cmd{leader, dead} {
			if c.Process != nil {
				_ = c.Process.Kill()
				_, _ = c.Process.Wait()
			}
		}
	}()
	lg := leader.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for !hasState(groupMemberStates(lg), 'Z') {
		if time.Now().After(deadline) {
			t.Fatal("the zombie member never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
	realRead := readProcDir
	defer func() { readProcDir = realRead }()
	calls := 0
	readProcDir = func(dir string) ([]os.DirEntry, error) {
		calls++
		// Both passes list ONLY the zombie member: its stat read succeeds
		// (nothing vanishes), so pass 1's void can come from its Z being
		// new alone, and pass 2 sees the same Z again — stable.
		return []os.DirEntry{fakeDirEntry{name: strconv.Itoa(dead.Process.Pid)}}, nil
	}
	if groupHasOtherMembers(lg, lg) {
		t.Fatal("a Z stable across two passes does not keep the group undrained")
	}
	if calls != 2 {
		t.Fatalf("a pass whose only member Z is new is voided and retried exactly once: %d listings", calls)
	}
}

// The double-fork daemonise shape end to end: the leader's child forks a
// grandchild and exits at once; the grandchild keeps the group alive. The
// scan that decides the waiter's reap must read the group as having
// members — the leader stays held, its real exit is the detail, and the
// stop still reaches the grandchild (#1474).
func TestADoubleForkIsNotReadAsAnEmptyGroup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sh", "-c", `sh -c 'sleep 30 &' & exit 0`}, Dir: t.TempDir(), Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stopAndWait(t, a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the leader must exit at once")
	}
	ea := a.(*execAgent)
	if ea.reaped.Load() || ea.emptyAtReap.Load() {
		t.Fatal("the grandchild keeps the group alive: the leader must stay held, not reaped-on-empty")
	}
	if got := a.ExitDetail(); got != "exit status 0" {
		t.Fatalf("the held exit's detail is the leader's real exit: %q", got)
	}
	stopped, detail := a.Terminate()
	if !stopped || !strings.Contains(detail, "launched process group") {
		t.Fatalf("the escaped grandchild is still stopped: stopped=%v %q", stopped, detail)
	}
}
