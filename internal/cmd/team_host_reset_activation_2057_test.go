//go:build linux

package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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
