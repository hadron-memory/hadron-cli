package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// hadron-cli#3017: no host this CLI runs carries out worker.reset-context.
// A config naming the capability is refused at load (the pin in
// internal/hostpoller/config_reset_refusal_3017_test.go), so a registration
// payload can never declare it; a server that issues the operation anyway —
// one that still knows the code — gets an answer naming the removal, not a
// timeout waiting on a handler that does not exist.
func TestCollectedResetContextIsAnsweredFailedWithTheRemoval3017(t *testing.T) {
	cfg := writeHostConfig(t, "")
	op := strings.Replace(polledOpJSON("r1", "worker.reset-context", "COLLECTED"), `"args":{}`, `"args":{"resetId":"reset1","sessionId":"s1","taskId":"t1"}`, 1)
	srv, calls := hostServerFake(t, map[string]string{
		"RegisterHost":          hostRegistrationAnswer,
		"CollectHostOperations": `{"data":{"collectHostOperations":[` + op + `]}}`,
		"ReportHostOperation":   `{"data":{"reportHostOperation":` + polledOpJSON("r1", "worker.reset-context", "FAILED") + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("run --once: %v", err)
	}
	got := calls()
	if len(got) != 3 {
		t.Fatalf("calls %+v", got)
	}
	var registration struct {
		Input struct {
			Capabilities []string `json:"capabilities"`
		} `json:"input"`
	}
	if err := json.Unmarshal(got[0].vars, &registration); err != nil {
		t.Fatal(err)
	}
	if len(registration.Input.Capabilities) != 0 {
		t.Fatalf("the registration declared capabilities the host does not carry out: %+v", registration)
	}
	var report struct {
		Input struct {
			Reason  string `json:"failureReason"`
			Outcome string `json:"outcome"`
		} `json:"input"`
	}
	if err := json.Unmarshal(got[2].vars, &report); err != nil {
		t.Fatal(err)
	}
	if report.Input.Outcome != "FAILED" || !strings.HasPrefix(report.Input.Reason, "HOST_UNSUPPORTED: ") || !strings.Contains(report.Input.Reason, "worker.reset-context") || !strings.Contains(report.Input.Reason, "hadron-cli#3017") {
		t.Fatalf("a collected reset-context must be answered FAILED with the pre-claim code naming the removal (the server refuses an unlisted reason with BAD_CONTEXT_RESET_INPUT and re-delivers), got outcome=%q reason=%q", report.Input.Outcome, report.Input.Reason)
	}
}
