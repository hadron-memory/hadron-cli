package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHostUpWithoutConfigReportsNoHostKey2056(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.writeDesired("OBSERVE")
	_, raw, err := fx.run()
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		SweepRun struct {
			Recorded bool   `json:"recorded"`
			Reason   string `json:"reason"`
		} `json:"sweepRun"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.SweepRun.Recorded || out.SweepRun.Reason != "NO_HOST_KEY" {
		t.Fatalf("missing no-key report: %s", raw)
	}
	for _, op := range fx.mutations() {
		if strings.Contains(op, "Sweep") {
			t.Fatalf("no config must not write a sweep: %s", op)
		}
	}
}

func TestHostUpRecordFailureDoesNotChangeReconcileExit2056(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.writeDesired("OBSERVE")
	_, raw, err := fx.run("--config", fx.root+"/missing-host.yaml", "--run-id", "run:replay")
	if err != nil {
		t.Fatalf("record failure changed reconcile result: %v", err)
	}
	var out struct {
		SweepRun struct {
			Recorded bool   `json:"recorded"`
			Reason   string `json:"reason"`
			RunID    string `json:"runId"`
		} `json:"sweepRun"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.SweepRun.Recorded || out.SweepRun.Reason != "HOST_KEY_UNAVAILABLE" || out.SweepRun.RunID != "run:replay" {
		t.Fatalf("wrong record result: %s", raw)
	}
}
