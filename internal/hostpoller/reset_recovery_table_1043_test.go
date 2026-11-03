package hostpoller

import "testing"

// TestRecoveryActionTable pins what a restarted host does with each reset
// it collected, by the phase the server holds (#1043, comment 6593190456).
// Nothing is ever signalled on recovery and no step is retried: the table
// only relaunches (after a proven stop), reports, or keeps the record.
//
// Rows marked provisional are the engineer's proposal, awaiting the spec
// owner's confirmation; the pin on Provisional makes the confirmation a
// visible one-row edit.
func TestRecoveryActionTable(t *testing.T) {
	alive, gone := true, false
	cases := []struct {
		name     string
		phase    string
		reason   string
		oldAlive bool
		newAlive bool
		want     recoveryAction
	}{
		// Fixed by the issue body (✱).
		{"stop-granted, old alive", "stop-granted", "", alive, gone, recoveryAction{Kind: recoverRelaunchFailed, Reason: "RELAUNCH_BLOCKED"}},
		{"agent-stopped, old gone", "agent-stopped", "", gone, gone, recoveryAction{Kind: recoverRelaunch}},
		{"agent-stopped, old alive", "agent-stopped", "", alive, gone, recoveryAction{Kind: recoverReportFailed, Reason: "PROCESS_CHANGED"}},
		// Ended: report once (the read answered, so the operation is still
		// collected and unsettled), then drop.
		{"done", "done", "", gone, alive, recoveryAction{Kind: recoverReportSucceeded}},
		{"refused", "refused", "NOT_IDLE", alive, gone, recoveryAction{Kind: recoverReportFailed, Reason: "NOT_IDLE"}},
		{"failed", "failed", "CLAIM_LAPSED", alive, gone, recoveryAction{Kind: recoverReportFailed, Reason: "CLAIM_LAPSED"}},
		// Provisional, awaiting the spec owner.
		{"requested", "requested", "", alive, gone, recoveryAction{Kind: recoverReportFailed, Reason: "PROCESS_CHANGED", Provisional: true}},
		{"claimed", "claimed", "", alive, gone, recoveryAction{Kind: recoverRelaunchFailed, Reason: "CHECKPOINT_FAILED", Provisional: true}},
		{"checkpointed", "checkpointed", "", alive, gone, recoveryAction{Kind: recoverRelaunchFailed, Reason: "STAGING_FAILED", Provisional: true}},
		{"stop-granted, old gone", "stop-granted", "", gone, gone, recoveryAction{Kind: recoverStoppedThenRelaunch, Provisional: true}},
		{"relaunched, new alive", "relaunched", "", gone, alive, recoveryAction{Kind: recoverReportSucceeded, Provisional: true}},
		{"relaunched, new gone", "relaunched", "", gone, gone, recoveryAction{Kind: recoverReportFailed, Reason: "RELAUNCH_UNVERIFIED", Provisional: true}},
		// A phase this host does not know: keep the record, touch nothing.
		{"unknown phase", "something-new", "", gone, gone, recoveryAction{Kind: recoverKeep}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decideRecovery(ContextReset{Phase: c.phase, Reason: c.reason}, c.oldAlive, c.newAlive)
			if got != c.want {
				t.Fatalf("decideRecovery(%s) = %+v, want %+v", c.phase, got, c.want)
			}
		})
	}
}

// TestRecoveryActionEndedWithoutReason: an ended reset the server gives no
// reason for still reports a well-formed FAILED (the server accepts any
// leading code once the reset has ended), never an empty one.
func TestRecoveryActionEndedWithoutReason(t *testing.T) {
	for _, phase := range []string{"refused", "failed"} {
		got := decideRecovery(ContextReset{Phase: phase}, false, false)
		if got.Kind != recoverReportFailed || got.Reason == "" {
			t.Fatalf("%s with no reason = %+v, want a FAILED report with a reason", phase, got)
		}
	}
}
