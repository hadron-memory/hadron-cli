package team

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func TestSweepDaemonDownReadsAndRecordsButIssuesNoOperation2056(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "manager-only")
	writes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(api.SessionHeader) != "" {
			t.Error("host call carried worker session")
		}
		var q struct {
			Op   string `json:"operationName"`
			Vars struct {
				Input map[string]any `json:"input"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch q.Op {
		case "HostDesiredState":
			fmt.Fprint(w, `{"data":{"hostDesiredState":{"hostId":"h1","sweepMode":"APPLY","workers":[{"workerId":"w1","name":"Iris","lifecycle":"LONG_LIVED","desired":"RUNNING","reason":"LONG_LIVED","openSessionId":"open","family":"claude","worktrees":["iris"]}]}}}`)
		case "TeamHostSetup":
			fmt.Fprint(w, `{"data":{"hostSetup":{"source":"HOST","assignments":[]}}}`)
		case "HostRegistrations":
			t.Error("unavailable sweep reached host observation")
			fmt.Fprint(w, `{"data":{"hostRegistrations":[{"hostId":"h1","live":true,"workers":[{"workerId":"w1","worker":{"id":"w1","name":"Iris"},"family":"claude","worktrees":["iris"]}]}]}}`)
		case "HostSweepIdentity":
			if r.Header.Get("Authorization") != "Bearer host-only" {
				t.Error("identity borrowed manager")
			}
			fmt.Fprint(w, `{"data":{"hostRegistrations":[{"hostId":"h1"}]}}`)
		case "RecordHostSweepRun":
			writes++
			if r.Header.Get("Authorization") != "Bearer host-only" {
				t.Error("record borrowed manager")
			}
			if q.Vars.Input["mode"] != "APPLY" || q.Vars.Input["applied"] != float64(0) || q.Vars.Input["failed"] != float64(0) || q.Vars.Input["skipped"] != float64(1) {
				t.Errorf("wrong down counts: %+v", q.Vars.Input)
			}
			lines := q.Vars.Input["lines"].([]any)
			if lines[0].(map[string]any)["reason"] != "ORCA_DOWN" {
				t.Error("down reason missing")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"recordHostSweepRun": map[string]any{"id": "r1", "runId": q.Vars.Input["runId"]}}})
		default:
			t.Errorf("unavailable sweep issued %s", q.Op)
			fmt.Fprint(w, `{"errors":[{"message":"unexpected"}]}`)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "key"), []byte("host-only"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "hadron-host-state.json"), []byte(`{"hostId":"h1"}`), 0600)
	fakeOrca := filepath.Join(dir, "orca")
	if err := os.WriteFile(fakeOrca, []byte("#!/bin/sh\ncase \"$1\" in\nstatus) printf '%s' '{\"ok\":true,\"result\":{\"runtime\":{\"reachable\":true}}}';;\nterminal) printf '%s' '{\"ok\":true,\"result\":{\"terminals\":[]}}';;\n*) exit 1;;\nesac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "iris"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "host.yaml")
	_ = os.WriteFile(cfg, []byte(fmt.Sprintf("server: %s\napp: hrn:app:example.com:team\nkey_file: key\nworktrees_root: %s\nworkers:\n  Iris:\n    family: claude\n    launch: [echo]\n    worktrees: [iris]\ndelivery:\n  orca_bin: %s\n", srv.URL, dir, fakeOrca)), 0600)
	old := sweepDaemonReady
	t.Cleanup(func() { sweepDaemonReady = old })
	sweepDaemonReady = func(context.Context, *hostpoller.Config) error { return hostpoller.ErrOrcaDown }
	streams, out, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: streams, JSON: true}
	c := newCmdTeamHostSweep(f)
	c.SilenceErrors = true
	c.SilenceUsage = true
	c.SetContext(api.WithSession(context.Background(), "must-not-send"))
	c.SetArgs([]string{"--config", cfg})
	if err := c.Execute(); exitcode.FromError(err) != exitcode.Error {
		t.Fatalf("want ORCA_DOWN exit1, got %v", err)
	}
	var dto hostUpDTO
	if err := json.Unmarshal(out.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if writes != 1 || !dto.SweepRun.Recorded || len(dto.Planned) != 0 || len(dto.Applied) != 0 {
		t.Fatalf("wrong unavailable sweep: %+v", dto)
	}
}
