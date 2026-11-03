package hostpoller

import "context"

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

// recoveryAction is the decision for one reset. Provisional marks a row
// still awaiting the spec owner's confirmation (#1043 comment 6593190456).
type recoveryAction struct {
	Kind        recoverKind
	Reason      string
	Provisional bool
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
	provisional := func(k recoverKind, reason string) recoveryAction {
		return recoveryAction{Kind: k, Reason: reason, Provisional: true}
	}
	switch r.Phase {
	case "requested":
		// No claim held, and the observation is gone: nothing to re-run.
		return provisional(recoverReportFailed, "PROCESS_CHANGED")
	case "claimed":
		// No process control happened; the checkpoint is not proven written.
		return provisional(recoverRelaunchFailed, "CHECKPOINT_FAILED")
	case "checkpointed":
		// No process control happened; the staged prompt is not proven intact.
		return provisional(recoverRelaunchFailed, "STAGING_FAILED")
	case "stop-granted":
		// The lease has expired and the host never signals on recovery.
		if oldAlive {
			return recoveryAction{Kind: recoverRelaunchFailed, Reason: "RELAUNCH_BLOCKED"}
		}
		// The signal may have landed before the crash.
		return provisional(recoverStoppedThenRelaunch, "")
	case "agent-stopped":
		if oldAlive {
			return recoveryAction{Kind: recoverReportFailed, Reason: "PROCESS_CHANGED"}
		}
		return recoveryAction{Kind: recoverRelaunch}
	case "relaunched":
		if newAlive {
			return provisional(recoverReportSucceeded, "")
		}
		return provisional(recoverReportFailed, "RELAUNCH_UNVERIFIED")
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
