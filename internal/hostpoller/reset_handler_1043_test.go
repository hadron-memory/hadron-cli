package hostpoller

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeResets records every protocol step; this slice must make none.
type fakeResets struct{ calls []string }

func (f *fakeResets) ClaimReset(context.Context, string, int, ProcessIdentity) (ResetStep, error) {
	f.calls = append(f.calls, "claim")
	return ResetStep{}, nil
}
func (f *fakeResets) RegisterResetRecovery(context.Context, string, int, ResetCheckpoint, string, string) (ResetStep, error) {
	f.calls = append(f.calls, "recovery")
	return ResetStep{}, nil
}
func (f *fakeResets) AuthorizeResetStop(context.Context, string, int, ProcessIdentity) (ResetStep, error) {
	f.calls = append(f.calls, "authorize")
	return ResetStep{}, nil
}
func (f *fakeResets) ReportResetAgentStopped(context.Context, string, int, time.Duration) (ResetStep, error) {
	f.calls = append(f.calls, "stopped")
	return ResetStep{}, nil
}
func (f *fakeResets) ReportResetRelaunched(context.Context, string, int, ProcessIdentity) (ResetStep, error) {
	f.calls = append(f.calls, "relaunched")
	return ResetStep{}, nil
}
func (f *fakeResets) ReportResetRelaunchFailed(context.Context, string, int, string) (ResetStep, error) {
	f.calls = append(f.calls, "relaunch-failed")
	return ResetStep{}, nil
}
func (f *fakeResets) ReadReset(context.Context, string) (ContextReset, error) {
	f.calls = append(f.calls, "read")
	return ContextReset{}, nil
}

// fakeProc counts observations and signals: process control.
type fakeProc struct {
	observed, signalled int
	err                 error
}

func (p *fakeProc) Observe(pid int) (ProcessHandle, error) {
	if p.err != nil {
		return nil, p.err
	}
	p.observed++
	return &fakeHandle{p: p, id: ProcessIdentity{PID: pid, StartTime: "1"}}, nil
}
func (p *fakeProc) Now() time.Duration { return 0 }

type fakeHandle struct {
	p  *fakeProc
	id ProcessIdentity
}

func (h *fakeHandle) Identity() ProcessIdentity { return h.id }
func (h *fakeHandle) SignalWithin(syscall.Signal, time.Duration, time.Duration, func() time.Duration) (time.Duration, error) {
	h.p.signalled++
	return 0, nil
}
func (h *fakeHandle) Close() {}

type resetFixture struct {
	p      *Poller
	srv    *fakeServer
	resets *fakeResets
	proc   *fakeProc
	git    GitTreeState
	gitErr error
	gitDir string
	idle   func(context.Context, string) error
	agent  *fakeAgent
}

func newResetFixture(t *testing.T) *resetFixture {
	t.Helper()
	p, srv, _ := testPoller(t)
	x := &resetFixture{p: p, srv: srv, resets: &fakeResets{}, proc: &fakeProc{},
		git:   GitTreeState{InGit: true, Upstream: "origin/iris/work"},
		idle:  func(context.Context, string) error { return nil },
		agent: &fakeAgent{done: make(chan struct{}), pid: 4242},
	}
	p.trackAgent("id-iris", "repo/iris", x.agent)
	p.EnableResets(x.resets, ResetDeps{
		Proc: x.proc,
		Git: func(_ context.Context, dir string) (GitTreeState, error) {
			x.gitDir = dir
			return x.git, x.gitErr
		},
		Idle: func(ctx context.Context, ref string) error { return x.idle(ctx, ref) },
	})
	return x
}

func resetOp() Operation {
	return Operation{ID: "op1", Type: OpWorkerResetContext, WorkerID: "id-iris",
		Args: map[string]any{"workerId": "id-iris", "sessionId": "s1", "taskId": "t1", "resetId": "r1"}}
}

func (x *resetFixture) run(t *testing.T, op Operation) report {
	t.Helper()
	x.p.register(context.Background()) //nolint:errcheck // fills workerRefs
	x.p.carryOut(context.Background(), op)
	reps := x.srv.finalReports()
	if len(reps) != 1 {
		t.Fatalf("reports %+v, want one", reps)
	}
	if reps[0].outcome != Failed {
		t.Fatalf("outcome %q", reps[0].outcome)
	}
	if len(x.resets.calls) != 0 {
		t.Fatalf("protocol steps %v in a slice that must make none", x.resets.calls)
	}
	if x.proc.signalled != 0 {
		t.Fatalf("signalled %d times", x.proc.signalled)
	}
	return reps[0]
}

func wantReason(t *testing.T, r report, code string) {
	t.Helper()
	if !strings.HasPrefix(r.reason, code+": ") {
		t.Fatalf("reason %q, want prefix %s", r.reason, code)
	}
}

func TestResetIsUnknownUntilEnabled1043(t *testing.T) {
	p, srv, _ := testPoller(t)
	// Registered and running: only the switch keeps the handler out.
	p.register(context.Background()) //nolint:errcheck
	p.trackAgent("id-iris", "repo/iris", &fakeAgent{done: make(chan struct{}), pid: 4242})
	p.carryOut(context.Background(), resetOp())
	reps := srv.finalReports()
	if len(reps) != 1 || reps[0].outcome != Failed || reps[0].reason != "HOST_UNSUPPORTED: this host carries out no worker.reset-context (hadron-cli#1043)" {
		t.Fatalf("reports %+v", reps)
	}
}

func TestResetNeverAdvertisesItself1043(t *testing.T) {
	x := newResetFixture(t)
	x.p.register(context.Background()) //nolint:errcheck
	for _, r := range x.srv.regs {
		if len(r.Capabilities) != 0 {
			t.Fatalf("EnableResets declared %v", r.Capabilities)
		}
	}
}

func TestResetRefusesAMalformedOperation1043(t *testing.T) {
	for _, arg := range []string{"resetId", "sessionId", "taskId"} {
		t.Run(arg, func(t *testing.T) {
			x := newResetFixture(t)
			op := resetOp()
			delete(op.Args, arg)
			wantReason(t, x.run(t, op), "BAD_OPERATION")
			if x.proc.observed != 0 {
				t.Fatal("observed a process for a malformed operation")
			}
		})
	}
}

func TestResetRefusesAWorkerNotConfiguredHere1043(t *testing.T) {
	x := newResetFixture(t)
	op := resetOp()
	op.WorkerID, op.Args["workerId"] = "id-stranger", "id-stranger"
	wantReason(t, x.run(t, op), "HOST_UNSUPPORTED")
}

func TestResetRefusesAnAgentThisHostDidNotLaunch1043(t *testing.T) {
	x := newResetFixture(t)
	close(x.agent.done) // it exited: whatever runs now is not ours
	wantReason(t, x.run(t, resetOp()), "PROCESS_CHANGED")
	if x.proc.observed != 0 {
		t.Fatal("observed a process this host does not own")
	}
}

func TestResetGitCheckRunsInTheAgentsWorktree1043(t *testing.T) {
	x := newResetFixture(t)
	x.git.Dirty = true
	x.run(t, resetOp())
	if want := x.p.cfg.WorktreePath("repo/iris"); x.gitDir != want {
		t.Fatalf("git read %q, want %q", x.gitDir, want)
	}
}

func TestResetGitRefusals1043(t *testing.T) {
	cases := map[string]struct {
		st   GitTreeState
		err  error
		code string
	}{
		"dirty (S5)":        {GitTreeState{InGit: true, Dirty: true, Upstream: "origin/x"}, nil, "DIRTY_TREE"},
		"no upstream":       {GitTreeState{InGit: true}, nil, "UNPUSHED_HEAD"},
		"ahead of upstream": {GitTreeState{InGit: true, Upstream: "origin/x", Ahead: 2}, nil, "UNPUSHED_HEAD"},
		"unreadable tree":   {GitTreeState{}, errors.New("dubious ownership"), "DIRTY_TREE"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			x := newResetFixture(t)
			x.git, x.gitErr = c.st, c.err
			wantReason(t, x.run(t, resetOp()), c.code)
			if x.proc.observed != 0 {
				t.Fatal("observed a process after a Git refusal")
			}
		})
	}
}

// S6: a non-Git directory passes the Git stage with no Git refusal: the
// handler goes on to observe the process (and stops later, at this slice's
// boundary).
func TestResetNonGitDirectoryHasNoGitRefusal1043(t *testing.T) {
	x := newResetFixture(t)
	x.git = GitTreeState{InGit: false, Dirty: true} // Dirty is not read off Git
	r := x.run(t, resetOp())
	if strings.HasPrefix(r.reason, "DIRTY_TREE") || strings.HasPrefix(r.reason, "UNPUSHED_HEAD") {
		t.Fatalf("a non-Git directory was refused on Git grounds: %q", r.reason)
	}
	if x.proc.observed != 1 {
		t.Fatalf("observed %d, want 1", x.proc.observed)
	}
}

func TestResetCleanPushedTreePassesGit1043(t *testing.T) {
	x := newResetFixture(t)
	x.run(t, resetOp())
	if x.proc.observed != 1 {
		t.Fatalf("observed %d, want 1", x.proc.observed)
	}
}

func TestResetUnsupportedPlatformRefuses1043(t *testing.T) {
	x := newResetFixture(t)
	x.proc.err = ErrUnsupportedPlatform
	wantReason(t, x.run(t, resetOp()), "HOST_UNSUPPORTED")
}

func TestResetGoneProcessRefuses1043(t *testing.T) {
	x := newResetFixture(t)
	x.proc.err = errors.New("pidfd_open: no such process")
	wantReason(t, x.run(t, resetOp()), "PROCESS_CHANGED")
}

func TestResetNotProvenIdleRefuses1043(t *testing.T) {
	x := newResetFixture(t)
	x.idle = func(context.Context, string) error { return errors.New("a draft is in the composer") }
	wantReason(t, x.run(t, resetOp()), "NOT_IDLE")
}

func TestResetWithoutAnIdleProverRefuses1043(t *testing.T) {
	x := newResetFixture(t)
	x.p.resetDeps.Idle = nil
	wantReason(t, x.run(t, resetOp()), "NOT_IDLE")
}

// The claim and every later step wait on the open seams (restart phase
// read, relaunch binding, checkpoint writer): with every pre-claim check
// passed, this slice still stops before the claim.
func TestResetStopsBeforeTheClaimUntilTheSeamsAreBuilt1043(t *testing.T) {
	x := newResetFixture(t)
	wantReason(t, x.run(t, resetOp()), "HOST_UNSUPPORTED")
	if x.proc.observed != 1 {
		t.Fatalf("observed %d, want 1", x.proc.observed)
	}
}
