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
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/healthsched"
	"github.com/hadron-memory/hadron-cli/internal/hostup"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// All writes and requests stay inside this fixture; no real host unit or Orca.
func sweepFixture2153(t *testing.T, serve func(http.ResponseWriter, *http.Request)) (*cmdutil.Factory, string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(serve))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	for name, body := range map[string]string{"key": "synthetic-host-key", "hadron-host-state.json": `{"hostId":"h1"}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(dir, "host.yaml")
	raw := fmt.Sprintf("server: %s\napp: app\nkey_file: key\nworktrees_root: %s\nworkers:\n  Iris:\n    family: claude\n    launch: [echo]\ndelivery:\n  orca_bin: /nonexistent/fixture-orca\n", srv.URL, dir)
	if err := os.WriteFile(cfg, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	streams, _, _ := output.Test()
	return &cmdutil.Factory{IOStreams: streams, ServerFlag: srv.URL}, cfg, dir
}

func TestSweepRecorderModes2153(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		apply bool
		want  string
	}{{"APPLY", true, "APPLY"}, {"APPLY", false, "OBSERVE"}, {"OBSERVE", true, "OBSERVE"}, {"OFF", true, "SWEEP_OFF"}, {"", true, "SWEEP_MODE_UNKNOWN"}, {"future", true, "SWEEP_MODE_UNKNOWN"}} {
		t.Run(fmt.Sprintf("%s/apply=%v", tc.mode, tc.apply), func(t *testing.T) {
			reads, writes := 0, 0
			f, cfg, _ := sweepFixture2153(t, func(w http.ResponseWriter, r *http.Request) {
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
				case "HostSweepIdentity":
					reads++
					fmt.Fprint(w, `{"data":{"hostDesiredState":{"hostId":"h1"}}}`)
				case "RecordHostSweepRun":
					writes++
					if q.Vars.Input["mode"] != tc.want {
						t.Errorf("record mode=%v want%s", q.Vars.Input["mode"], tc.want)
					}
					fmt.Fprint(w, `{"data":{"recordHostSweepRun":{"id":"r","runId":"run1"}}}`)
				default:
					t.Errorf("unexpected operation %s", q.Op)
				}
			})
			got := recordSweepRun(context.Background(), f, cfg, "app", "h1", "run1", time.Now(), tc.apply, hostup.Result{SweepMode: tc.mode})
			if reads != 1 {
				t.Fatalf("same App identity reads=%d", reads)
			}
			wantWrites := 0
			if tc.want == "APPLY" || tc.want == "OBSERVE" {
				wantWrites = 1
				if !got.Recorded || got.Reason != "RECORDED" {
					t.Fatalf("%+v", got)
				}
			} else if got.Recorded || got.Reason != tc.want {
				t.Fatalf("%+v want%s", got, tc.want)
			}
			if writes != wantWrites {
				t.Fatalf("record calls=%d want%d", writes, wantWrites)
			}
		})
	}
}

func TestSweepCommandScopeAndLockRefuseBeforeNetwork2153(t *testing.T) {
	for _, which := range []string{"app", "server", "lock"} {
		t.Run(which, func(t *testing.T) {
			calls := 0
			f, cfg, _ := sweepFixture2153(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				t.Error("refused command reached network")
				w.WriteHeader(500)
			})
			want := exitcode.Usage
			switch which {
			case "app":
				f.AppFlag = "other"
			case "server":
				f.ServerFlag += "/other"
			case "lock":
				dir, err := config.Dir()
				if err != nil {
					t.Fatal(err)
				}
				unlock, err := hostup.Lock(dir, "h1")
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
				want = exitcode.Conflict
			}
			c := newCmdTeamHostSweep(f)
			c.SilenceUsage = true
			c.SilenceErrors = true
			c.SetArgs([]string{"--config", cfg})
			err := c.Execute()
			if exitcode.FromError(err) != want || calls != 0 {
				t.Fatalf("err=%v calls=%d want exit%d", err, calls, want)
			}
		})
	}
}

func TestSweepRunIDBoundariesBeforeReads2153(t *testing.T) {
	// cor:agt:070:09 bounds/alphabet, including the alphanumeric first
	// character (Proposed built-as clarification by Vera, team #112616).
	for _, id := range []string{strings.Repeat("a", 65), "has space", "a/b", "a@b", "a\n", "é", ".run", "_run", ":run", "-run"} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			calls := 0
			f, _, dir := sweepFixture2153(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
			c := newCmdTeamHostUp(f)
			c.SilenceErrors = true
			c.SilenceUsage = true
			c.SetArgs([]string{"--host", "h1", "--worktrees-root", dir, "--run-id", id})
			err := c.Execute()
			if exitcode.FromError(err) != exitcode.Usage || calls != 0 || !strings.Contains(err.Error(), "--run-id") {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
	for _, id := range []string{"a", "a._:-9", strings.Repeat("z", 64)} {
		if !sweepRunToken.MatchString(id) {
			t.Errorf("valid token rejected: %q", id)
		}
	}
}

func TestSweepHistoryLimitsAndHumanOutput2153(t *testing.T) {
	for _, limit := range []int{0, 1, 100, 101} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			calls := 0
			f, _, _ := sweepFixture2153(t, func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					Op   string         `json:"operationName"`
					Vars map[string]any `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
				}
				if q.Op == "TeamAppIdentity" {
					fmt.Fprint(w, `{"data":{"app":{"id":"app-id","urn":"hrn:app:example.com:team","name":"Team"}}}`)
					return
				}
				calls++
				if q.Op != "HostSweepRuns" || q.Vars["limit"] != float64(limit) {
					t.Errorf("wrong request %+v", q)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"data":{"hostSweepRuns":[{"id":"r","hostId":"h1","runId":"run\u001b[31m","startedAt":"2026-01-01T00:00:00Z","endedAt":"2026-01-01T00:00:01Z","mode":"OBSERVE","planned":1,"applied":0,"skipped":1,"failed":0,"recordedAt":"2026-01-01T00:00:01Z","lines":[]}]}}`)
			})
			t.Setenv("HADRON_TOKEN", "synthetic-reader")
			f.AppFlag = "hrn:app:example.com:team"
			streams, out, _ := output.Test()
			f.IOStreams = streams
			c := newCmdTeamHostSweepRuns(f)
			c.SilenceErrors = true
			c.SilenceUsage = true
			c.SetArgs([]string{"--host", "h1", "--limit", fmt.Sprint(limit)})
			err := c.Execute()
			if limit == 0 || limit == 101 {
				if exitcode.FromError(err) != exitcode.Usage || calls != 0 {
					t.Fatalf("err=%v calls=%d", err, calls)
				}
				return
			}
			if err != nil || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			for _, want := range []string{"RUN", "STARTED", "OBSERVE", "run"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %s: %q", want, out.String())
				}
			}
			if strings.Contains(out.String(), "\x1b") {
				t.Fatalf("raw control in output %q", out.String())
			}
		})
	}
}

func TestSweepStatusUsesItsOwnJob2153(t *testing.T) {
	hf, _, home := healthHarness(t, "systemctl", "logger")
	streams, _, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: streams}
	e := healthEnv(f)
	e.Job = healthsched.Sweep
	if _, err := e.Install(healthsched.Spec{Bin: "/fake/hadron", Config: "/fake/host.yaml", Path: "/fake", Interval: time.Minute}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(home, ".config/systemd/user/hadron-team-sweep.service"))
	if err != nil {
		t.Fatal(err)
	}
	for _, asJSON := range []bool{false, true} {
		streams, out, _ := output.Test()
		f.IOStreams = streams
		f.JSON = asJSON
		start := len(hf.calls)
		c := newScheduledStatus(f, healthsched.Sweep)
		if err := c.Execute(); err != nil {
			t.Fatal(err)
		}
		if asJSON {
			var got healthStatusDTO
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !got.Installed || got.Scheduler == nil || *got.Scheduler != "systemd" || got.Config == nil || *got.Config != "/fake/host.yaml" {
				t.Fatalf("wrong status: %s", out.String())
			}
		} else if !strings.Contains(out.String(), "systemd") {
			t.Fatalf("missing human scheduler: %s", out.String())
		}
		for _, call := range hf.calls[start:] {
			if strings.Contains(call, "team-health") || strings.Contains(call, "daemon-reload") || strings.Contains(call, " enable ") {
				t.Fatalf("status touched wrong job or wrote scheduler: %s", call)
			}
		}
	}
	after, err := os.ReadFile(filepath.Join(home, ".config/systemd/user/hadron-team-sweep.service"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("status changed service: %v", err)
	}
}
