//go:build linux

package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// C2c wires the existing handler, rather than inventing another reset.
// An untracked process must reach its safe refusal only when opted in.
func TestHostResetCapabilityWiresOnlyOnDeclaration2057(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "declared"}[enabled], func(t *testing.T) {
			extra := "reset_context:\n  orca_bin: /fake/orca\n"
			if enabled {
				extra = "capabilities: [worker.reset-context]\n" + extra
			}
			cfg := writeHostConfig(t, extra)
			raw, _ := os.ReadFile(cfg)
			raw = []byte(strings.Replace(string(raw), "launch: [claude-worker]", "launch: [claude-worker]\n    recover: [recover-worker]", 1))
			if err := os.WriteFile(cfg, raw, 0600); err != nil {
				t.Fatal(err)
			}
			op := strings.Replace(polledOpJSON("r1", "worker.reset-context", "COLLECTED"), `"args":{}`, `"args":{"resetId":"reset1","sessionId":"s1","taskId":"t1"}`, 1)
			srv, calls := hostServerFake(t, map[string]string{"RegisterHost": hostRegistrationAnswer, "CollectHostOperations": `{"data":{"collectHostOperations":[` + op + `]}}`, "ReportHostOperation": `{"data":{"reportHostOperation":` + polledOpJSON("r1", "worker.reset-context", "FAILED") + `}}`})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
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
			if (len(registration.Input.Capabilities) == 1) != enabled {
				t.Fatalf("capabilities %+v", registration)
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
			want := "HOST_UNSUPPORTED"
			if enabled {
				want = "PROCESS_CHANGED"
			}
			if report.Input.Outcome != "FAILED" || !strings.HasPrefix(report.Input.Reason, want+":") {
				t.Fatalf("wanted %s: %s", want, got[2].vars)
			}
			if enabled && strings.Contains(f.IOStreams.ErrOut.(*strings.Builder).String(), "has no handler") {
				t.Fatal("declared wired capability still warns unsupported")
			}
			for _, c := range got {
				if c.auth != "Bearer hk_host_key" || c.session != "" {
					t.Fatalf("wrong identity %+v", c)
				}
			}
		})
	}
}

func TestHostResetDeclarationNeedsIdleReader2057(t *testing.T) {
	for _, path := range []string{"", "relative-orca"} {
		cfg := writeHostConfig(t, "capabilities: [worker.reset-context]\nreset_context:\n  orca_bin: '"+path+"'\n")
		srv, calls := hostServerFake(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
		if code := exitCodeFor(root.Execute()); code != exitcode.Usage {
			t.Fatalf("exit %d want usage", code)
		}
		if len(calls()) != 0 {
			t.Fatal("declared before proving its prerequisites")
		}
	}
}

// A new host-run instance has no in-memory launched agents. The server's
// collect contract includes unsettled COLLECTED operations (including resets),
// so the persisted record must route to phase recovery before launch checks.
func TestHostResetRestartUsesRedeliveredOperation2057(t *testing.T) {
	for _, phase := range []string{"done", "claimed"} {
		t.Run(phase, func(t *testing.T) {
			cfgPath := writeHostConfig(t, "capabilities: [worker.reset-context]\nreset_context:\n  orca_bin: /not-invoked/orca\n")
			cfg, err := hostpoller.LoadConfig(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			saved := hostpoller.State{Resets: map[string]hostpoller.ResetRecord{"reset1": {ResetID: "reset1", OperationID: "r1", WorkerID: "wkr1", Old: hostpoller.ProcessIdentity{PID: 1 << 30, StartTime: "old"}}}}
			raw, err := json.Marshal(&saved)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg.StateFile, raw, 0600); err != nil {
				t.Fatal(err)
			}
			op := strings.Replace(polledOpJSON("r1", "worker.reset-context", "COLLECTED"), `"args":{}`, `"args":{"resetId":"reset1","sessionId":"s1","taskId":"t1"}`, 1)
			outcome := "SUCCEEDED"
			if phase == "claimed" {
				outcome = "FAILED"
			}
			answer := `{"data":{"contextReset":{"id":"reset1","epoch":2,"phase":"` + phase + `","workerId":"wkr1","sessionId":"s1","taskId":"t1"}}}`
			srv, calls := hostServerFake(t, map[string]string{
				"RegisterHost":                     hostRegistrationAnswer,
				"CollectHostOperations":            `{"data":{"collectHostOperations":[` + op + `]}}`,
				"ContextReset":                     answer,
				"ReportContextResetRelaunchFailed": `{"data":{"reportContextResetRelaunchFailed":{"refused":null,"replayed":false,"reset":{"id":"reset1","epoch":2,"phase":"failed"}}}}`,
				"ReportHostOperation":              `{"data":{"reportHostOperation":` + polledOpJSON("r1", "worker.reset-context", outcome) + `}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfgPath, "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			got := calls()
			want := []string{"RegisterHostWithRefused", "CollectHostOperations", "ContextReset"}
			if phase == "claimed" {
				want = append(want, "ReportContextResetRelaunchFailed")
			}
			want = append(want, "ReportHostOperation")
			if len(got) != len(want) {
				t.Fatalf("calls %+v want %v", got, want)
			}
			for i, c := range got {
				if c.op != want[i] {
					t.Fatalf("call %d=%s want %s", i, c.op, want[i])
				}
				if c.auth != "Bearer hk_host_key" || c.session != "" {
					t.Fatalf("wrong identity %+v", c)
				}
			}
			var report struct {
				Input struct {
					Outcome string  `json:"outcome"`
					Reason  *string `json:"failureReason"`
				} `json:"input"`
			}
			if err := json.Unmarshal(got[len(got)-1].vars, &report); err != nil {
				t.Fatal(err)
			}
			if report.Input.Outcome != outcome {
				t.Fatalf("report %+v", report)
			}
			if phase == "claimed" && (report.Input.Reason == nil || !strings.HasPrefix(*report.Input.Reason, "CHECKPOINT_FAILED:")) {
				t.Fatalf("unproven checkpoint was not refused: %+v", report)
			}
			state, err := hostpoller.LoadState(cfg.StateFile)
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Resets) != 0 {
				t.Fatalf("finished reset record retained: %+v", state.Resets)
			}
		})
	}
}
