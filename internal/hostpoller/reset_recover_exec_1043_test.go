package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"testing"
	"time"
)

// recResets answers ReadReset with a fixed reset and records each step
// with the epoch, reason and lateness it was sent with. stepErr fails the
// named step.
type recResets struct {
	fakeResets
	read    ContextReset
	readErr error
	stepErr map[string]error
	// refuse answers the named step with a refusal that ended the reset.
	refuse map[string]string
}

func (f *recResets) ReadReset(context.Context, string) (ContextReset, error) {
	f.calls = append(f.calls, "read")
	return f.read, f.readErr
}
func (f *recResets) ReportResetAgentStopped(_ context.Context, _ string, epoch int, late time.Duration) (ResetStep, error) {
	f.calls = append(f.calls, fmt.Sprintf("stopped@%d late=%s", epoch, late))
	return ResetStep{}, f.stepErr["stopped"]
}
func (f *recResets) ReportResetRelaunched(_ context.Context, _ string, epoch int, p ProcessIdentity) (ResetStep, error) {
	f.calls = append(f.calls, fmt.Sprintf("relaunched@%d pid=%d", epoch, p.PID))
	return ResetStep{Refused: f.refuse["relaunched"]}, f.stepErr["relaunched"]
}
func (f *recResets) ReportResetRelaunchFailed(_ context.Context, _ string, epoch int, reason string) (ResetStep, error) {
	f.calls = append(f.calls, fmt.Sprintf("relaunch-failed@%d %s", epoch, reason))
	return ResetStep{}, f.stepErr["relaunch-failed"]
}

// recProc observes processes by pid: a pid in live answers with that start
// time, one in errs fails with that error, any other is gone (ESRCH).
type recProc struct {
	live      map[int]string
	errs      map[int]error
	signalled int
}

func (p *recProc) Observe(pid int) (ProcessHandle, error) {
	if err, ok := p.errs[pid]; ok {
		return nil, err
	}
	st, ok := p.live[pid]
	if !ok {
		return nil, fmt.Errorf("pidfd_open %d: %w", pid, syscall.ESRCH)
	}
	return &recHandle{p: p, id: ProcessIdentity{PID: pid, StartTime: st}}, nil
}
func (p *recProc) Now() time.Duration { return 0 }

type recHandle struct {
	p  *recProc
	id ProcessIdentity
}

func (h *recHandle) Identity() ProcessIdentity { return h.id }
func (h *recHandle) SignalWithin(syscall.Signal, time.Duration, time.Duration, func() time.Duration) (time.Duration, error) {
	h.p.signalled++
	return 0, nil
}
func (h *recHandle) Close() {}

type recoverFixture struct {
	p      *Poller
	srv    *fakeServer
	host   *fakeHost
	resets *recResets
	proc   *recProc
	rec    ResetRecord
}

const checkpointNode = "hrn:node:acme.com:iris:sheds:auto-1"

// newRecoverFixture is a poller restarted after collecting r1: the agent
// it launched is no longer in memory (only its worktree is recorded), and
// r1's record names the old agent (4242, start "1") and the checkpoint.
// The relaunch would start pid 5151.
func newRecoverFixture(t *testing.T, phase string) *recoverFixture {
	t.Helper()
	x, host := relaunchFixture(t)
	x.p.mu.Lock()
	delete(x.p.agents, "id-iris")
	x.p.mu.Unlock()
	rs := &recResets{read: ContextReset{ID: "r1", Phase: phase, Epoch: 2, SessionID: "s1", TaskID: "t1", WorkerID: "id-iris"}}
	proc := &recProc{live: map[int]string{5151: "2"}, errs: map[int]error{}}
	x.p.EnableResets(rs, ResetDeps{Proc: proc, Git: x.p.resetDeps.Git, Idle: x.p.resetDeps.Idle})
	// Epoch 0: the restart came before the claim's epoch was written.
	rec := ResetRecord{ResetID: "r1", OperationID: "op1", WorkerID: "id-iris", Epoch: 0, Old: oldIdentity, Checkpoint: checkpointNode}
	if err := x.p.state.recordReset(rec); err != nil {
		t.Fatal(err)
	}
	return &recoverFixture{p: x.p, srv: x.srv, host: host, resets: rs, proc: proc, rec: rec}
}

func (x *recoverFixture) oldAlive() { x.proc.live[4242] = "1" }
func (x *recoverFixture) set(f func(*ResetRecord)) {
	_ = x.p.state.updateReset("r1", f)
}

// run delivers r1's operation again, as a restarted poller's collect does.
func (x *recoverFixture) run(t *testing.T) {
	t.Helper()
	x.p.register(context.Background()) //nolint:errcheck // fills workerRefs
	x.p.carryOut(context.Background(), resetOp())
	if x.proc.signalled != 0 {
		t.Fatalf("recovery signalled a process %d times", x.proc.signalled)
	}
}

func (x *recoverFixture) kept() bool {
	for _, r := range x.p.state.inFlightResets() {
		if r.ResetID == "r1" {
			return true
		}
	}
	return false
}

func (x *recoverFixture) wantSteps(t *testing.T, want ...string) {
	t.Helper()
	got := x.resets.calls
	if len(got) != len(want)+1 || got[0] != "read" {
		t.Fatalf("steps %v, want read then %v", got, want)
	}
	for i, w := range want {
		if got[i+1] != w {
			t.Fatalf("steps %v, want read then %v", got, want)
		}
	}
}

func (x *recoverFixture) wantReport(t *testing.T, outcome, code string) {
	t.Helper()
	reps := x.srv.finalReports()
	if len(reps) != 1 || reps[0].outcome != outcome {
		t.Fatalf("reports %+v, want one %s", reps, outcome)
	}
	if code != "" {
		wantReason(t, reps[0], code)
	}
	if x.kept() {
		t.Fatal("the record outlived its final report")
	}
}

func (x *recoverFixture) wantNoLaunch(t *testing.T) {
	t.Helper()
	if n := len(x.host.launches); n != 0 {
		t.Fatalf("recovery launched %d agent(s)", n)
	}
}

func TestRecoverAgentStoppedRelaunches1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.run(t)
	if len(x.host.launches) != 1 {
		t.Fatalf("launches %d, want one", len(x.host.launches))
	}
	// The epoch is the one the server holds for a live reset.
	x.wantSteps(t, "relaunched@2 pid=5151")
	x.wantReport(t, Succeeded, "")
	if env := x.host.promptEnvs[0]; !containsStr(env, "HADRON_RECOVERY_NODE="+checkpointNode) {
		t.Fatalf("recover prompt env %v lacks the recorded checkpoint", env)
	}
}

func TestRecoverAgentStoppedOldAliveIsProcessChanged1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.oldAlive()
	x.run(t)
	x.wantNoLaunch(t)
	x.wantSteps(t)
	x.wantReport(t, Failed, "PROCESS_CHANGED")
}

// A pid reused by another process is the old agent gone, not alive.
func TestRecoverReusedPidIsGone1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.proc.live[4242] = "77"
	x.run(t)
	if len(x.host.launches) != 1 {
		t.Fatalf("launches %d, want one", len(x.host.launches))
	}
}

// An old agent that cannot be observed for any reason but ESRCH counts as
// alive: recovery never relaunches next to an agent it cannot rule out.
func TestRecoverUnobservableOldCountsAsAlive1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.proc.errs[4242] = fmt.Errorf("pidfd_open 4242: %w", syscall.EPERM)
	x.run(t)
	x.wantNoLaunch(t)
	x.wantReport(t, Failed, "PROCESS_CHANGED")
}

func TestRecoverStopGrantedOldAliveIsBlocked1043(t *testing.T) {
	x := newRecoverFixture(t, "stop-granted")
	x.oldAlive()
	x.run(t)
	x.wantNoLaunch(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantReport(t, Failed, "RELAUNCH_BLOCKED")
}

func TestRecoverStopGrantedOldGoneReportsStoppedThenRelaunches1043(t *testing.T) {
	x := newRecoverFixture(t, "stop-granted")
	x.run(t)
	// No lateness is claimed: 0 is sent as an omitted lateStopMs.
	x.wantSteps(t, "stopped@2 late=0s", "relaunched@2 pid=5151")
	x.wantReport(t, Succeeded, "")
}

// A relaunch that began before the restart is never started again: two
// agents in one worktree is worse than a failed reset.
func TestRecoverNeverRelaunchesTwice1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.set(func(r *ResetRecord) { r.Relaunching = true })
	x.run(t)
	x.wantNoLaunch(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_UNVERIFIED")
	x.wantReport(t, Failed, "RELAUNCH_UNVERIFIED")
}

// The relaunch is marked on disk before the launch.
func TestRecoverMarksTheRelaunchBeforeLaunching1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.host.launchErr = errors.New("no such file")
	var marked bool
	x.host.beforeLaunch = func() {
		for _, r := range x.p.state.inFlightResets() {
			marked = marked || r.Relaunching
		}
	}
	x.run(t)
	if !marked {
		t.Fatal("the relaunch was not recorded before the launch")
	}
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantReport(t, Failed, "RELAUNCH_BLOCKED")
}

func TestRecoverWithoutACheckpointIsBlocked1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.set(func(r *ResetRecord) { r.Checkpoint = "" })
	x.run(t)
	x.wantNoLaunch(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantReport(t, Failed, "RELAUNCH_BLOCKED")
}

func TestRecoverRelaunchedAliveSucceeds1043(t *testing.T) {
	x := newRecoverFixture(t, "relaunched")
	x.set(func(r *ResetRecord) { r.New = &ProcessIdentity{PID: 5151, StartTime: "2"} })
	x.run(t)
	x.wantNoLaunch(t)
	x.wantSteps(t)
	x.wantReport(t, Succeeded, "")
}

// A relaunched agent that cannot be observed is not proven alive.
func TestRecoverRelaunchedUnobservableIsUnverified1043(t *testing.T) {
	x := newRecoverFixture(t, "relaunched")
	x.set(func(r *ResetRecord) { r.New = &ProcessIdentity{PID: 5151, StartTime: "2"} })
	x.proc.errs[5151] = fmt.Errorf("pidfd_open 5151: %w", syscall.EPERM)
	x.run(t)
	x.wantReport(t, Failed, "RELAUNCH_UNVERIFIED")
}

func TestRecoverRelaunchedWithoutANewIdentityIsUnverified1043(t *testing.T) {
	x := newRecoverFixture(t, "relaunched")
	x.run(t)
	x.wantReport(t, Failed, "RELAUNCH_UNVERIFIED")
}

func TestRecoverClaimedFailsTheCheckpoint1043(t *testing.T) {
	x := newRecoverFixture(t, "claimed")
	x.oldAlive()
	x.run(t)
	x.wantNoLaunch(t)
	x.wantSteps(t, "relaunch-failed@2 CHECKPOINT_FAILED")
	x.wantReport(t, Failed, "CHECKPOINT_FAILED")
}

func TestRecoverRequestedReportsOnly1043(t *testing.T) {
	x := newRecoverFixture(t, "requested")
	x.oldAlive()
	x.run(t)
	x.wantSteps(t)
	x.wantReport(t, Failed, "PROCESS_CHANGED")
}

func TestRecoverEndedReportsOnce1043(t *testing.T) {
	x := newRecoverFixture(t, "failed")
	x.resets.read.Reason = "CLAIM_LAPSED"
	x.run(t)
	x.wantSteps(t)
	x.wantReport(t, Failed, "CLAIM_LAPSED")

	y := newRecoverFixture(t, "done")
	y.run(t)
	y.wantSteps(t)
	y.wantReport(t, Succeeded, "")
}

func TestRecoverUnknownPhaseKeepsTheRecord1043(t *testing.T) {
	x := newRecoverFixture(t, "something-new")
	x.run(t)
	x.wantSteps(t)
	if reps := x.srv.finalReports(); len(reps) != 0 {
		t.Fatalf("reported %+v for a phase this host does not know", reps)
	}
	if !x.kept() {
		t.Fatal("dropped the record of a phase this host does not know")
	}
}

func TestRecoverReadOutcomes1043(t *testing.T) {
	for code, keep := range map[string]bool{"CONTEXT_RESET_NOT_FOUND": false, "HOST_OPERATION_NOT_COLLECTED": false, "": true} {
		t.Run(code, func(t *testing.T) {
			x := newRecoverFixture(t, "agent-stopped")
			if code == "" {
				x.resets.readErr = errors.New("transport down")
			} else {
				x.resets.readErr = &ResetStepError{Code: code, Err: errors.New("x")}
			}
			x.run(t)
			x.wantNoLaunch(t)
			x.wantSteps(t)
			if reps := x.srv.finalReports(); len(reps) != 0 {
				t.Fatalf("reported %+v after a failed read", reps)
			}
			if x.kept() != keep {
				t.Fatalf("record kept=%v, want %v", x.kept(), keep)
			}
		})
	}
}

// A step the server rejects with a typed code ends recovery: the
// operation is reported FAILED with the action's reason and the record
// dropped. A transport failure proves nothing: no report, record kept,
// and the re-delivered operation reads the phase again.
func TestRecoverStepErrors1043(t *testing.T) {
	x := newRecoverFixture(t, "stop-granted")
	x.oldAlive()
	x.resets.stepErr = map[string]error{"relaunch-failed": &ResetStepError{Code: "STALE_EPOCH", Err: errors.New("x")}}
	x.run(t)
	x.wantReport(t, Failed, "RELAUNCH_BLOCKED")

	y := newRecoverFixture(t, "stop-granted")
	y.resets.stepErr = map[string]error{"stopped": errors.New("transport down")}
	y.run(t)
	y.wantNoLaunch(t)
	if reps := y.srv.finalReports(); len(reps) != 0 {
		t.Fatalf("reported %+v after an unproven step", reps)
	}
	if !y.kept() {
		t.Fatal("dropped the record after an unproven step")
	}
}

// A final report that does not reach the server keeps the record: the
// operation is re-delivered and its now-ended phase reported again.
func TestRecoverKeepsTheRecordWhenTheReportFails1043(t *testing.T) {
	x := newRecoverFixture(t, "done")
	x.srv.reportErr = errors.New("transport down")
	x.run(t)
	if !x.kept() {
		t.Fatal("dropped the record although the report failed")
	}
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestRecoverWithoutTheWorktreeIsBlocked1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.p.mu.Lock()
	delete(x.p.agentTrees, "id-iris")
	x.p.mu.Unlock()
	x.p.state.mu.Lock()
	delete(x.p.state.Agents, "id-iris")
	x.p.state.mu.Unlock()
	x.run(t)
	x.wantNoLaunch(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantReport(t, Failed, "RELAUNCH_BLOCKED")
}

// A step answered with a refusal ended the reset server-side: recovery
// reports that refusal, never SUCCEEDED.
func TestRecoverStepRefusedEndsRecovery1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.resets.refuse = map[string]string{"relaunched": "SESSION_ENDED"}
	x.run(t)
	x.wantReport(t, Failed, "SESSION_ENDED")
}
