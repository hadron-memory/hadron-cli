package hostpoller

import (
	"context"
	"errors"
	"testing"
)

// Pins for the review findings on cli#1582 at 398d6da4 (LOCAL Juliet,
// §10 Rodolfo). The probes are the reviewers', adapted to the fixtures.

// LOCAL P2-1: a late signal whose agent does not exit still reaches the
// coordinator: the reset ends RELAUNCH_BLOCKED, then the stop is annotated
// late on the ended reset (#3255), never reported as a stop before the end.
func TestResetLateSignalThatDoesNotExitIsAnnotated1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fp.late, x.fp.noExit = 1500e6, true
	x.run(t)
	x.wantCalls(t, "claim@0 pid=4242", "register@1 s1/t1", "authorize@1 pid=4242",
		"relaunch-failed@1 RELAUNCH_BLOCKED", "stopped@1 late=1.5s")
	x.wantReport(t, Failed, "RELAUNCH_BLOCKED")
}

// LOCAL P2-1: a cancellation during the exit wait keeps the record, with
// the lateness on it, for the recovery to send.
func TestResetLateSignalSurvivesACancellation1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fp.late, x.fp.noExit = 1500e6, true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	x.p.register(context.Background()) //nolint:errcheck // fills workerRefs
	x.p.runReset(ctx, resetOp(), "iris", "repo/iris", GitTreeState{InGit: true, Upstream: "origin/iris/work"}, x.h, x.agent)
	rec, ok := x.p.state.resetRecord("r1")
	if !ok || rec.LateStopMs != 1500 {
		t.Fatalf("record %+v (kept %v), want lateStopMs 1500", rec, ok)
	}
}

// LOCAL P2-1: recovery sends the recorded lateness, with the stop when the
// old agent is gone, and as an annotation after the end when it is alive.
func TestResetRecoverySendsTheRecordedLateness1043(t *testing.T) {
	t.Run("old gone", func(t *testing.T) {
		x := newRecoverFixture(t, "stop-granted")
		x.set(func(r *ResetRecord) { r.LateStopMs = 1500 })
		x.run(t)
		x.wantSteps(t, "stopped@2 late=1.5s", "relaunched@2 pid=5151")
	})
	t.Run("old alive", func(t *testing.T) {
		x := newRecoverFixture(t, "stop-granted")
		x.oldAlive()
		x.set(func(r *ResetRecord) { r.LateStopMs = 1500 })
		x.run(t)
		x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED", "stopped@2 late=1.5s")
		x.wantNoLaunch(t)
	})
	t.Run("old alive, on time", func(t *testing.T) {
		x := newRecoverFixture(t, "stop-granted")
		x.oldAlive()
		x.run(t)
		x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	})
}

// LOCAL P2-2: recovery never launches beside a live replacement this host
// launched for the worker.
func TestResetRecoveryDoesNotLaunchBesideAReplacement1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.proc.live[6161] = "replacement"
	x.p.trackAgent("id-iris", "repo/iris", &fakeAgent{done: make(chan struct{}), pid: 6161})
	x.run(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantNoLaunch(t)
}

// LOCAL P2-2 control: an exited replacement does not block the relaunch.
func TestResetRecoveryRelaunchesBesideAnExitedAgent1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	gone := &fakeAgent{done: make(chan struct{}), pid: 6161}
	close(gone.done)
	x.p.trackAgent("id-iris", "repo/iris", gone)
	x.run(t)
	x.wantSteps(t, "relaunched@2 pid=5151")
}

// LOCAL P2-3: idle is proven again after the claim; activity since refuses
// the stop and nothing is signalled.
func TestResetActivityAfterTheClaimSendsNothing1043(t *testing.T) {
	x := newFwdFixture(t)
	x.p.resetDeps.Idle = func(context.Context, string) error {
		if len(x.fr.calls) > 0 { // the claim went out
			return errors.New("BUSY: the agent began working")
		}
		return nil
	}
	x.run(t)
	x.wantCalls(t, "claim@0 pid=4242", "register@1 s1/t1", "relaunch-failed@1 RELAUNCH_BLOCKED")
	x.wantReport(t, Failed, "RELAUNCH_BLOCKED")
	if x.fp.signals != 0 {
		t.Fatal("signalled an agent that was active after the claim")
	}
}

// §10 P3-2: an old agent whose liveness after the stop is unknown blocks
// the relaunch, as a live one does.
func TestResetUnknownLivenessAfterTheStopBlocksTheRelaunch1043(t *testing.T) {
	x := newFwdFixture(t)
	x.fp.errs[4242] = errors.New("pidfd_open: operation not permitted")
	x.run(t)
	x.wantCalls(t, "claim@0 pid=4242", "register@1 s1/t1", "authorize@1 pid=4242", "relaunch-failed@1 RELAUNCH_BLOCKED")
	if len(x.host.launches) != 0 {
		t.Fatal("relaunched with the old agent's exit unproven")
	}
}

// §10 P3-3: recovery takes the config from the operation and the worktree
// from the record, so they must name one worker.
func TestResetRecoveryRefusesAMismatchedWorker1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.set(func(r *ResetRecord) { r.WorkerID = "id-other" })
	// The record's worker has a worktree of its own here, so only the
	// mismatch itself can refuse.
	x.p.mu.Lock()
	x.p.agentTrees["id-other"] = "repo/iris"
	x.p.mu.Unlock()
	x.run(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantNoLaunch(t)
}

// 2nd REVIEW P2: the pin must be the process observed at launch, not
// merely a live process with the agent's pid (a reaped-and-reused pid).
func TestResetPinnedProcessMustBeTheLaunchedOne1043(t *testing.T) {
	x := newResetFixture(t)
	x.p.mu.Lock()
	x.p.agentIDs["id-iris"] = ProcessIdentity{PID: 4242, StartTime: "launch-time"}
	x.p.mu.Unlock()
	wantReason(t, x.run(t, resetOp()), "PROCESS_CHANGED")
}

// 2nd REVIEW P2: an agent tracked before resets were enabled has no
// launch-time identity, so nothing proves the pin is it.
func TestResetAgentWithoutALaunchIdentityRefuses1043(t *testing.T) {
	x := newResetFixture(t)
	x.p.mu.Lock()
	delete(x.p.agentIDs, "id-iris")
	x.p.mu.Unlock()
	wantReason(t, x.run(t, resetOp()), "PROCESS_CHANGED")
}

// 2nd REVIEW P3-1: recovery refuses a worker with no recover prompt before
// it starts anything (an empty argv would otherwise reach Prompt).
func TestResetRecoveryRefusesAWorkerWithoutARecoverPrompt1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	w := x.p.cfg.Workers["iris"]
	w.Recover = nil
	x.p.cfg.Workers["iris"] = w
	x.run(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantNoLaunch(t)
}

// 2nd REVIEW P3-4: a handler enabled without process control or a Git
// reader refuses rather than panics.
func TestResetWithoutDepsRefuses1043(t *testing.T) {
	x := newResetFixture(t)
	x.p.resetDeps.Proc = nil
	wantReason(t, x.run(t, resetOp()), "HOST_UNSUPPORTED")
}

// 2nd REVIEW P2: an agent that exited before its launch-time observation
// gets no identity (its pid may already be reused), so it is never reset.
func TestResetAgentExitedAtLaunchHasNoIdentity1043(t *testing.T) {
	x := newResetFixture(t)
	gone := &fakeAgent{done: make(chan struct{}), pid: 4343}
	close(gone.done)
	x.p.trackAgent("id-iris", "repo/iris", gone)
	x.p.mu.Lock()
	_, has := x.p.agentIDs["id-iris"]
	x.p.mu.Unlock()
	if has {
		t.Fatal("recorded a launch identity for an agent that had already exited")
	}
}

// BLIND delta P2: a live replacement that reuses the old pid with a new
// start time is still a replacement.
func TestResetRecoveryDoesNotLaunchBesideASamePIDReplacement1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.proc.live[4242] = "new-start" // launch-time observation of the replacement
	x.p.trackAgent("id-iris", "repo/iris", &fakeAgent{done: make(chan struct{}), pid: 4242})
	delete(x.proc.live, 4242) // the old identity {4242, "1"} is gone
	x.run(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantNoLaunch(t)
}

// BLIND delta P2: a live tracked agent with no launch identity is not
// proven to be the old one, so nothing is started beside it.
func TestResetRecoveryDoesNotLaunchBesideAnUnidentifiedAgent1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.proc.errs[6161] = errors.New("pidfd_open: operation not permitted")
	x.p.trackAgent("id-iris", "repo/iris", &fakeAgent{done: make(chan struct{}), pid: 6161})
	delete(x.proc.errs, 6161)
	x.run(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantNoLaunch(t)
}

// LOCAL delta P2-2: after a restart nothing is tracked in memory; a live
// replacement recorded in the state file still blocks the relaunch.
func TestResetRecoveryDoesNotLaunchBesideAPersistedReplacement1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	if err := x.p.state.recordAgent("id-iris", "repo/iris", 6161, ""); err != nil {
		t.Fatal(err)
	}
	x.proc.live[6161] = "replacement"
	x.run(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantNoLaunch(t)
}

// LOCAL delta P2-2: a persisted replacement that cannot be proven gone
// blocks the relaunch too.
func TestResetRecoveryDoesNotLaunchBesideAnUnobservablePersistedAgent1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	if err := x.p.state.recordAgent("id-iris", "repo/iris", 6161, ""); err != nil {
		t.Fatal(err)
	}
	x.proc.errs[6161] = errors.New("pidfd_open: operation not permitted")
	x.run(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantNoLaunch(t)
}

// Control: a persisted replacement that is gone does not block.
func TestResetRecoveryRelaunchesWhenThePersistedAgentIsGone1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	if err := x.p.state.recordAgent("id-iris", "repo/iris", 6161, ""); err != nil {
		t.Fatal(err)
	}
	x.run(t)
	x.wantSteps(t, "relaunched@2 pid=5151")
}

// restart drops what this process tracks for the worker, as a poller
// restart does; the state file is kept.
func (x *recoverFixture) restart() {
	x.p.mu.Lock()
	delete(x.p.agents, "id-iris")
	delete(x.p.agentIDs, "id-iris")
	x.p.mu.Unlock()
}

// BLIND + LOCAL delta P2 at b9592069: a replacement that reuses the old
// pid, recorded by this host, is still a replacement after a restart. The
// state file keeps its launch identity, so pid equality proves nothing.
func TestResetRecoveryDoesNotLaunchBesideAPersistedSamePIDReplacement1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	x.proc.live[4242] = "replacement-start"
	x.p.trackAgent("id-iris", "repo/iris", &fakeAgent{done: make(chan struct{}), pid: 4242})
	x.restart()
	x.run(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantNoLaunch(t)
}

// Control: the old agent's own record (its launch identity) with its pid
// now reused by an unrelated process does not block the relaunch.
func TestResetRecoveryRelaunchesBesideAnUnrelatedReuseOfTheOldPID1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	if err := x.p.state.recordAgent("id-iris", "repo/iris", oldIdentity.PID, oldIdentity.StartTime); err != nil {
		t.Fatal(err)
	}
	x.proc.live[4242] = "unrelated"
	x.run(t)
	x.wantSteps(t, "relaunched@2 pid=5151")
}

// A record without a launch identity at the old pid, with another process
// live there, cannot be told from a replacement: it blocks.
func TestResetRecoveryBlocksAnUnidentifiedRecordAtTheOldPID1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	if err := x.p.state.recordAgent("id-iris", "repo/iris", oldIdentity.PID, ""); err != nil {
		t.Fatal(err)
	}
	x.proc.live[4242] = "unknown-launch"
	x.run(t)
	x.wantSteps(t, "relaunch-failed@2 RELAUNCH_BLOCKED")
	x.wantNoLaunch(t)
}

// A recorded launch that is gone (its pid now runs something else) is not
// a replacement.
func TestResetRecoveryRelaunchesWhenTheRecordedLaunchIsGone1043(t *testing.T) {
	x := newRecoverFixture(t, "agent-stopped")
	if err := x.p.state.recordAgent("id-iris", "repo/iris", 6161, "replacement"); err != nil {
		t.Fatal(err)
	}
	x.proc.live[6161] = "someone-else"
	x.run(t)
	x.wantSteps(t, "relaunched@2 pid=5151")
}
