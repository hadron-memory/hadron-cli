package cmd

import "testing"

// Wolfgang #108587 supersedes the earlier host-up profile/config proposal:
// host up only reports, and an unconnected poller is unproven.
func TestHostUpRelaunchOnlyReportsWithoutPoller2060(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Iris", 0)
	fx.writeDesired("APPLY", hostUpWorker1389{ID: "wkr-iris", Name: "Iris", Desired: "RUNNING", Reason: "LONG_LIVED", Session: "s-iris"})
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("report failed: %v\n%s", err, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Iris worker.relaunch RELAUNCH_UNPROVEN")
	if calls := fx.mutations(); len(calls) != 0 {
		t.Fatalf("host up mutated for relaunch: %v", calls)
	}
}
