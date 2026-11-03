package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// fwdResets is the forward path's server: each step answers with a
// configured reset (or refusal, or error) and records its epoch.
type fwdResets struct {
	recResets
	claim      ContextReset
	lease      int
	checkpoint ResetCheckpoint
	authAt     time.Duration
	clock      func() (time.Duration, error)
	recordedAt map[string]bool // whether the reset was on disk at the claim
	state      *State
	refusals   map[string]string
	// node is the checkpoint node register answers with (server#3300).
	node string
	// cpAtAuth is the record's checkpoint when the stop was requested.
	cpAtAuth string
}

func (f *fwdResets) ClaimReset(_ context.Context, id string, epoch int, p ProcessIdentity) (ResetStep, error) {
	f.calls = append(f.calls, fmt.Sprintf("claim@%d pid=%d", epoch, p.PID))
	_, f.recordedAt["claim"] = f.state.resetRecord(id)
	if err := f.stepErr["claim"]; err != nil {
		return ResetStep{}, err
	}
	return ResetStep{Reset: f.claim, Refused: f.refusals["claim"]}, nil
}
func (f *fwdResets) RegisterResetRecovery(_ context.Context, _ string, epoch int, cp ResetCheckpoint, session, task string) (ResetStep, error) {
	f.calls = append(f.calls, fmt.Sprintf("register@%d %s/%s", epoch, session, task))
	f.checkpoint = cp
	if err := f.stepErr["register"]; err != nil {
		return ResetStep{}, err
	}
	return ResetStep{Reset: ContextReset{Epoch: epoch, Phase: "checkpointed", CheckpointNode: f.node}, Refused: f.refusals["register"]}, nil
}
func (f *fwdResets) AuthorizeResetStop(_ context.Context, _ string, epoch int, p ProcessIdentity) (ResetStep, error) {
	f.calls = append(f.calls, fmt.Sprintf("authorize@%d pid=%d", epoch, p.PID))
	f.authAt, _ = f.clock()
	if r, ok := f.state.resetRecord("r1"); ok {
		f.cpAtAuth = r.Checkpoint
	}
	if err := f.stepErr["authorize"]; err != nil {
		return ResetStep{}, err
	}
	return ResetStep{Reset: ContextReset{Epoch: epoch, Phase: "stop-granted", StopGranted: true, StopLeaseMs: f.lease}, Refused: f.refusals["authorize"]}, nil
}

// fwdProc is a clock that moves one second per read, and a handle on the
// old agent that records the signal and, unless told otherwise, makes the
// agent exit.
type fwdProc struct {
	recProc
	t              time.Duration
	sig            syscall.Signal
	t0, lease      time.Duration
	signals        int
	expire, noExit bool
	late           time.Duration
	clockErr       error
	agent          *fakeAgent
}

func (p *fwdProc) Now() (time.Duration, error) {
	if p.clockErr != nil {
		return 0, p.clockErr
	}
	p.t += time.Second
	return p.t, nil
}

type fwdHandle struct {
	p  *fwdProc
	id ProcessIdentity
}

func (h *fwdHandle) Identity() ProcessIdentity { return h.id }
func (h *fwdHandle) SignalWithin(sig syscall.Signal, t0, lease time.Duration, _ func() (time.Duration, error)) (time.Duration, error) {
	h.p.sig, h.p.t0, h.p.lease = sig, t0, lease
	if h.p.expire {
		return 0, ErrLeaseExpired
	}
	h.p.signals++
	if !h.p.noExit {
		delete(h.p.live, h.id.PID)
		close(h.p.agent.done)
	}
	return h.p.late, nil
}
func (h *fwdHandle) Close() {}

type fwdFixture struct {
	*recoverFixture
	fr    *fwdResets
	fp    *fwdProc
	agent *fakeAgent
	h     *fwdHandle
}

func newFwdFixture(t *testing.T) *fwdFixture {
	t.Helper()
	x, host := relaunchFixture(t)
	agent := &fakeAgent{done: make(chan struct{}), pid: 4242}
	x.p.trackAgent("id-iris", "repo/iris", agent)
	fp := &fwdProc{recProc: recProc{live: map[int]string{4242: "1", 5151: "2"}, errs: map[int]error{}}, agent: agent}
	fr := &fwdResets{claim: ContextReset{ID: "r1", Epoch: 1, Phase: "claimed"}, lease: 15000, clock: fp.Now,
		recordedAt: map[string]bool{}, state: x.p.state, refusals: map[string]string{}, node: checkpointNode}
	fr.read = ContextReset{ID: "r1", Epoch: 1, SessionID: "s1", TaskID: "t1", WorkerID: "id-iris"}
	fr.stepErr = map[string]error{}
	x.p.EnableResets(fr, ResetDeps{Proc: fp, Git: x.p.resetDeps.Git, Idle: x.p.resetDeps.Idle})
	x.p.resetStopWait = 50 * time.Millisecond
	rf := &recoverFixture{p: x.p, srv: x.srv, host: host, resets: &fr.recResets, proc: &fp.recProc}
	return &fwdFixture{recoverFixture: rf, fr: fr, fp: fp, agent: agent, h: &fwdHandle{p: fp, id: oldIdentity}}
}

func (x *fwdFixture) run(t *testing.T) {
	t.Helper()
	x.p.register(context.Background()) //nolint:errcheck // fills workerRefs
	x.p.runReset(context.Background(), resetOp(), "iris", "repo/iris", GitTreeState{InGit: true, Upstream: "origin/iris/work"}, x.h, x.agent)
}

func (x *fwdFixture) wantCalls(t *testing.T, want ...string) {
	t.Helper()
	got := x.fr.calls
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Fatalf("steps\n  %v\nwant\n  %v", got, want)
	}
}

// The whole forward path: claim, checkpoint, stop, relaunch with the
// checkpoint node the server returned (server#3300), done.
func TestResetForwardPath1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fp.late = 0
	x.run(t)
	x.wantCalls(t,
		"claim@0 pid=4242",
		"register@1 s1/t1",
		"authorize@1 pid=4242",
		"stopped@1 late=0s",
		"relaunched@1 pid=5151")
	if !x.fr.recordedAt["claim"] {
		t.Fatal("the claim was sent before the reset was recorded on disk")
	}
	if x.fp.signals != 1 || x.fp.sig != syscall.SIGTERM {
		t.Fatalf("signals %d (%v), want one SIGTERM", x.fp.signals, x.fp.sig)
	}
	if x.fp.lease != 15*time.Second {
		t.Fatalf("lease %s, want the granted 15s", x.fp.lease)
	}
	// t0 is read before the authorize request goes out.
	if x.fp.t0 >= x.fr.authAt {
		t.Fatalf("t0 %s is not before the authorize request (%s)", x.fp.t0, x.fr.authAt)
	}
	// The node is on disk before the stop: a restart after the signal
	// still relaunches with it.
	if x.fr.cpAtAuth != checkpointNode {
		t.Fatalf("record checkpoint at the stop request %q, want %q", x.fr.cpAtAuth, checkpointNode)
	}
	if len(x.host.launches) != 1 {
		t.Fatalf("launches %d, want one", len(x.host.launches))
	}
	if env := x.host.promptEnvs[0]; !containsStr(env, "HADRON_RECOVERY_NODE="+checkpointNode) {
		t.Fatalf("relaunch env %v lacks the checkpoint node", env)
	}
	x.wantReport(t, Succeeded, "")
	if x.kept() {
		t.Fatal("kept the record of a finished reset")
	}
}

// A register answer with no checkpoint node leaves nothing to relaunch
// with: the reset fails CHECKPOINT_FAILED before any process control.
func TestResetRegisterWithoutANodeStopsBeforeTheSignal1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fr.node = ""
	x.run(t)
	x.wantCalls(t, "claim@0 pid=4242", "register@1 s1/t1", "relaunch-failed@1 CHECKPOINT_FAILED")
	x.wantReport(t, Failed, "CHECKPOINT_FAILED")
	if x.fp.signals != 0 {
		t.Fatal("signalled with no checkpoint node to relaunch with")
	}
	if len(x.host.launches) != 0 {
		t.Fatal("launched without a checkpoint node")
	}
}

func TestResetLateStopIsReported1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fp.late = 1500 * time.Millisecond
	x.run(t)
	if x.fr.calls[3] != "stopped@1 late=1.5s" {
		t.Fatalf("steps %v, want the late stop reported", x.fr.calls)
	}
}

func TestResetCheckpointContent1043(t *testing.T) {
	x := newFwdFixture(t)
	x.run(t)
	cp := x.fr.checkpoint
	if strings.TrimSpace(cp.Title) == "" || utf8.RuneCountInString(cp.Title) > 200 {
		t.Fatalf("title %q", cp.Title)
	}
	if len(cp.Content) > 65536 {
		t.Fatalf("content %d bytes", len(cp.Content))
	}
	for _, want := range []string{"r1", "s1", "t1", "id-iris", "repo/iris", "origin/iris/work"} {
		if !strings.Contains(cp.Content, want) {
			t.Fatalf("checkpoint lacks %q:\n%s", want, cp.Content)
		}
	}
	if strings.Contains(cp.Content, hostKey) || strings.Contains(cp.Title, hostKey) {
		t.Fatal("the checkpoint carries the host key")
	}
}

func TestResetClaimRefusedEndsBeforeAnyProcessControl1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fr.refusals["claim"] = "SESSION_CHANGED"
	x.run(t)
	x.wantCalls(t, "claim@0 pid=4242")
	x.wantReport(t, Failed, "SESSION_CHANGED")
	if x.fp.signals != 0 {
		t.Fatal("signalled after a refused claim")
	}
}

// A claim whose answer is lost proves nothing: no report, the record kept,
// and the re-delivered operation recovers from the server's phase.
func TestResetClaimTransportFailureKeepsTheRecord1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fr.stepErr["claim"] = errors.New("transport down")
	x.fr.readErr = errors.New("transport down")
	x.run(t)
	if reps := x.srv.finalReports(); len(reps) != 0 {
		t.Fatalf("reported %+v", reps)
	}
	if !x.kept() {
		t.Fatal("dropped the record of a claim that may have landed")
	}
}

// A step the server rejects hands the reset to recovery, which acts on the
// phase the server holds: never a guess from the failed step.
func TestResetStepRejectionRecoversFromThePhase1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fr.stepErr["register"] = &ResetStepError{Code: "CHECKPOINT_LOC_TAKEN", Err: errors.New("x")}
	x.fr.read.Phase = "claimed"
	x.run(t)
	x.wantCalls(t, "claim@0 pid=4242", "register@1 s1/t1", "read", "relaunch-failed@1 CHECKPOINT_FAILED")
	x.wantReport(t, Failed, "CHECKPOINT_FAILED")
	if x.fp.signals != 0 {
		t.Fatal("signalled after a failed checkpoint")
	}
}

func TestResetRefusedRegisterEnds1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fr.refusals["register"] = "CLAIM_LAPSED"
	x.run(t)
	x.wantCalls(t, "claim@0 pid=4242", "register@1 s1/t1")
	x.wantReport(t, Failed, "CLAIM_LAPSED")
}

func TestResetExpiredLeaseSendsNothing1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fp.expire = true
	x.run(t)
	x.wantCalls(t, "claim@0 pid=4242", "register@1 s1/t1", "authorize@1 pid=4242", "relaunch-failed@1 RELAUNCH_BLOCKED")
	x.wantReport(t, Failed, "RELAUNCH_BLOCKED")
	// The record says no stop was sent, not that the agent outlived one.
	if r := x.srv.finalReports()[0].reason; !strings.Contains(r, "the stop was not sent") {
		t.Fatalf("reason %q does not say the stop was never sent", r)
	}
	if x.fp.signals != 0 {
		t.Fatal("signalled after the lease expired")
	}
}

// A boot clock that cannot be read before the stop request leaves no window
// to measure (cli#1582 BLIND P2 3): the stop is never asked for or sent.
func TestResetClockFailureBeforeAuthorizeSendsNothing1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fp.clockErr = fmt.Errorf("%w: test", ErrClock)
	x.run(t)
	x.wantCalls(t, "claim@0 pid=4242", "register@1 s1/t1", "relaunch-failed@1 RELAUNCH_BLOCKED")
	x.wantReport(t, Failed, "RELAUNCH_BLOCKED")
	if r := x.srv.finalReports()[0].reason; !strings.Contains(r, "the stop was not asked for") {
		t.Fatalf("reason %q does not say the stop was never asked for", r)
	}
	if x.fp.signals != 0 {
		t.Fatal("signalled with no readable clock")
	}
}

// An agent that does not exit after the one stop is not escalated: the
// reset fails RELAUNCH_BLOCKED and nothing starts.
func TestResetAgentThatDoesNotExitIsNotEscalated1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fp.noExit = true
	x.run(t)
	x.wantCalls(t, "claim@0 pid=4242", "register@1 s1/t1", "authorize@1 pid=4242", "relaunch-failed@1 RELAUNCH_BLOCKED")
	if x.fp.signals != 1 {
		t.Fatalf("signals %d, want exactly the one stop", x.fp.signals)
	}
	x.wantReport(t, Failed, "RELAUNCH_BLOCKED")
}

func TestResetRefusedAuthorizeEnds1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fr.refusals["authorize"] = "CLAIM_LAPSED"
	x.run(t)
	x.wantCalls(t, "claim@0 pid=4242", "register@1 s1/t1", "authorize@1 pid=4242")
	x.wantReport(t, Failed, "CLAIM_LAPSED")
	if x.fp.signals != 0 {
		t.Fatal("signalled without a grant")
	}
}
