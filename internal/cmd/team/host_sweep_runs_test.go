package team

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/hostup"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func TestSweepRecordUsesOnlyHostKeyAndCodeLines2056(t *testing.T) {
	calls := 0
	var input map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-host-key" || r.Header.Get("X-Hadron-Session") != "" {
			t.Errorf("wrong credential provenance: %q session=%q", r.Header.Get("Authorization"), r.Header.Get("X-Hadron-Session"))
		}
		var req struct {
			Operation string `json:"operationName"`
			Variables struct {
				Input map[string]any `json:"input"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Operation {
		case "HostSweepIdentity":
			fmt.Fprint(w, `{"data":{"hostDesiredState":{"hostId":"orca1"}}}`)
		case "RecordHostSweepRun":
			calls++
			input = req.Variables.Input
			fmt.Fprint(w, `{"data":{"recordHostSweepRun":{"id":"row1","runId":"replay:1"}}}`)
		default:
			t.Errorf("unexpected operation %s", req.Operation)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte("private-host-key"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "host.yaml")
	raw := fmt.Sprintf("server: %s\napp: test-app\nkey_file: %s\nworktrees_root: %s\nworkers:\n  Iris:\n    family: claude\n    launch: [echo]\n    worktrees: [iris]\n", srv.URL, key, dir)
	if err := os.WriteFile(cfg, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	io, _, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: io, ServerFlag: srv.URL}
	res := hostup.Result{SweepMode: "APPLY", Planned: []hostup.Item{{WorkerID: "worker-0"}, {WorkerID: "worker-0"}}}
	for i := 0; i < 60; i++ {
		res.Failed = append(res.Failed, hostup.Item{WorkerID: fmt.Sprintf("worker-%d", i), Worker: "SECRET NAME", Reason: "free text SECRET", Detail: "never send SECRET"})
	}
	t.Setenv("HADRON_TOKEN", "manager-must-not-be-used")
	for i := 0; i < 2; i++ {
		got := recordSweepRun(context.Background(), f, cfg, "test-app", "orca1", "replay:1", time.Now(), true, res)
		if !got.Recorded {
			t.Fatalf("record failed: %+v", got)
		}
	}
	if calls != 2 || input["runId"] != "replay:1" || input["failed"] != float64(60) || input["planned"] != float64(1) {
		t.Fatalf("wrong recording: calls=%d input=%v", calls, input)
	}
	lines, ok := input["lines"].([]any)
	if !ok || len(lines) != 50 {
		t.Fatalf("line cap: %v", input["lines"])
	}
	rawInput, _ := json.Marshal(input)
	if strings.Contains(string(rawInput), "SECRET") {
		t.Fatalf("free text leaked: %s", rawInput)
	}
	for _, v := range lines {
		line := v.(map[string]any)
		if line["reason"] != "UNKNOWN_REASON" || line["outcome"] != "FAILED" {
			t.Fatal(line)
		}
	}
}

func TestSweepRecordCompatibilityAndRefusals2056(t *testing.T) {
	for _, tc := range []struct{ name, response, want string }{
		{"old", `{"errors":[{"message":"Cannot query field \"recordHostSweepRun\" on type \"Mutation\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, "S10_UNAVAILABLE"},
		{"refused", `{"errors":[{"message":"secret refusal text","extensions":{"code":"HOST_KEY_REQUIRED"}}]}`, "HOST_KEY_REQUIRED"},
		{"null", `{"data":{"recordHostSweepRun":null}}`, "RECORD_UNCONFIRMED"},
		{"wrong-run-id", `{"data":{"recordHostSweepRun":{"id":"r","runId":"other"}}}`, "RECORD_UNCONFIRMED"},
		{"empty-record-id", `{"data":{"recordHostSweepRun":{"id":"","runId":"run1"}}}`, "RECORD_UNCONFIRMED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					Operation string `json:"operationName"`
				}
				_ = json.NewDecoder(r.Body).Decode(&q)
				w.Header().Set("Content-Type", "application/json")
				if q.Operation == "HostSweepIdentity" {
					fmt.Fprint(w, `{"data":{"hostDesiredState":{"hostId":"orca1"}}}`)
					return
				}
				if q.Operation != "RecordHostSweepRun" {
					t.Errorf("unexpected %s", q.Operation)
				}
				writes++
				fmt.Fprint(w, tc.response)
			}))
			defer srv.Close()
			dir := t.TempDir()
			key := filepath.Join(dir, "key")
			_ = os.WriteFile(key, []byte("host-key"), 0600)
			cfg := filepath.Join(dir, "host.yaml")
			_ = os.WriteFile(cfg, []byte(fmt.Sprintf("server: %s\napp: app\nkey_file: %s\nworktrees_root: %s\nworkers:\n  Iris:\n    family: claude\n    launch: [echo]\n", srv.URL, key, dir)), 0600)
			io, _, _ := output.Test()
			f := &cmdutil.Factory{IOStreams: io, ServerFlag: srv.URL}
			got := recordSweepRun(context.Background(), f, cfg, "app", "orca1", "run1", time.Now(), false, hostup.Result{SweepMode: "OBSERVE"})
			if got.Recorded || got.Reason != tc.want || writes != 1 {
				t.Fatalf("got %+v writes=%d", got, writes)
			}
		})
	}
}

func TestSweepInstallUsesOneMinuteUnitsWithoutCredential2056(t *testing.T) {
	// Installation resolves Orca against the saved PATH/config, independently
	// of the fake scheduler's Which. Never borrow a developer's real Orca.
	t.Setenv("PATH", "/nonexistent")
	orca := filepath.Join(t.TempDir(), "orca")
	if err := os.WriteFile(orca, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, home := healthHarness(t, "systemctl", "logger")
	cfg := hostConfig(t, orca)
	io, _, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: io}
	c := newCmdTeamHostSweep(f)
	c.SetArgs([]string{"install", "--config", cfg, "--bin", "/usr/bin/hadron"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	service, err := os.ReadFile(filepath.Join(home, ".config/systemd/user/hadron-team-sweep.service"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(service)
	for _, want := range []string{`"team" "host" "sweep"`, "Restart=on-failure", "StartLimitBurst=3", "OnFailure="} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(text, "SENTINELKEY") || strings.Contains(text, "health\" \"sweep") {
		t.Fatalf("wrong unit argv: %s", text)
	}
}

func TestSweepHistoryEmptyAndMalformed2056(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"empty", `{"data":{"hostSweepRuns":[]}}`, false},
		{"null", `{"data":{"hostSweepRuns":null}}`, true},
		{"row", `{"data":{"hostSweepRuns":[null]}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Hadron-Session") != "" {
					t.Error("read carried session")
				}
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			io, out, _ := output.Test()
			f := &cmdutil.Factory{IOStreams: io, ServerFlag: srv.URL, AppFlag: "hrn:app:example.com:team", JSON: true}
			t.Setenv("HADRON_TOKEN", "reader-key")
			c := newCmdTeamHostSweepRuns(f)
			c.SetArgs([]string{"--host", "orca1"})
			err := c.Execute()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v output=%s", err, out.String())
			}
			if !tc.wantErr && strings.TrimSpace(out.String()) != "[]" {
				t.Fatalf("empty list not []: %s", out.String())
			}
		})
	}
}

func TestSweepIdentityProvesHostWithScopedRead2137(t *testing.T) {
	for _, scenario := range []string{"valid", "alias", "wrong-host", "missing", "second-app-error", "second-app-wrong-host", "transport", "server-mismatch", "server-trailing-slash"} {
		t.Run(scenario, func(t *testing.T) {
			reads, writes := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					Op    string         `json:"operationName"`
					Query string         `json:"query"`
					Vars  map[string]any `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				if q.Op == "HostSweepIdentity" {
					reads++
					wantApp := "app"
					if reads == 2 {
						wantApp = "alias"
					}
					if q.Vars["appRef"] != wantApp {
						t.Errorf("identity App=%v want%s", q.Vars["appRef"], wantApp)
					}
					if q.Vars["hostId"] != "orca1" || !strings.Contains(q.Query, "hostDesiredState(") || strings.Contains(q.Query, "hostRegistrations(") {
						t.Errorf("not host-scoped proof: %+v", q)
					}
					if scenario == "transport" {
						w.WriteHeader(503)
						return
					}
					if scenario == "second-app-error" && reads == 2 {
						fmt.Fprint(w, `{"errors":[{"message":"other App","extensions":{"code":"APP_NOT_FOUND"}}]}`)
						return
					}
					if scenario == "wrong-host" || (scenario == "second-app-wrong-host" && reads == 2) {
						fmt.Fprint(w, `{"data":{"hostDesiredState":{"hostId":"other"}}}`)
						return
					}
					if scenario == "missing" {
						fmt.Fprint(w, `{"data":{"hostDesiredState":null}}`)
						return
					}
					fmt.Fprint(w, `{"data":{"hostDesiredState":{"hostId":"orca1"}}}`)
					return
				}
				if q.Op != "RecordHostSweepRun" {
					t.Errorf("unexpected %s", q.Op)
				}
				writes++
				fmt.Fprint(w, `{"data":{"recordHostSweepRun":{"id":"r1","runId":"run1"}}}`)
			}))
			defer srv.Close()
			dir := t.TempDir()
			key := filepath.Join(dir, "key")
			if err := os.WriteFile(key, []byte("host-key"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(dir, "host.yaml")
			if err := os.WriteFile(cfg, []byte(fmt.Sprintf("server: %s\napp: app\nkey_file: %s\nworktrees_root: %s\nworkers:\n  Iris:\n    family: claude\n    launch: [echo]\n", srv.URL, key, dir)), 0600); err != nil {
				t.Fatal(err)
			}
			streams, _, _ := output.Test()
			f := &cmdutil.Factory{IOStreams: streams, ServerFlag: srv.URL}
			app := "app"
			if scenario == "alias" || scenario == "second-app-error" || scenario == "second-app-wrong-host" {
				app = "alias"
			}
			if scenario == "server-mismatch" {
				f.ServerFlag = srv.URL + "/different"
			}
			if scenario == "server-trailing-slash" {
				f.ServerFlag = srv.URL + "/"
			}
			got := recordSweepRun(context.Background(), f, cfg, app, "orca1", "run1", time.Now(), true, hostup.Result{SweepMode: "APPLY"})
			want := "RECORDED"
			switch scenario {
			case "wrong-host", "missing", "second-app-wrong-host":
				want = "HOST_SCOPE_MISMATCH"
			case "second-app-error":
				want = "APP_NOT_FOUND"
			case "transport":
				want = "HOST_IDENTITY_FAILED"
			case "server-mismatch":
				want = "SERVER_MISMATCH"
			}
			if got.Reason != want {
				t.Fatalf("got %+v want %s", got, want)
			}
			expectedWrites := 0
			if want == "RECORDED" {
				expectedWrites = 1
			}
			if writes != expectedWrites {
				t.Fatalf("record calls %d", writes)
			}
			if scenario == "alias" && reads != 2 {
				t.Fatalf("alias not proved: %d reads", reads)
			}
			if scenario == "server-mismatch" && reads != 0 {
				t.Fatalf("server mismatch issued reads: %d", reads)
			}
		})
	}
}
