//go:build linux

package hostpoller

// The #1970 load-sensitive flake, made deterministic. The traced mechanism
// (issue #1970): under a parallel full-suite fork storm, /proc entries
// vanish between a scan's listing and its stat read often enough that
// every one of a scan's bounded passes voids, and groupHasOtherMembers
// then answers its fail-safe "members" — so a leader-alone launch is HELD
// (unreaped zombie, watcher armed) instead of reaped-at-exit, and a later
// stop signals the launch's own held group once before confirming. The
// fixture below reproduces that void-exhaustion on demand — zero real
// churn, zero new processes — and the tests pin the truthful behavior on
// every side of it (contract chain on the issue: 6840318504 → 6840554091
// → 6844483508 → 6844497474 → 6844647665 → 6844747819; gate 6844801636).
//
// Design facts this file leans on (settled at #1817's merge 87dcd12e):
//   - stop() holds a.mu across terminateGroup, its confirmation AND its
//     reap (detach_unix.go); watchDrain may scan outside the mutex but
//     must take the same mutex before reaping and re-checks reaped under
//     it — so a watcher can never steal an in-flight stop's reap, and no
//     interval pin is needed anywhere here. drainWatchInterval is never
//     swapped by these tests: the watcher runs at the real 200 ms
//     throughout.
//   - Every scan — the stop's and the watcher's alike — goes through the
//     readProcDir seam, so one atomic flag shapes them both.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// abovePidMax is a synthetic /proc entry name above the kernel pid_max
// ceiling (4,194,304 default), so the name can never name a real process
// and its void comes from the real stat read returning fs.ErrNotExist —
// the same fakeDirEntry pattern reapgroup_linux_test.go established.
const abovePidMax = "99999999"

// watcherSettleBound is the suite's established watcher deadline (the same
// bound stopAndWait and TestHeldZombieIsReapedWhenTheGroupDrainsAlone
// use). A watcher that cannot settle within it is a FAILED test, never a
// logged residual: after the storm clears, the next tick after a reap is
// at most one real 200 ms interval away.
const watcherSettleBound = 10 * time.Second

// stopFixture installs the #1970 seams around one launch: every signal is
// recorded with its per-call ownership proof, and — for the storm shapes —
// every /proc listing the scan machinery sees carries one extra entry that
// is gone again at stat time, so every pass voids identically for every
// caller of the seam. The install registers its cleanup BEFORE the caller
// launches, and that cleanup runs on EVERY exit path in the gate's order:
// clear only the atomic storm flag (the installed wrappers then forward
// the real functions — transparent pass-throughs), drain/reap as needed
// through the production stop, wait for ACTUAL watcherDone closure on a
// finite failing deadline, and only then restore the global pointers. On a
// deadline miss the test fails with the wrappers still installed: no
// restoration races a live watcher, and with storming cleared the
// leftovers leak nothing behavioral into later tests.
type stopFixture struct {
	storm      bool // wrap readProcDir with the void-exhaustion storm
	flipOnTerm bool // matrix 1: clear the storm after the first recorded TERM

	epoch    time.Time // monotonic base for the record/flip ordering proof
	storming atomic.Bool
	flipAt   atomic.Int64 // nanoseconds since epoch; 0 until the flip
	settled  atomic.Bool

	realRead   func(string) ([]os.DirEntry, error) // nil unless storm
	realSignal func(int, syscall.Signal) error
	rec        signalRecorder
}

// newStormFixture installs both wrappers: the void-exhaustion storm over
// readProcDir and the recording signalGroup. flipOnTerm selects the
// finite→clean flip (matrix 1); false storms for the whole journey
// (matrix 3 and the ownership control's held-zombie row).
func newStormFixture(flipOnTerm bool) *stopFixture {
	return &stopFixture{storm: true, flipOnTerm: flipOnTerm, epoch: time.Now()}
}

// newJourneyFixture records signals with their ownership proof around an
// unstormed real-/proc journey (the matrix 4 tolerance in detach_unix_test
// delegates here). readProcDir is never touched: there is no storm to
// shape, so the listing stays the host's own.
func newJourneyFixture() *stopFixture {
	return &stopFixture{epoch: time.Now()}
}

func (f *stopFixture) install(t *testing.T) {
	t.Helper()
	f.realSignal = signalGroup
	signalGroup = func(pgid int, sig syscall.Signal) error {
		err := f.realSignal(pgid, sig) // the real signal completes FIRST:
		// a TERM provably went out before any scan could see a clean
		// listing (the contract's flip ordering).
		f.rec.record(pgid, sig, time.Since(f.epoch))
		if f.flipOnTerm && sig == syscall.SIGTERM && f.storming.CompareAndSwap(true, false) {
			f.flipAt.Store(int64(time.Since(f.epoch)))
		}
		return err
	}
	if f.storm {
		f.realRead = readProcDir
		readProcDir = func(dir string) ([]os.DirEntry, error) {
			entries, err := f.realRead(dir)
			if err != nil {
				return nil, err
			}
			if f.storming.Load() {
				entries = append(entries, fakeDirEntry{name: abovePidMax})
			}
			return entries, nil
		}
		f.storming.Store(true)
	}
	t.Cleanup(func() { f.settle(t) })
}

// settle is the fixture's finite tail, in the gate's order: clear only the
// atomic storm flag (the installed wrappers then forward the real
// functions — transparent pass-throughs), drain what an unconfirmed stop
// left held (a no-op once confirmed), wait for actual watcherDone closure
// on the finite failing deadline, and only then restore the global
// pointers and retire the recorder. Once-guarded: the inline call a
// passing test makes and the deferred cleanup never double-run.
func (f *stopFixture) settle(t *testing.T) {
	t.Helper()
	if !f.settled.CompareAndSwap(false, true) {
		return
	}
	f.storming.Store(false) // idempotent on every exit path: waiting
	// while the storm is on can never settle the watcher
	if a := f.rec.bound(); a != nil {
		_, _ = a.Terminate() // bounded: the no-op (true, "") once
		// confirmed, else one quiet-scan confirm-and-reap through the
		// production stop — waitOut reaps at most once either way
		select {
		case <-a.watcherDone:
		case <-time.After(watcherSettleBound):
			t.Fatalf("the drain watcher did not settle after the stop's reap; the fixture's wrappers stay installed (transparent pass-throughs of the real functions)")
			return
		}
	}
	if f.realRead != nil {
		readProcDir = f.realRead
	}
	signalGroup = f.realSignal
	f.rec.stop()
}

// recordedSignal is one signalGroup call as it happened, with the
// monotonic time it was recorded at (relative to the fixture's epoch) and
// the per-call ownership proof's verdict — "" exactly when the signal
// provably reached only the launch's own held group.
type recordedSignal struct {
	pgid  int
	sig   syscall.Signal
	at    time.Duration
	proof string
}

// signalRecorder wraps signalGroup's recording side. It never touches
// *testing.T: recorders must stay safe outside a failed test's lifetime,
// so failures are recorded here and asserted in the test body, and calls
// arriving after stop() (a wrapper a failed test left installed) are
// dropped, never panicked on.
type signalRecorder struct {
	mu      sync.Mutex
	agent   atomic.Pointer[execAgent]
	stopped bool
	sigs    []recordedSignal
}

func (r *signalRecorder) bind(a Agent) {
	if ea, ok := a.(*execAgent); ok {
		r.agent.Store(ea)
	}
}

func (r *signalRecorder) bound() *execAgent { return r.agent.Load() }

func (r *signalRecorder) record(pgid int, sig syscall.Signal, at time.Duration) {
	a := r.agent.Load()
	proof := "a signal arrived before any launch was bound to the recorder"
	if a != nil {
		proof = signalOwnershipError(a, pgid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.sigs = append(r.sigs, recordedSignal{pgid: pgid, sig: sig, at: at, proof: proof})
}

func (r *signalRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sigs)
}

func (r *signalRecorder) at(i int) recordedSignal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sigs[i]
}

func (r *signalRecorder) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
}

// signalOwnershipError is the per-signal ownership proof (contract §2),
// evaluated at call time so it is synchronized with the signal itself. It
// returns "" exactly when the signal provably reached only the launch's
// own held group: the hold established (Done closed, not yet reaped), the
// signalled pgid the launch's own pid, and that pid still the UNREAPED
// zombie — the on-the-books corpse is what keeps the number provably the
// launch's (a reaped-and-reused number loses its /proc entry or its Z).
func signalOwnershipError(a *execAgent, pgid int) string {
	select {
	case <-a.done:
	default:
		return fmt.Sprintf("signalled (pgid %d) before the launch's Done was closed", pgid)
	}
	if a.reaped.Load() {
		return fmt.Sprintf("signalled (pgid %d) after the hold was gone: reaped already true", pgid)
	}
	if pgid != a.pid {
		return fmt.Sprintf("signalled pgid %d, the launch's own group is %d", pgid, a.pid)
	}
	_, state, _, err := readGroupStat(filepath.Join("/proc", strconv.Itoa(a.pid), "stat"))
	if err != nil || state != 'Z' {
		return fmt.Sprintf("the launch pid %d is not an unreaped zombie at signal time (state %q, err %v)", a.pid, state, err)
	}
	return ""
}

// The discriminating control for the ownership proof (contract §2): each
// fed false shape must FAIL the proof, naming which part — and the one
// true shape (a genuinely held zombie) must pass it. Red-first evidence in
// the PR: with signalOwnershipError sabotaged to return "" unconditionally
// every wrong-shape row failed this control before the real proof ran.
func TestSignalOwnershipProofRejectsEveryFalseShapeItIsFed(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	held := func(pid int) *execAgent { // Done closed, not reaped: the hold shape
		a := &execAgent{pid: pid, done: closedChan()}
		return a
	}
	reapedHold := func(pid int) *execAgent {
		a := held(pid)
		a.reaped.Store(true)
		return a
	}
	// A genuinely reaped pid — the established dead-leader shape: its
	// /proc entry is gone, so a signal reaching that number reached
	// whatever took it next, or nothing.
	dead := exec.Command("true")
	if err := dead.Start(); err != nil {
		t.Skip("cannot spawn true")
	}
	_ = dead.Wait()
	rows := []struct {
		name     string
		a        *execAgent
		pgid     int
		mustName string
	}{
		{"a live launch, Done not yet closed", &execAgent{pid: os.Getpid(), done: make(chan struct{})}, os.Getpid(), "before the launch's Done was closed"},
		{"the hold already gone, reaped", reapedHold(os.Getpid()), os.Getpid(), "after the hold was gone"},
		{"a pgid that is not the launch's group", held(os.Getpid()), os.Getpid() + 1, "the launch's own group is"},
		{"a reaped number with no /proc entry", held(dead.Process.Pid), dead.Process.Pid, "not an unreaped zombie at signal time"},
		{"a live replacement owning the number", held(os.Getpid()), os.Getpid(), "not an unreaped zombie at signal time"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			err := signalOwnershipError(row.a, row.pgid)
			if err == "" {
				t.Fatalf("the ownership proof must reject: %s", row.name)
			}
			if !strings.Contains(err, row.mustName) {
				t.Fatalf("the failure must name which part failed: %q (want %q)", err, row.mustName)
			}
		})
	}

	// The one true shape: a genuinely held zombie of a real launch. The
	// storm keeps holdExit's own scan void-exhausting, so the leader stays
	// held — Done closed, unreaped, its zombie stat-readable.
	f := newStormFixture(false)
	f.install(t) // cleanup registered before the launch (gate ordering)
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	f.rec.bind(a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	if err := signalOwnershipError(f.rec.bound(), a.PID()); err != "" {
		t.Fatalf("the ownership proof must hold for a genuinely held zombie: %s", err)
	}
}

// Matrix 1 — the storm regression, finite→clean. The fixture holds the
// launch (every scan voids), the stop sends exactly one TERM to its own
// held group — ownership-proven at call time — and the first TERM flips
// the storm off, so the stop's own bounded re-scan is the first scan that
// can prove the group empty: the confirm is deterministic. Red-first
// evidence in the PR: the same fixture run against the pre-#1970 strict
// asserts (detail=="" and signals==0, detach_unix_test.go) failed them on
// demand, reproducing the filed flake, before the truthful asserts below
// landed.
func TestStormVoidExhaustionHoldsThenOneTermConfirms(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	f := newStormFixture(true /* flip on the first TERM */)
	f.install(t) // cleanup registered BEFORE the launch (gate ordering)
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	f.rec.bind(a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	stopped, detail := a.Terminate()
	ea := f.rec.bound()
	if !stopped {
		t.Fatalf("the void-exhausted hold still stops truthfully: stopped=false %q", detail)
	}
	want := fmt.Sprintf("terminated the launched process group (pid %d)", a.PID())
	if detail != want {
		t.Fatalf("the stop's own re-scan confirms after the flip: detail=%q want %q", detail, want)
	}
	if n := f.rec.count(); n != 1 {
		t.Fatalf("exactly one signal after the flip rules out a KILL escalation: %d signals", n)
	}
	s := f.rec.at(0)
	if s.sig != syscall.SIGTERM || s.proof != "" {
		t.Fatalf("the one TERM is ownership-proven: sig=%v proof=%q", s.sig, s.proof)
	}
	if flip := time.Duration(f.flipAt.Load()); flip == 0 || s.at > flip {
		t.Fatalf("the flip lands only after the recorded TERM: term at %v, flip at %v", s.at, flip)
	}
	if !ea.reaped.Load() || !ea.confirmed.Load() {
		t.Fatalf("the stop's own reap completed: reaped=%v confirmed=%v", ea.reaped.Load(), ea.confirmed.Load())
	}
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(a.PID()))); !os.IsNotExist(err) {
		t.Fatalf("no /proc entry remains for the launch: %v", err)
	}
	// The deferred cleanup settles the watcher (bounded, failing) and
	// restores the seam pointers only after closure; nothing is asserted
	// here that depends on it.
}

// Matrix 3 — the forever-inconclusive negative control. The storm never
// clears during the body, so the stop cannot prove the group empty inside
// its bounded waits: it walks the whole ladder truthfully and KEEPS ITS
// RECORDS — never converting inconclusive exhaustion into success — and
// the cleanup then ends the case as clean as the success case.
func TestAForeverInconclusiveScanStopsUnconfirmedAndCleansUp(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	f := newStormFixture(false /* never flips */)
	f.install(t)
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	f.rec.bind(a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	ea := f.rec.bound()
	stopped, detail := a.Terminate()
	if stopped {
		t.Fatalf("a scan that could never decide is not a success: stopped=true %q", detail)
	}
	want := fmt.Sprintf("the launched process group (pid %d) did not exit within %s of SIGKILL", a.PID(), postKillWait)
	if detail != want {
		t.Fatalf("the bounded ladder's exhaustion wording: detail=%q want %q", detail, want)
	}
	if n := f.rec.count(); n > 2 {
		t.Fatalf("the source bounds the ladder at one TERM and one KILL: %d signals", n)
	}
	sigs := map[syscall.Signal]bool{}
	for i := 0; i < f.rec.count(); i++ {
		s := f.rec.at(i)
		sigs[s.sig] = true
		if s.proof != "" {
			t.Fatalf("signal %v not ownership-proven: %s", s.sig, s.proof)
		}
	}
	if f.rec.count() == 2 && (!sigs[syscall.SIGTERM] || !sigs[syscall.SIGKILL]) {
		t.Fatalf("two signals are exactly TERM then KILL: %v", sigs)
	}
	if ea.confirmed.Load() || ea.reaped.Load() {
		t.Fatalf("records are kept on an unconfirmed stop: confirmed=%v reaped=%v", ea.confirmed.Load(), ea.reaped.Load())
	}
	if _, state, _, err := readGroupStat(filepath.Join("/proc", strconv.Itoa(a.PID()), "stat")); err != nil || state != 'Z' {
		t.Fatalf("the zombie is still held after the unconfirmed stop: state=%q err=%v", state, err)
	}
	// The cleanup tail, run inline so its end state is asserted: clear
	// only the atomic storm flag (the wrapper then forwards the real
	// listing), drain/reap through the production stop, wait for actual
	// watcherDone closure on the finite failing deadline, only then
	// restore the pointers. The deferred copy no-ops.
	f.settle(t)
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(a.PID()))); !os.IsNotExist(err) {
		t.Fatalf("the negative control ends as clean as the success case: %v", err)
	}
}

// Matrix 2 — the quiet positive, scan-only: a stable listing whose every
// entry is genuinely stat-read and none is a member proves empty IN ONE
// PASS. The complementary directions (a retained real member answers
// not-empty; a vanishing or first-seen-Z entry voids the pass) are already
// pinned by TestScanRescansWhenAListedEntryVanishes and
// TestAZFirstSeenInAPassVoidsItUntilStable — cited, not duplicated.
func TestAStableListingOfNonMembersProvesEmptyInOnePass(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	entries := []os.DirEntry{fakeDirEntry{name: strconv.Itoa(os.Getpid())}}
	if _, _, _, err := readGroupStat(filepath.Join("/proc", strconv.Itoa(os.Getppid()), "stat")); err == nil {
		entries = append(entries, fakeDirEntry{name: strconv.Itoa(os.Getppid())})
	}
	realRead := readProcDir
	defer func() { readProcDir = realRead }()
	readProcDir = func(string) ([]os.DirEntry, error) { return entries, nil }
	// The scan target is above pid_max: no real process can hold it as a
	// process group, so the entries — the test and its parent, live for
	// the test's duration — are provably not its members.
	if groupHasOtherMembers(99999998, 99999998) {
		t.Fatal("stat-read non-members do not make the target group non-empty")
	}
}

// assertTruthfulStopLadder is the Linux tolerance for the UNFIXED
// real-/proc post-exit journey (matrix 4): whatever the host's real churn
// does, the outcome is one of the settled ladder's exact wordings, every
// signal is ownership-proven, and an unconfirmed stop keeps its records.
// Success is never converted out of indecision — the FIT's truthfulness
// constraint (6840273523). The tail settles the journey exactly like the
// fixture cases: drain, finite failing watcher wait, only then restore.
func assertTruthfulStopLadder(t *testing.T, f *stopFixture, a Agent, stopped bool, detail string) {
	t.Helper()
	ea, ok := a.(*execAgent)
	if !ok {
		t.Fatalf("the journey is a real launch, not a %T", a)
	}
	pid := a.PID()
	confirm := map[string]bool{
		"": true,
		fmt.Sprintf("terminated the launched process group (pid %d)", pid):                                       true,
		fmt.Sprintf("killed the launched process group (pid %d) after it ignored SIGTERM for %s", pid, ea.grace): true,
	}
	exhaust := fmt.Sprintf("the launched process group (pid %d) did not exit within %s of SIGKILL", pid, postKillWait)
	if stopped {
		if !confirm[detail] {
			t.Fatalf("a confirmed stop carries one of the settled confirm wordings: %q", detail)
		}
		if !ea.reaped.Load() || !ea.confirmed.Load() {
			t.Fatalf("a confirmed stop completed its reap: reaped=%v confirmed=%v", ea.reaped.Load(), ea.confirmed.Load())
		}
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); !os.IsNotExist(err) {
			t.Fatalf("no /proc entry remains after a confirmed stop: %v", err)
		}
	} else {
		if detail != exhaust {
			t.Fatalf("an unconfirmed stop carries the exhaustion wording and keeps records: %q", detail)
		}
		if ea.confirmed.Load() || ea.reaped.Load() {
			t.Fatalf("records are kept on an unconfirmed stop: confirmed=%v reaped=%v", ea.confirmed.Load(), ea.reaped.Load())
		}
		if _, state, _, err := readGroupStat(filepath.Join("/proc", strconv.Itoa(pid), "stat")); err != nil || state != 'Z' {
			t.Fatalf("the zombie is still held after an unconfirmed stop: state=%q err=%v", state, err)
		}
	}
	if n := f.rec.count(); n > 2 {
		t.Fatalf("the source bounds the ladder at one TERM and one KILL: %d signals", n)
	}
	seen := map[syscall.Signal]bool{}
	for i := 0; i < f.rec.count(); i++ {
		s := f.rec.at(i)
		seen[s.sig] = true
		if s.proof != "" {
			t.Fatalf("signal %v to pgid %d not ownership-proven: %s", s.sig, s.pgid, s.proof)
		}
	}
	if f.rec.count() == 2 && (!seen[syscall.SIGTERM] || !seen[syscall.SIGKILL]) {
		t.Fatalf("two signals are exactly TERM then KILL: %v", seen)
	}
	f.settle(t)
}

// journeyLadderApplies reports whether this platform's post-exit journey
// asserts the #1970 ladder tolerance; runPostExitJourneyLadder then runs
// that journey. Linux says yes here; the !linux stub beside this file says
// no, and detach_unix_test.go keeps the settled strict baseline verbatim.
func journeyLadderApplies() bool { return true }

// runPostExitJourneyLadder is the matrix 4 Linux branch: the original
// post-exit journey (a real `true`, its group gone, stop after Done) under
// the truthful ladder — on a quiet host exactly the old strict shape
// ("" and zero signals), and under the churn this issue filed, one of the
// ladder's wordings with every signal ownership-proven.
func runPostExitJourneyLadder(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	f := newJourneyFixture()
	f.install(t)
	a, err := ExecHost{}.Launch(context.Background(),
		LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	f.rec.bind(a)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	stopped, detail := a.Terminate()
	assertTruthfulStopLadder(t, f, a, stopped, detail)
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}
