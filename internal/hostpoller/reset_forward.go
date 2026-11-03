package hostpoller

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"time"
)

// defaultResetStopWait bounds how long the old agent has to exit after the
// one stop. It is never escalated: one grant, one signal (#1043).
const defaultResetStopWait = 30 * time.Second

func (p *Poller) stopWait() time.Duration {
	if p.resetStopWait > 0 {
		return p.resetStopWait
	}
	return defaultResetStopWait
}

// runReset carries a checked worker.reset-context from the claim through
// the stop (#1043, steps 1-3), then hands the relaunch to recoverRelaunch.
// h pins the observed old agent; agent is the process this poller launched.
//
// The reset is recorded on disk before the claim, so a restart at any
// point recovers it from the server's phase. Any step that errs, in
// transport or by a typed rejection, also hands over to that recovery: the
// next action is decided by the phase the server holds, never by a guess
// from the failed step. A step that refused ended the reset; its refusal
// is reported. Nothing is retried and the stop is never escalated.
func (p *Poller) runReset(ctx context.Context, op Operation, ref, token string, git GitTreeState, h ProcessHandle, agent Agent) {
	resetID := argString(op, "resetId")
	old := h.Identity()
	if err := p.state.recordReset(ResetRecord{ResetID: resetID, OperationID: op.ID, WorkerID: op.WorkerID, Old: old}); err != nil {
		p.finish(ctx, op, Failed, "", "HOST_UNSUPPORTED: the reset could not be recorded before the claim: "+err.Error())
		return
	}
	step := func(do func() (ResetStep, error)) (ResetStep, bool) {
		res, err := do()
		if err != nil {
			p.log("reset", op.ID, op.Type, "", "a step failed; recovering from the server's phase: "+err.Error())
			if cur, ok := p.state.resetRecord(resetID); ok {
				p.recoverReset(ctx, op, cur)
			}
			return res, false
		}
		if res.Refused != "" {
			p.endRecovery(ctx, op, resetID, Failed, res.Refused+": the server ended the reset")
			return res, false
		}
		return res, true
	}

	claim, ok := step(func() (ResetStep, error) { return p.resets.ClaimReset(ctx, resetID, 0, old) })
	if !ok {
		return
	}
	epoch := claim.Reset.Epoch
	if err := p.state.updateReset(resetID, func(r *ResetRecord) { r.Epoch = epoch }); err != nil {
		p.log("state-failed", op.ID, op.Type, "", err.Error())
	}

	cp := resetCheckpoint(op, ref, token, git)
	reg, ok := step(func() (ResetStep, error) {
		return p.resets.RegisterResetRecovery(ctx, resetID, epoch, cp, argString(op, "sessionId"), argString(op, "taskId"))
	})
	if !ok {
		return
	}
	// The relaunch starts the agent on the node the server wrote
	// (server#3300). Without it, or without it on disk, nothing could be
	// relaunched: fail before any process control, never after a stop.
	node := reg.Reset.CheckpointNode
	if node == "" {
		p.recoveryRelaunchFailed(ctx, op, resetID, epoch, "CHECKPOINT_FAILED", "the server returned no checkpoint node")
		return
	}
	if err := p.state.updateReset(resetID, func(r *ResetRecord) { r.Checkpoint = node }); err != nil {
		p.recoveryRelaunchFailed(ctx, op, resetID, epoch, "CHECKPOINT_FAILED", "the checkpoint node could not be recorded: "+err.Error())
		return
	}

	// t0 before the request: a slow answer shortens the window, never
	// lengthens it.
	t0, err := p.resetDeps.Proc.Now()
	if err != nil {
		p.recoveryRelaunchFailed(ctx, op, resetID, epoch, "RELAUNCH_BLOCKED", "the stop was not asked for: "+err.Error())
		return
	}
	grant, ok := step(func() (ResetStep, error) { return p.resets.AuthorizeResetStop(ctx, resetID, epoch, old) })
	if !ok {
		return
	}
	lease := time.Duration(grant.Reset.StopLeaseMs) * time.Millisecond
	late, err := h.SignalWithin(syscall.SIGTERM, t0, lease, p.resetDeps.Proc.Now)
	if err != nil {
		p.recoveryRelaunchFailed(ctx, op, resetID, epoch, "RELAUNCH_BLOCKED", "the stop was not sent: "+err.Error())
		return
	}
	timer := time.NewTimer(p.stopWait())
	defer timer.Stop()
	select {
	case <-agent.Done():
	case <-timer.C:
	case <-ctx.Done():
		return // the record stays; the re-delivered operation recovers it
	}
	if alive, known := p.processAlive(old); alive || !known {
		p.recoveryRelaunchFailed(ctx, op, resetID, epoch, "RELAUNCH_BLOCKED", fmt.Sprintf("the agent did not exit within %s of the stop", p.stopWait()))
		return
	}
	if _, ok := step(func() (ResetStep, error) { return p.resets.ReportResetAgentStopped(ctx, resetID, epoch, late) }); !ok {
		return
	}
	cur, _ := p.state.resetRecord(resetID)
	p.recoverRelaunch(ctx, op, cur, epoch)
}

// resetCheckpoint is the checkpoint the server writes for the host: ids
// and the worktree's push state only, never the agent's environment or a
// credential. The agent's own handoff, if it wrote one, is in its memory.
func resetCheckpoint(op Operation, ref, token string, git GitTreeState) ResetCheckpoint {
	var b strings.Builder
	b.WriteString("# Context reset checkpoint\n\n")
	b.WriteString("Written by the host before resetting this worker's context (hadron-cli#1043). The binding and session are kept; read this, then your newest handoff, and continue.\n\n")
	fmt.Fprintf(&b, "- worker: %s (%s)\n", ref, op.WorkerID)
	fmt.Fprintf(&b, "- session: %s\n", argString(op, "sessionId"))
	fmt.Fprintf(&b, "- finished task: %s\n", argString(op, "taskId"))
	fmt.Fprintf(&b, "- reset: %s (operation %s)\n", argString(op, "resetId"), op.ID)
	fmt.Fprintf(&b, "- worktree: %s\n", token)
	if git.InGit {
		fmt.Fprintf(&b, "- branch upstream: %s (clean, pushed)\n", git.Upstream)
	} else {
		b.WriteString("- not a Git worktree\n")
	}
	return ResetCheckpoint{Title: "Context reset " + argString(op, "resetId"), Content: b.String()}
}
