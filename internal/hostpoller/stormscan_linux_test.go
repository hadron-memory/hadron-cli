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
	"reflect"
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

// watcherSettleBound is how long a test waits for the drain watcher to settle after
// the stop's reap. A watcher that cannot settle within it is a FAILED test, never a
// logged residual. The watcher needs one clean membership pass, and on a host
// saturated with runnable processes that took 81 s (#2173, F2: saturation run of the
// frozen #2177 head), so the bound is generous like the other eventually-true waits
// in this package; only a watcher that never settles reaches it.
const watcherSettleBound = 200 * time.Second

// stopFixture installs the #1970 seams around one launch: every signal is
// recorded with its per-call ownership proof, and — for the storm shapes —
// every /proc listing the scan machinery sees carries one extra entry that
// is gone again at stat time, so every pass voids identically for every
// caller of the seam. The install registers its cleanup BEFORE the caller
// launches, and that cleanup runs on EVERY exit path in the gate's order:
// clear the atomic storm flag and set the settle-empty gate (the signal
// wrapper keeps forwarding the real function; the listing wrapper answers
// empty, so every listing the drain path sees is a provably drained
// group, cli#2951), drain/reap as needed through the production stop,
// wait for ACTUAL watcherDone closure on a finite failing deadline, and
// only then restore the global pointers. On a deadline miss the test
// fails with the wrappers still installed — no restoration races a live
// watcher — after un-gating the listing wrapper back to a pass-through,
// so the leftovers leak nothing behavioral into later tests. After the
// closure, one more scan over the real listing checks the gate did not
// blind the settle to a genuinely surviving member (positive evidence
// only; a voided pass proves nothing and is tolerated).
type stopFixture struct {
	storm      bool // wrap readProcDir with the void-exhaustion storm
	flipOnTerm bool // matrix 1: clear the storm after the first recorded TERM
	// killed is set at the first recorded SIGKILL (cli#2853: the modelled host clears its churn a
	// number of listings after it).
	killed atomic.Bool

	epoch    time.Time // monotonic base for the record/flip ordering proof
	storming atomic.Bool
	// settleEmpty, once set at settle, makes the wrapper's listings empty: a
	// provably drained group (cli#2951), so the quiet-scan confirm-and-reap the
	// settle performs is quiet by construction instead of hoping the host's
	// churn yields a clean pass inside the settle bound.
	settleEmpty atomic.Bool
	flipAt      atomic.Int64 // nanoseconds since epoch; 0 until the flip
	// cleanAfterFlip answers every post-flip listing as the fixture's own
	// synthetic clean one (this pid, a stat-readable non-member): a real
	// listing is voided by any process that vanishes inside it, and no
	// budget bounds another process's exit, so the flip's confirm is
	// deterministic on the loaded host instead (#3019; set with
	// withCleanAfterFlip).
	cleanAfterFlip bool
	settled        atomic.Bool
	// missedBound is set only on the settle-bound miss, where the wrappers
	// deliberately stay installed over a still-live watcher: an outer
	// cleanup must not restore the seam then (it would race that watcher).
	missedBound atomic.Bool

	realRead   func(string) ([]os.DirEntry, error) // captured at install (every shape, cli#2951); restored only after watcherDone closure
	realSignal func(int, syscall.Signal) error
	rec        signalRecorder
	// beforeSettle, when set, runs at the start of settle (cli#2954: lets a test observe what is still
	// installed at that moment).
	beforeSettle func()
	// failf is settle's failure sink, t.Fatalf by default. A pin that must observe settle
	// failing without dying by it (the wiring red control, cli#3016 round 4) swaps it in
	// beforeSettle and asserts the recorded failure: neutering settle's scan call then
	// empties the record and fails the pin.
	failf func(format string, args ...any)
}

// stormOption is one optional shape of newStormFixture's storm.
type stormOption func(*stopFixture)

// withCleanAfterFlip sets cleanAfterFlip: every post-flip listing is the
// fixture's synthetic clean one, for the finite→clean shapes whose confirm
// must not depend on the host's real /proc (#3019).
func withCleanAfterFlip() stormOption {
	return func(f *stopFixture) { f.cleanAfterFlip = true }
}

// newStormFixture installs both wrappers: the void-exhaustion storm over
// readProcDir and the recording signalGroup. flipOnTerm selects the
// finite→clean flip (matrix 1); false storms for the whole journey
// (matrix 3 and the ownership control's held-zombie row).
func newStormFixture(flipOnTerm bool, opts ...stormOption) *stopFixture {
	f := &stopFixture{storm: true, flipOnTerm: flipOnTerm, epoch: time.Now()}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// newJourneyFixture records signals with their ownership proof around an
// unstormed real-/proc journey (the matrix 4 tolerance in detach_unix_test
// delegates here). The listing wrapper is still installed — settle needs
// its empty gate (cli#2951) — but until then it is a transparent
// pass-through: the listing the journey scans stays the host's own.
func newJourneyFixture() *stopFixture {
	return &stopFixture{epoch: time.Now()}
}

func (f *stopFixture) install(t *testing.T) {
	t.Helper()
	f.failf = t.Fatalf
	f.realSignal = signalGroup
	signalGroup = func(pgid int, sig syscall.Signal) error {
		err := f.realSignal(pgid, sig) // the real signal completes FIRST:
		// a TERM provably went out before any scan could see a clean
		// listing (the contract's flip ordering).
		f.rec.record(pgid, sig, time.Since(f.epoch))
		if sig == syscall.SIGKILL {
			f.killed.Store(true)
		}
		if f.flipOnTerm && sig == syscall.SIGTERM && f.storming.CompareAndSwap(true, false) {
			f.flipAt.Store(int64(time.Since(f.epoch)))
		}
		return err
	}
	// cli#2951: the listing wrapper is installed for every fixture shape, not
	// only the storm. Until settle it is a transparent pass-through of the
	// host's own listing (the storm shapes add their vanishing entry on top);
	// settle flips settleEmpty below, and from that moment every listing the
	// drain path sees is empty — a provably drained group.
	f.realRead = readProcDir
	readProcDir = func(dir string) ([]os.DirEntry, error) {
		if f.settleEmpty.Load() {
			return nil, nil
		}
		if f.cleanAfterFlip && f.flipAt.Load() != 0 {
			// The synthetic clean listing the flip shape confirms on: one
			// entry that stats (this pid, never a member of the launched
			// group), so ambient churn cannot void the pass.
			return []os.DirEntry{fakeDirEntry{name: strconv.Itoa(os.Getpid())}}, nil
		}
		entries, err := f.realRead(dir)
		if err != nil {
			return nil, err
		}
		if f.storming.Load() {
			entries = append(entries, fakeDirEntry{name: abovePidMax})
		}
		return entries, nil
	}
	if f.storm {
		// cli#2853: the scan retries until a wall-clock deadline; a storm that never clears
		// would spend the production second on every probe, so the fixture uses a short one.
		oldDeadline, oldStop := groupScanDeadline, groupStopScanDeadline
		groupScanDeadline, groupStopScanDeadline = 20*time.Millisecond, 20*time.Millisecond
		t.Cleanup(func() { groupScanDeadline, groupStopScanDeadline = oldDeadline, oldStop })
		f.storming.Store(true)
	}
	t.Cleanup(func() { f.settle(t) })
}

// settle is the fixture's finite tail, in the gate's order: clear the
// atomic storm flag and set the settle-empty gate (the signal wrapper
// keeps forwarding the real function; the listing wrapper answers empty,
// so every listing the drain path sees is a provably drained group,
// cli#2951), drain what an unconfirmed stop left held (a no-op once
// confirmed), wait for actual watcherDone closure on the finite failing
// deadline, and only then restore the global pointers and retire the
// recorder. Once-guarded: the inline call a passing test makes and the
// deferred cleanup never double-run.
func (f *stopFixture) settle(t *testing.T) {
	t.Helper()
	if !f.settled.CompareAndSwap(false, true) {
		return
	}
	if f.beforeSettle != nil {
		f.beforeSettle()
	}
	f.storming.Store(false) // idempotent on every exit path: waiting
	// while the storm is on can never settle the watcher
	f.settleEmpty.Store(true) // cli#2951: settle drains over an empty listing,
	// never the host's real /proc — churn that outlasts the test cannot hold it
	var leakPgid int
	if a := f.rec.bound(); a != nil {
		_, _ = a.Terminate() // bounded: the no-op (true, "") once
		// confirmed, else one quiet-scan confirm-and-reap through the
		// production stop — waitOut reaps at most once either way
		select {
		case <-a.watcherDone:
		case <-time.After(watcherSettleBound):
			f.settleEmpty.Store(false) // un-gate before failing: the
			// abandoned wrapper must forward the real listing again, or
			// one timeout would leak an empty /proc into every later test
			f.missedBound.Store(true) // the watcher stays live: an outer
			// restore now would race it (the pin's own cleanup checks this)
			f.failf("the drain watcher did not settle after the stop's reap; the fixture's wrappers stay installed (transparent pass-throughs of the real functions)")
			return
		}
		leakPgid = a.pid
	}
	if f.realRead != nil {
		readProcDir = f.realRead
	}
	signalGroup = f.realSignal
	f.rec.stop()
	// cli#2951 review (blind, P2 ×2): the empty gate must not blind the
	// settle to a genuinely surviving member — before the gate, a real leak
	// held the watcher to the bound and failed the test. The check runs
	// LAST, after every restore: its failure sink (t.Fatalf unless a pin
	// records it) ends the test with nothing left installed to leak into
	// later tests.
	if leakPgid != 0 && f.confirmedDrainDisproved(leakPgid) {
		f.failf("settle confirmed a drain the listing disproves: a member of group %d survived the reap", leakPgid)
	}
}

// confirmedDrainDisproved is settle's post-closure leak scan (cli#2951):
// one pass over the captured real listing — NOT the gated global seam —
// answering whether a live, stat-readable member of the group survives
// the reap the settle just confirmed. Positive evidence only: a voided
// pass (ambient churn, a first-seen orphan zombie) proves nothing and is
// tolerated, so ambient churn cannot flake it. Pinned red-first by
// TestAConfirmedDrainTheListingDisprovesFailsTheScan.
func (f *stopFixture) confirmedDrainDisproved(pgid int) bool {
	found, _, _, _ := scanGroupOnce(pgid, pgid, nil, f.realRead)
	return found
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
// can prove the group empty. The confirm itself rides the fixture's
// synthetic clean listing (withCleanAfterFlip): this test pins the
// field-voiding mechanism — the hold, the one TERM, the truthful confirm
// — not the budget, which the host's ambient churn would void inside any
// grace (#3019 run 9; the cost dimension is the pin pair's, below).
// Red-first evidence in the PR: the same fixture run against the
// pre-#1970 strict asserts (detail=="" and signals==0,
// detach_unix_test.go) failed them on demand, reproducing the filed
// flake, before the truthful asserts below landed.
func TestStormVoidExhaustionHoldsThenOneTermConfirms(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	f := newStormFixture(true /* flip on the first TERM */, withCleanAfterFlip())
	f.install(t) // cleanup registered BEFORE the launch (gate ordering)
	// cli#3019: the confirm budget is derived, not fixed — measured through
	// the installed seam and handed to the launch through the same
	// ExecHost.TerminateGrace production configures.
	grace, _ := confirmBudgetAndWindow()
	a, err := ExecHost{TerminateGrace: grace}.Launch(context.Background(),
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
	if ea == nil {
		t.Fatal("the launch is bound to the recorder before the stop")
	}
	if ea.grace != grace {
		t.Fatalf("the derived budget reaches the stop through ExecHost.TerminateGrace: grace=%v configured=%v", ea.grace, grace)
	}
	stopped, detail := a.Terminate()
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
	// The real /proc beside the synthetic listing: a reap failure cannot be
	// masked by the fixture's own clean answer.
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(a.PID()))); !os.IsNotExist(err) {
		t.Fatalf("no /proc entry remains for the launch: %v", err)
	}
	// The deferred cleanup settles the watcher (bounded, failing) and
	// restores the seam pointers only after closure; nothing is asserted
	// here that depends on it.
}

// cli#3019: matrix 1's confirm budget was a fixed wall clock (300 ms), and
// that is the load sensitivity the issue filed. The confirm needs one clean
// membership pass after the TERM; under full-suite load a pass costs more —
// or churn voids passes for longer — than a fixed budget affords, so the
// ladder escalates (the filed shape: ~0.25 s passing standalone, ~1.44 s
// failing loaded — a walked ladder). The fix is deriving the budget from
// the measured pass cost; the two pins below make that deterministic in
// both directions.
//
// slowScanSleep models the loaded host's /proc cost: every listing served
// before the TERM costs at least this — the way a fork-stormed /proc does.
// It is load-bearing for the pins' arithmetic: it lifts the measured pass
// cost high enough that the window (a fifth of the derived budget)
// outlasts the fixed floor while the budget itself covers it.
const slowScanSleep = 150 * time.Millisecond

const (
	// confirmBudgetPasses is how many membership passes' worth of measured
	// cost the derived budget affords — the observed field window was worth
	// roughly fifteen passes.
	confirmBudgetPasses = 20
	// confirmBudgetFloor is the historical fixed budget, kept as the
	// clamp's floor so a quiet host's shape is unchanged.
	confirmBudgetFloor = 300 * time.Millisecond
	// confirmWindowDivisor keeps the pin window structurally below the
	// budget: window = budget/confirmWindowDivisor.
	confirmWindowDivisor = 5
)

// measureScanPassCost times one membership pass over the CURRENTLY installed
// listing seam — the same listing-plus-stat-per-entry walk a ladder attempt
// takes — and answers the worst of n timed passes. The pass's verdict is
// irrelevant (under the storm fixture it voids; only its cost is measured).
func measureScanPassCost(n int) time.Duration {
	worst := time.Duration(0)
	self := os.Getpid()
	for i := 0; i < n; i++ {
		start := time.Now()
		scanGroupOnce(self, self, nil, func(dir string) ([]os.DirEntry, error) { return readProcDir(dir) })
		if d := time.Since(start); d > worst {
			worst = d
		}
	}
	return worst
}

// confirmBudgetAndWindow derives the confirm budget and its pin window from
// ONE shared measurement of a membership pass through the currently
// installed seam, so both scale with what the host's /proc actually costs
// instead of a fixed wall clock the loaded host can outlast (#3019). The
// budget is confirmBudgetPasses passes' worth, clamped to
// [confirmBudgetFloor, defaultTerminateGrace] — a pass cost implying more
// than the production default grace is a host beyond what a stop should
// wait out, and the confirm lands at the first clean pass, so a large
// budget costs nothing when it is not needed. The window is
// budget/confirmWindowDivisor: structurally below the budget, so a grace
// of one budget always covers the window and the floor never does.
func confirmBudgetAndWindow() (budget, window time.Duration) {
	budget = confirmBudgetPasses * measureScanPassCost(3)
	if budget < confirmBudgetFloor {
		budget = confirmBudgetFloor
	}
	if budget > defaultTerminateGrace {
		budget = defaultTerminateGrace
	}
	return budget, budget / confirmWindowDivisor
}

// runSlowScanWindowLadder runs matrix 1's finite→clean shape under a
// modelled loaded host: every pre-TERM listing costs slowScanSleep (the
// fork-stormed /proc), and for a window after the TERM every listing also
// carries one entry that is gone again at stat time — the churn that voids
// every membership pass, the field flake's own condition, with zero real
// churn and zero new processes. The model installs UNDER the storm fixture
// (its wrappers layer on top), so the fixture's hold, recorder, flip and
// settle all keep working. The budget and its window come from ONE shared
// measurement taken after the install and before the launch, so the
// derivation sees the modelled slow seam; the grace is whatever graceFor
// picks from that budget — the ONLY thing the two pins differ on, so
// confirm-versus-escalate is attributable to the grace alone. The churn
// window opens and its close is ARMED at the actual SIGTERM (the first
// TERM alone, by CompareAndSwap — no pre-TERM scan can consume window
// time), and it closes at the earlier of budget/confirmWindowDivisor
// after the TERM or the first SIGKILL. That dual close keeps both pin
// directions deterministic despite the ladder's select randomness once
// the grace expires: the escalation's exact moment is not fixed, only its
// certain side of the window close.
func runSlowScanWindowLadder(t *testing.T, graceFor func(derived time.Duration) time.Duration) (*stopFixture, Agent, bool, string) {
	t.Helper()
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	f := newStormFixture(true /* the fixture's own storm still flips at the first TERM */)
	realRead, realSignal := readProcDir, signalGroup
	epoch := time.Now()
	var churnWindow time.Duration        // the window's width; set from the shared measurement below
	var termAt, windowClose atomic.Int64 // ns since epoch; 0 until the TERM arms them
	var churnOver atomic.Bool            // the KILL closes the window early
	// Registered before install so LIFO restores AFTER the fixture's settle,
	// and skipped when settle missed its bound and deliberately left the
	// wrappers installed over a still-live watcher (the sibling pins' guard).
	t.Cleanup(func() {
		if !f.missedBound.Load() {
			readProcDir, signalGroup = realRead, realSignal
		}
	})
	signalGroup = func(pgid int, sig syscall.Signal) error {
		err := realSignal(pgid, sig)
		switch sig {
		case syscall.SIGTERM:
			if termAt.CompareAndSwap(0, int64(time.Since(epoch))) { // the FIRST TERM alone arms the window
				windowClose.Store(termAt.Load() + int64(churnWindow))
			}
		case syscall.SIGKILL:
			churnOver.Store(true) // the escalation is proven; what follows must be able to confirm
		}
		return err
	}
	readProcDir = func(dir string) ([]os.DirEntry, error) {
		if at := termAt.Load(); at != 0 {
			if churnOver.Load() || (windowClose.Load() != 0 && time.Since(epoch) >= time.Duration(windowClose.Load())) {
				// The churn window is closed: a provably clean, immediate
				// listing (this test's own pid — a stat-read non-member);
				// the real /proc state of the launch is asserted beside it.
				return []os.DirEntry{fakeDirEntry{name: strconv.Itoa(os.Getpid())}}, nil
			}
			entries, err := realRead(dir)
			if err != nil {
				return nil, err
			}
			return append(entries, fakeDirEntry{name: abovePidMax}), nil // the churn: gone again at stat time
		}
		time.Sleep(slowScanSleep) // the loaded host's listing cost
		return realRead(dir)
	}
	f.install(t)                                    // layers ON TOP of the model: its captured "real" functions are the wrappers above
	budget, churnWindow := confirmBudgetAndWindow() // ONE shared measurement, through the modelled slow seam
	grace := graceFor(budget)
	a, err := ExecHost{TerminateGrace: grace}.Launch(context.Background(),
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
	return f, a, stopped, detail
}

// The #3019 pin, green direction: the derived confirm budget holds a slow
// post-TERM scan window the fixed wall clock lost. The window is the same
// shared budget/confirmWindowDivisor the red control runs under; the only
// difference is the grace, so the confirm here is attributable to the
// derived budget alone. The pair's mutant teeth are the red control's: a
// derivation that lets the window fall inside the fixed floor (the budget
// stubbed away, the model's cost floor deleted) fails THAT test with a
// confirm where the escalation belongs.
func TestTheDerivedConfirmBudgetHoldsASlowPostTermScan(t *testing.T) {
	f, a, stopped, detail := runSlowScanWindowLadder(t, func(derived time.Duration) time.Duration { return derived })
	ea := f.rec.bound()
	if !stopped {
		t.Fatalf("the derived budget confirms inside the churn window: stopped=false %q", detail)
	}
	want := fmt.Sprintf("terminated the launched process group (pid %d)", a.PID())
	if detail != want {
		t.Fatalf("the stop's own re-scan confirms after the window: detail=%q want %q", detail, want)
	}
	if n := f.rec.count(); n != 1 {
		t.Fatalf("exactly one signal under the derived budget rules out a KILL escalation: %d signals", n)
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
	// The real /proc beside the synthetic listing: a reap failure cannot be
	// masked by the model's own clean answer.
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(a.PID()))); !os.IsNotExist(err) {
		t.Fatalf("no /proc entry remains for the launch: %v", err)
	}
}

// The #3019 pin, red control: the fixed floor the slow window outlasts
// escalates — deterministically, because the window closes only at the
// earlier of budget/confirmWindowDivisor after the TERM (far past the
// floor) or the first SIGKILL, so no pre-KILL pass can confirm and the
// grace is spent without a confirm by construction. The KILL then closes
// the window, and the first membership call after it confirms: the filed
// ~1.44 s shape — SIGKILL sent, the killed-wording detail, every signal
// ownership-proven, the reap real (asserted against /proc beside the
// synthetic listing). This control passes on both commits and carries the
// pair's mutant teeth: a derivation that lets the window fall inside the
// floor fails here with a confirm where the escalation belongs.
func TestAFixedConfirmBudgetTheSlowScanOutlastsEscalates(t *testing.T) {
	f, a, stopped, detail := runSlowScanWindowLadder(t, func(time.Duration) time.Duration { return confirmBudgetFloor })
	if !stopped {
		t.Fatalf("the walked ladder still confirms after its KILL: stopped=false %q", detail)
	}
	want := fmt.Sprintf("killed the launched process group (pid %d) after it ignored SIGTERM for %s", a.PID(), confirmBudgetFloor)
	if detail != want {
		t.Fatalf("the fixed budget escalates under the window it cannot cover: detail=%q want %q", detail, want)
	}
	if n := f.rec.count(); n != 2 {
		t.Fatalf("the churn window outlasting the fixed budget escalates to exactly TERM then KILL: %d signals", n)
	}
	for i := 0; i < f.rec.count(); i++ {
		if s := f.rec.at(i); s.proof != "" {
			t.Fatalf("signal %v not ownership-proven: %s", s.sig, s.proof)
		}
	}
	ea := f.rec.bound()
	if !ea.reaped.Load() || !ea.confirmed.Load() {
		t.Fatalf("the walked ladder's own reap completed: reaped=%v confirmed=%v", ea.reaped.Load(), ea.confirmed.Load())
	}
	// The real /proc beside the synthetic listing: a reap failure cannot be
	// masked by the model's own clean answer.
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(a.PID()))); !os.IsNotExist(err) {
		t.Fatalf("no /proc entry remains for the launch: %v", err)
	}
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
	// cli#2853: an unconfirmed stop says WHY the group could not be proven empty (a code, never
	// /proc text): here every pass is voided by a listed pid that vanished, churn, not a member.
	want := fmt.Sprintf("the launched process group (pid %d) did not exit within %s of SIGKILL, nor in a final scan of up to %s (last membership scan: %s)", a.PID(), postKillWait, groupStopScanDeadline, whyUnseenPid)
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
	exhaust := fmt.Sprintf("the launched process group (pid %d) did not exit within %s of SIGKILL, nor in a final scan of up to %s", pid, postKillWait, groupStopScanDeadline)
	// cli#2853: the exhaustion wording may carry the code of the last membership scan.
	exhaustWithCode := func(d string) bool {
		for _, w := range []drainWhy{whyLiveMember, whyUnseenPid, whyVanishedMember, whyLateFork, whyUnreadable} {
			if d == exhaust+" (last membership scan: "+string(w)+")" {
				return true
			}
		}
		return d == exhaust
	}
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
		if !exhaustWithCode(detail) {
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
	hostRead := readProcDir // the journey's own proof that settle restores
	// the seam: the wrapper every shape now installs (cli#2951) must be
	// gone again once the ladder's inline settle has closed the watcher.
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
	if reflect.ValueOf(readProcDir).Pointer() != reflect.ValueOf(hostRead).Pointer() {
		t.Fatal("the journey settle did not restore the listing seam: a wrapper was left installed after watcherDone closure")
	}
}

// cli#2951: a host whose ambient churn outlasts the test. The vanisher here
// sits UNDER the fixture's wrapper and never clears — the shape seen in the
// wild with no suite load at all (the issue's 3 QA runs): every pass voids,
// the watcher holds by design (a wrong reap is worse than a held zombie),
// and a settle that drains over the host's real /proc can run out the whole
// settle bound waiting for a clean pass that never comes. The fixture's
// settle gate makes its own listing empty, so the quiet-scan confirm-and-
// reap settle already assumes is quiet by construction. The pin fails on
// the ungated fixture at the full settle bound; with the gate it settles
// within a couple of watcher ticks, and the churn listing under the wrapper
// serves no listing the drain path takes after settle has begun. The
// counter counts only uncovered calls — a listing already in flight when
// settle flips the gate must not fail the pin — so at most the ONE
// straddling listing of the single watcher goroutine lands after the
// settle snapshot; the ungated failure mode is a listing per watcher tick
// for the whole bound, not one straddle.
func TestSettleDrainsDespiteChurnThatOutlastsTheTest(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	f := newJourneyFixture()
	// Never-clearing churn, installed BEFORE the fixture so it sits under the
	// fixture's wrapper: the journey body sees the real /proc plus one entry
	// that is always gone again at stat time.
	hostRead := readProcDir
	var listings atomic.Int64
	readProcDir = func(dir string) ([]os.DirEntry, error) {
		entries, err := hostRead(dir)
		if err != nil {
			return nil, err
		}
		if !f.settleEmpty.Load() {
			listings.Add(1) // count only the calls the gate did not cover
		}
		return append(entries, fakeDirEntry{name: abovePidMax}), nil
	}
	// registered first: restored last, after the fixture's settle — and
	// skipped entirely when the fixture failed at its bound, where the
	// wrappers must stay installed over the still-live watcher (a restore
	// under it is exactly the pointer race the fixture's contract forbids).
	t.Cleanup(func() {
		if !f.missedBound.Load() {
			readProcDir = hostRead
		}
	})
	// The ladder's own waits stay short (the sibling pins' convention): this
	// pin is about settle, not the stop's patience. Swapped BEFORE install so
	// the restore runs after the fixture's settle, which then waits under the
	// same short deadlines the body ran with.
	oldPost, oldStop, oldScan := postKillWait, groupStopScanDeadline, groupScanDeadline
	postKillWait, groupStopScanDeadline, groupScanDeadline = 100*time.Millisecond, 200*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { postKillWait, groupStopScanDeadline, groupScanDeadline = oldPost, oldStop, oldScan })
	var atSettle int64
	f.beforeSettle = func() { atSettle = listings.Load() }
	f.install(t)
	a, err := ExecHost{TerminateGrace: 50 * time.Millisecond}.Launch(context.Background(),
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
	if !f.settled.Load() {
		t.Fatal("the ladder did not settle the fixture inline: the listing counts below are meaningless without it")
	}
	if got := listings.Load(); got > atSettle+1 {
		t.Fatalf("settle consulted the churn listing after it began: %d listings at settle, %d after (one in-flight listing may straddle the flip)", atSettle, got)
	}
}

// The red control for settle's post-closure leak scan (cli#2951 round-2
// local P3: the scan had evidence but no pin — neutering it left the
// package green). It pins the HELPER's verdict; the wiring — settle's
// call of the helper — is pinned by the companion control below
// (round-4: one finding from all three seats). Two fed shapes over one
// launch: a listing whose extra entry stats as a LIVE member of the
// launch's own group must disprove the confirmed drain, and the same
// entry stat-vanishing — the ambient churn shape — must not (a voided
// pass proves nothing). The shapes are served only once the gate is set,
// exactly the world the scan sees: the gated watcher drained over empty
// while the listing under the wrapper held the disproof.
func TestAConfirmedDrainTheListingDisprovesFailsTheScan(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	f := newJourneyFixture()
	hostRead := readProcDir
	readProcDir = func(dir string) ([]os.DirEntry, error) {
		entries, err := hostRead(dir)
		if err != nil {
			return nil, err
		}
		return append(entries, fakeDirEntry{name: abovePidMax}), nil
	}
	t.Cleanup(func() {
		if !f.missedBound.Load() {
			readProcDir = hostRead
		}
	})
	realStat := readGroupStatFn
	var liveMember atomic.Bool
	readGroupStatFn = func(path string) (int, byte, int, error) {
		if liveMember.Load() && strings.HasSuffix(path, "/"+abovePidMax+"/stat") {
			if a := f.rec.bound(); a != nil {
				return a.pid, 'R', 1, nil // a live member of the launch's own group
			}
		}
		return realStat(path)
	}
	// The same missedBound contract as the listing restore above (round-4
	// blind M1, the one line the round-2 fix missed): on a bound miss the
	// abandoned watcher still stats through this seam, and restoring the
	// pointer underneath it is the race the guard exists to prevent.
	t.Cleanup(func() {
		if !f.missedBound.Load() {
			readGroupStatFn = realStat
		}
	})
	f.install(t)
	// Round-4 blind M3: a first-assertion failure must not leave the member
	// live for the deferred settle's own scan — that second failure would
	// pile a misleading "survived the reap" on the real one. Registered
	// after install so it runs BEFORE the fixture's settle.
	t.Cleanup(func() { liveMember.Store(false) })
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
	// The moment the scan runs at: gate set, storming clear — settle's own
	// world after it has confirmed and reaped.
	f.storming.Store(false)
	f.settleEmpty.Store(true)

	liveMember.Store(true)
	if !f.confirmedDrainDisproved(a.PID()) {
		t.Fatal("a live, stat-readable member of the group must disprove the confirmed drain")
	}
	liveMember.Store(false)
	if f.confirmedDrainDisproved(a.PID()) {
		t.Fatal("a voided pass (the entry vanishing at stat time) proves nothing and must not disprove the drain")
	}
	// The shapes were fed without a stop: settle the held launch for real
	// (its inline ladder already ran above only as far as Done), through
	// the production path the fixture provides.
	stopped, detail := a.Terminate()
	assertTruthfulStopLadder(t, f, a, stopped, detail)
}

// The wiring red control for the same scan (round-4: the same finding
// from all three seats — blind M2, local P3, QA F1 — the control above
// pins the helper's verdict, not settle's call of it, and forcing the
// call site's condition false left the whole package green). The same
// listing and the same stat-live member, but fed through the production
// path: the ladder settles the fixture inline, and settle must FAIL on
// the disproof. The failure is observed, not taken — beforeSettle swaps
// settle's sink for a recorder — so this pin asserts the Fatalf instead
// of dying by it, and neutering the call site empties the record.
func TestSettleFailsOnAListingThatDisprovesTheConfirmedDrain(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	f := newJourneyFixture()
	hostRead := readProcDir
	readProcDir = func(dir string) ([]os.DirEntry, error) {
		entries, err := hostRead(dir)
		if err != nil {
			return nil, err
		}
		return append(entries, fakeDirEntry{name: abovePidMax}), nil
	}
	t.Cleanup(func() {
		if !f.missedBound.Load() {
			readProcDir = hostRead
		}
	})
	realStat := readGroupStatFn
	readGroupStatFn = func(path string) (int, byte, int, error) {
		// Live exactly in the world the scan runs in — once the gate is
		// set — so the ladder's own confirmations still see the entry
		// stat-void and pass; only the post-closure scan meets it alive.
		if f.settleEmpty.Load() && strings.HasSuffix(path, "/"+abovePidMax+"/stat") {
			if a := f.rec.bound(); a != nil {
				return a.pid, 'R', 1, nil // a live member of the launch's own group
			}
		}
		return realStat(path)
	}
	t.Cleanup(func() {
		if !f.missedBound.Load() {
			readGroupStatFn = realStat
		}
	})
	var failures []string
	f.beforeSettle = func() {
		f.failf = func(format string, args ...any) {
			failures = append(failures, fmt.Sprintf(format, args...))
		}
	}
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
	// The moment the scan runs at: gate set, storming clear — settle's own
	// world after it has confirmed and reaped, pre-set exactly as the
	// sibling control above pre-sets it (the ladder then confirms over the
	// gated empty listing instead of grinding production deadlines against
	// the churn; the disproof lives in the listing the scan reads
	// directly, under the wrapper).
	f.storming.Store(false)
	f.settleEmpty.Store(true)
	stopped, detail := a.Terminate()
	assertTruthfulStopLadder(t, f, a, stopped, detail)
	if !f.settled.Load() {
		t.Fatal("the ladder did not settle the fixture inline: the scan's call site was never reached")
	}
	if len(failures) != 1 || !strings.Contains(failures[0], "survived the reap") {
		t.Fatalf("settle must fail on a listing that disproves the confirmed drain: recorded %v", failures)
	}
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}
