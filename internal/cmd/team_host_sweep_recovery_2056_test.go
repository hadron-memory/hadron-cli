package cmd

import "testing"

// The sweep uses this same host-up path. An open session is never sent through
// worker.start or a direct relaunch actuator, even on repeated applying runs.
func TestSweepReconciliationLeavesOpenSessionRecoveryToPoller2056(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.serverState("APPLY", serverWorker1389("wkr-jonas", "Jonas", "RUNNING", "HOLDS_TASK", "still-open", "hadron-cli/jonas"))
	for i := 0; i < 2; i++ {
		dto, raw, err := fx.run("--apply")
		if err != nil {
			t.Fatalf("%v %s", err, raw)
		}
		wantItems1389(t, "skipped", dto.Skipped, "Jonas worker.relaunch RELAUNCH_UNPROVEN")
	}
	if len(fx.mutations()) != 0 || len(fx.issued) != 0 {
		t.Fatalf("sweep took launch ownership: %v", fx.mutations())
	}
}
