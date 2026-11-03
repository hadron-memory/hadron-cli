package hostpoller

import (
	"context"
	"errors"
	"syscall"
	"time"
)

// recovery is one reset this host collected before a restart, with the
// state the server holds for it now.
type recovery struct {
	Record ResetRecord
	Reset  ContextReset
}

// pendingRecoveries reads, for every reset this host recorded, its current
// state from the server (#3273): the phase comes from there, never from
// local memory (hadron-cli#1043, step 5). Only reads are made, never a
// protocol step. A reset the server no longer shows this host
// (CONTEXT_RESET_NOT_FOUND, HOST_OPERATION_NOT_COLLECTED) is dropped
// locally; any other read error keeps the record for the next pass.
//
// What each phase then calls for is decided separately (comment
// 6593190456, awaiting the gate's confirmation); nothing here acts on it.
func pendingRecoveries(ctx context.Context, s *State, rs ResetServer) []recovery {
	out := []recovery{}
	for _, rec := range s.inFlightResets() {
		r, err := rs.ReadReset(ctx, rec.ResetID)
		switch code := ResetStepCode(err); {
		case err == nil:
			out = append(out, recovery{Record: rec, Reset: r})
		case code == "CONTEXT_RESET_NOT_FOUND" || code == "HOST_OPERATION_NOT_COLLECTED":
			_ = s.dropReset(rec.ResetID)
		}
	}
	return out
}

// recoverKind is what a restarted host does with one collected reset.
type recoverKind int

const (
	// recoverKeep: touch nothing and keep the record for the next pass.
	recoverKeep recoverKind = iota
	// recoverReportFailed: report the operation FAILED with Reason (the
	// server ends the reset with it, in any phase).
	recoverReportFailed
	// recoverReportSucceeded: report the operation SUCCEEDED.
	recoverReportSucceeded
	// recoverRelaunchFailed: send relaunchFailed(Reason), then report the
	// operation FAILED with it.
	recoverRelaunchFailed
	// recoverRelaunch: the old agent is proven stopped; relaunch it.
	recoverRelaunch
	// recoverStoppedThenRelaunch: report agentStopped with no lateStopMs
	// (lateness is unknowable after a restart), then relaunch.
	recoverStoppedThenRelaunch
)

// recoveryAction is the decision for one reset. Every row of the table is
// the spec owner's (cor:agt:050:27 part 11).
type recoveryAction struct {
	Kind   recoverKind
	Reason string
}

// endedWithoutReason is the FAILED code sent for an ended reset the server
// gives no reason for; the server accepts any code once a reset has ended.
const endedWithoutReason = "CONTEXT_RESET_ENDED"

// decideRecovery is the per-phase restart table (#1043). It is pure: the
// phase is the server's (ReadReset), oldAlive says whether the recorded old
// agent process is still the same live process (pidfd identity and start
// time), newAlive the same for the recorded relaunched one. Nothing it
// returns ever signals a process, and no protocol step is retried.
func decideRecovery(r ContextReset, oldAlive, newAlive bool) recoveryAction {
	switch r.Phase {
	case "requested":
		// No claim held, and the observation is gone: nothing to re-run.
		return recoveryAction{Kind: recoverReportFailed, Reason: "PROCESS_CHANGED"}
	case "claimed":
		// No process control happened; the checkpoint is not proven written.
		return recoveryAction{Kind: recoverRelaunchFailed, Reason: "CHECKPOINT_FAILED"}
	case "checkpointed":
		// No process control happened; the staged prompt is not proven intact.
		return recoveryAction{Kind: recoverRelaunchFailed, Reason: "STAGING_FAILED"}
	case "stop-granted":
		// The lease has expired and the host never signals on recovery.
		if oldAlive {
			return recoveryAction{Kind: recoverRelaunchFailed, Reason: "RELAUNCH_BLOCKED"}
		}
		// The signal may have landed before the crash.
		return recoveryAction{Kind: recoverStoppedThenRelaunch}
	case "agent-stopped":
		if oldAlive {
			return recoveryAction{Kind: recoverReportFailed, Reason: "PROCESS_CHANGED"}
		}
		return recoveryAction{Kind: recoverRelaunch}
	case "relaunched":
		if newAlive {
			return recoveryAction{Kind: recoverReportSucceeded}
		}
		return recoveryAction{Kind: recoverReportFailed, Reason: "RELAUNCH_UNVERIFIED"}
	case "done":
		return recoveryAction{Kind: recoverReportSucceeded}
	case "refused", "failed":
		reason := r.Reason
		if reason == "" {
			reason = endedWithoutReason
		}
		return recoveryAction{Kind: recoverReportFailed, Reason: reason}
	}
	return recoveryAction{Kind: recoverKeep}
}

// recoverReset finishes a reset this host collected before a restart, from
// the phase the server holds now (#1043, step 5). It never signals a
// process and retries no step. The record is dropped only once the
// operation's final report reaches the server; anything unproven (a read
// or step that failed in transport, a report that did not arrive) keeps
// it, and the re-delivered operation reads the phase again.
func (p *Poller) recoverReset(ctx context.Context, op Operation, rec ResetRecord) {
	r, err := p.resets.ReadReset(ctx, rec.ResetID)
	switch code := ResetStepCode(err); {
	case err == nil:
	case code == "CONTEXT_RESET_NOT_FOUND" || code == "HOST_OPERATION_NOT_COLLECTED":
		p.dropResetRecord(rec.ResetID)
		return
	default:
		p.log("reset-recovery", op.ID, op.Type, "", "reading the reset failed: "+err.Error())
		return
	}
	// A restart between the register answer and its write to disk leaves
	// the record without the node; the server's read carries it (#3300).
	if rec.Checkpoint == "" && r.CheckpointNode != "" {
		rec.Checkpoint = r.CheckpointNode
		if err := p.state.updateReset(rec.ResetID, func(x *ResetRecord) { x.Checkpoint = r.CheckpointNode }); err != nil {
			p.log("state-failed", op.ID, op.Type, "", err.Error())
		}
	}
	oldAlive, oldKnown := p.processAlive(rec.Old)
	newAlive := false
	if rec.New != nil {
		newAlive, _ = p.processAlive(*rec.New) // alive only when proven
	}
	act := decideRecovery(r, oldAlive || !oldKnown, newAlive) // unknown old counts as alive
	// A live reset's epoch is the claim's: a cancellation moves it only by
	// ending the reset, and an ended phase takes no step here.
	epoch := r.Epoch
	switch act.Kind {
	case recoverKeep:
		p.log("reset-recovery", op.ID, op.Type, "", "phase "+r.Phase+" is unknown to this host; kept")
	case recoverReportSucceeded:
		p.endRecovery(ctx, op, rec.ResetID, Succeeded, "")
	case recoverReportFailed:
		p.endRecovery(ctx, op, rec.ResetID, Failed, act.Reason+": after a host restart, at phase "+r.Phase)
	case recoverRelaunchFailed:
		if p.recoveryRelaunchFailed(ctx, op, rec.ResetID, epoch, act.Reason, "after a host restart, at phase "+r.Phase) && r.Phase == "stop-granted" {
			p.annotateLateStop(ctx, op, rec.ResetID, epoch, recordedLate(rec))
		}
	case recoverStoppedThenRelaunch:
		// The lateness recorded when the signal returned; a crash before
		// that write leaves 0, which omits lateStopMs.
		if !p.recoveryStep(ctx, op, rec.ResetID, "RELAUNCH_UNVERIFIED", func() (ResetStep, error) {
			return p.resets.ReportResetAgentStopped(ctx, rec.ResetID, epoch, recordedLate(rec))
		}) {
			return
		}
		p.recoverRelaunch(ctx, op, rec, epoch)
	case recoverRelaunch:
		p.recoverRelaunch(ctx, op, rec, epoch)
	}
}

// recoverRelaunch starts the worker's agent again for a recovered reset
// whose old agent is gone. A relaunch already begun before the restart is
// never started again: two agents in one worktree are worse than a failed
// reset.
func (p *Poller) recoverRelaunch(ctx context.Context, op Operation, rec ResetRecord, epoch int) {
	_, w, ok := p.workerRef(op)
	token, hasTree := p.agentWorktree(rec.WorkerID)
	switch {
	case op.WorkerID != rec.WorkerID:
		// The config comes from the operation and the worktree from the
		// record: they must name one worker.
		p.recoveryRelaunchFailed(ctx, op, rec.ResetID, epoch, "RELAUNCH_BLOCKED", "the operation's worker is not the recorded reset's worker")
		return
	case p.replacedAgent(rec):
		// Relaunch only when the old agent is gone AND not replaced: a
		// live agent this host launched for the worker since is its
		// replacement, and a second beside it is worse than a failed reset.
		p.recoveryRelaunchFailed(ctx, op, rec.ResetID, epoch, "RELAUNCH_BLOCKED", "a replacement agent this host launched for the worker is running; nothing was started beside it")
		return
	case rec.Relaunching:
		p.recoveryRelaunchFailed(ctx, op, rec.ResetID, epoch, "RELAUNCH_UNVERIFIED", "a relaunch had begun before the host restarted")
		return
	case !ok || !hasTree:
		p.recoveryRelaunchFailed(ctx, op, rec.ResetID, epoch, "RELAUNCH_BLOCKED", "the worker or its worktree is no longer configured on this host")
		return
	case len(w.Recover) == 0:
		p.recoveryRelaunchFailed(ctx, op, rec.ResetID, epoch, "RELAUNCH_BLOCKED", "no recover prompt is configured for this worker on this host")
		return
	case rec.Checkpoint == "":
		p.recoveryRelaunchFailed(ctx, op, rec.ResetID, epoch, "RELAUNCH_BLOCKED", "no checkpoint was recorded for the relaunched agent")
		return
	}
	if err := p.state.updateReset(rec.ResetID, func(r *ResetRecord) { r.Relaunching = true }); err != nil {
		p.recoveryRelaunchFailed(ctx, op, rec.ResetID, epoch, "RELAUNCH_BLOCKED", "the relaunch could not be recorded: "+err.Error())
		return
	}
	id, code, detail := p.relaunchAgent(ctx, op, w, token, rec.Old, rec.Checkpoint)
	if code != "" {
		p.recoveryRelaunchFailed(ctx, op, rec.ResetID, epoch, code, detail)
		return
	}
	if err := p.state.updateReset(rec.ResetID, func(r *ResetRecord) { r.New = &id }); err != nil {
		p.log("state-failed", op.ID, op.Type, "", err.Error())
	}
	if !p.recoveryStep(ctx, op, rec.ResetID, "RELAUNCH_UNVERIFIED", func() (ResetStep, error) {
		return p.resets.ReportResetRelaunched(ctx, rec.ResetID, epoch, id)
	}) {
		return
	}
	p.endRecovery(ctx, op, rec.ResetID, Succeeded, "")
}

// recoveryRelaunchFailed sends relaunchFailed(reason), then reports the
// operation FAILED with it.
func (p *Poller) recoveryRelaunchFailed(ctx context.Context, op Operation, resetID string, epoch int, reason, detail string) bool {
	if !p.recoveryStep(ctx, op, resetID, reason, func() (ResetStep, error) {
		return p.resets.ReportResetRelaunchFailed(ctx, resetID, epoch, reason)
	}) {
		return false
	}
	p.endRecovery(ctx, op, resetID, Failed, reason+": "+detail)
	return true
}

// annotateLateStop records a late stop on a reset that ended without the
// agent's stop being reported (it did not exit): agentStopped with
// lateStopMs on the ended reset, under the stop grant's epoch, which the
// server takes as telemetry only, changing no reset, task or availability
// state (#3255). Never sent for an on-time stop. Best effort: the reset
// has already ended, so a failure is logged, not retried.
func (p *Poller) annotateLateStop(ctx context.Context, op Operation, resetID string, epoch int, late time.Duration) {
	if late <= 0 {
		return
	}
	if _, err := p.resets.ReportResetAgentStopped(ctx, resetID, epoch, late); err != nil {
		p.log("reset", op.ID, op.Type, "", "the late stop could not be annotated: "+err.Error())
	}
}

// lateStopMs is late in whole milliseconds, rounded up, as lateStopMs is
// sent.
func lateStopMs(late time.Duration) int64 {
	return int64((late + time.Millisecond - 1) / time.Millisecond)
}

// recoveryStep makes one protocol step during recovery and says whether to
// go on. A step that ended the reset, or that the server rejected with a
// typed code, ends recovery with a FAILED report (failReason, or the
// server's refusal); a transport failure proves nothing and keeps the
// record without a report.
func (p *Poller) recoveryStep(ctx context.Context, op Operation, resetID, failReason string, step func() (ResetStep, error)) bool {
	res, err := step()
	switch {
	case err != nil && ResetStepCode(err) == "":
		p.log("reset-recovery", op.ID, op.Type, "", "a step failed in transport; kept: "+err.Error())
		return false
	case err != nil:
		p.endRecovery(ctx, op, resetID, Failed, failReason+": the server rejected a recovery step: "+err.Error())
		return false
	case res.Refused != "":
		p.endRecovery(ctx, op, resetID, Failed, res.Refused+": the server ended the reset during recovery")
		return false
	}
	return true
}

// endRecovery reports the operation's outcome and drops the record once
// the report reached the server.
func (p *Poller) endRecovery(ctx context.Context, op Operation, resetID, outcome, reason string) {
	if _, err := p.report(ctx, op, outcome, "", reason); err != nil {
		return
	}
	p.dropResetRecord(resetID)
}

func (p *Poller) dropResetRecord(id string) {
	if err := p.state.dropReset(id); err != nil {
		p.log("state-failed", "", "", "", err.Error())
	}
}

// processAlive says whether the recorded process is still that process
// (same pid and start time) and whether that is known: only ESRCH or a
// different start time proves it gone; any other failure to observe it is
// unknown, which each caller resolves to its own safe side.
// recordedLate is the lateness written when the signal returned.
func recordedLate(rec ResetRecord) time.Duration {
	return time.Duration(rec.LateStopMs) * time.Millisecond
}

// replacedAgent says whether a live agent other than the reset's old one is
// tracked for its worker: one this host launched since, so a replacement.
func (p *Poller) replacedAgent(rec ResetRecord) bool {
	p.mu.Lock()
	a := p.agents[rec.WorkerID]
	p.mu.Unlock()
	if a == nil || a.PID() == rec.Old.PID {
		return false
	}
	select {
	case <-a.Done():
		return false
	default:
		return true
	}
}

func (p *Poller) processAlive(id ProcessIdentity) (alive, known bool) {
	h, err := p.resetDeps.Proc.Observe(id.PID)
	if err != nil {
		return false, errors.Is(err, syscall.ESRCH)
	}
	defer h.Close()
	return h.Identity() == id, true
}
