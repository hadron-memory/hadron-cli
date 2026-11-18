package cmd

import (
	"encoding/json"
	"os"
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
	if out.SweepRun.Recorded || out.SweepRun.Reason != "HOST_CONFIG_UNREADABLE" || out.SweepRun.RunID != "run:replay" {
		t.Fatalf("wrong record result: %s", raw)
	}
}

// cli#2156 F14: an absent host key reads as its own closed code, apart from an
// unreadable configuration. Neither code can carry a path or credential.
func TestHostUpRecordFailureNamesAnAbsentKey2156(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.writeDesired("OBSERVE")
	cfg := writeHostConfig(t, "")
	raw0, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(strings.Replace(string(raw0), "host.key", "absent.key", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HADRON_HOST_KEY", "")
	_, raw, err := fx.run("--config", cfg, "--run-id", "run:replay")
	if err != nil {
		t.Fatalf("record failure changed reconcile result: %v", err)
	}
	if !strings.Contains(raw, `"reason": "HOST_KEY_UNAVAILABLE"`) && !strings.Contains(raw, `"reason":"HOST_KEY_UNAVAILABLE"`) {
		t.Fatalf("absent key must keep HOST_KEY_UNAVAILABLE: %s", raw)
	}
	if strings.Contains(raw, "absent.key") {
		t.Fatalf("code leaked the key path: %s", raw)
	}
}
