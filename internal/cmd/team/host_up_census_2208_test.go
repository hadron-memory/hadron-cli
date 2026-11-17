package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
	"github.com/hadron-memory/hadron-cli/internal/hostup"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/teamrouter"
)

// Serial dependency overrides retain the real command and adapter closures.
func censusDependencies2208(t *testing.T, ready func(*hostpoller.Config) func(context.Context) error, probe func(context.Context, string) (hostrelaunch.Observation, error)) {
	t.Helper()
	oldReady, oldProbe := hostUpReadinessFactory, hostUpProcessProbe
	hostUpReadinessFactory, hostUpProcessProbe = ready, probe
	t.Cleanup(func() { hostUpReadinessFactory, hostUpProcessProbe = oldReady, oldProbe })
	// An unwired override must never fall through to a real managed host.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestHostUpCensusNativeDefaults2208(t *testing.T) {
	if reflect.ValueOf(hostUpReadinessFactory).Pointer() != reflect.ValueOf(managedDaemonProbe).Pointer() {
		t.Fatal("readiness default is not the managed daemon proof")
	}
	if reflect.ValueOf(hostUpProcessProbe).Pointer() != reflect.ValueOf(hostpoller.ProbeWorktreeAgents).Pointer() {
		t.Fatal("process default is not the native strict census")
	}
}

func TestHostUpCensusProductionAdapter2208(t *testing.T) {
	unavailable := errors.New("synthetic proof unavailable")
	for _, tc := range []struct {
		name                   string
		readyFailure           int
		probeErr               error
		readyCalls, probeCalls int
		proven                 bool
	}{
		{name: "ready before and after", readyCalls: 2, probeCalls: 1, proven: true},
		{name: "not ready before", readyFailure: 1, readyCalls: 1},
		{name: "not ready after", readyFailure: 2, readyCalls: 2, probeCalls: 1},
		{name: "probe error", probeErr: unavailable, readyCalls: 1, probeCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readyCalls, probeCalls := 0, 0
			var child context.Context
			checkContext := func(ctx context.Context) {
				deadline, ok := ctx.Deadline()
				remaining := time.Until(deadline)
				if !ok || remaining <= 0 || remaining > 10*time.Second {
					t.Fatalf("adapter deadline=%v remaining=%v", ok, remaining)
				}
				if child == nil {
					child = ctx
				} else if child != ctx {
					t.Error("readiness/probe did not share the bounded context")
				}
			}
			work := t.TempDir()
			censusDependencies2208(t, func(cfg *hostpoller.Config) func(context.Context) error {
				if cfg.OrcaLaunch.OrcaBin != "private-orca" {
					t.Errorf("configured binary lost: %q", cfg.OrcaLaunch.OrcaBin)
				}
				return func(ctx context.Context) error {
					checkContext(ctx)
					readyCalls++
					if readyCalls == tc.readyFailure {
						return unavailable
					}
					return nil
				}
			}, func(ctx context.Context, got string) (hostrelaunch.Observation, error) {
				checkContext(ctx)
				probeCalls++
				if got != work {
					t.Errorf("probe worktree=%q want %q", got, work)
				}
				return hostrelaunch.Observation{Proven: true, PIDs: []int{42}, ForeignUIDSkipped: 7}, tc.probeErr
			})
			parent, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			got, err := hostUpCensus("private-orca")(parent, work)
			if readyCalls != tc.readyCalls || probeCalls != tc.probeCalls {
				t.Fatalf("ready=%d probe=%d, want %d/%d; err=%v", readyCalls, probeCalls, tc.readyCalls, tc.probeCalls, err)
			}
			if tc.proven {
				if err != nil || !got.Proven || !reflect.DeepEqual(got.PIDs, []int{42}) || got.ForeignUIDSkipped != 7 {
					t.Fatalf("complete proof lost: %+v %v", got, err)
				}
			} else {
				if !errors.Is(err, unavailable) || got.Proven || len(got.PIDs) != 0 || got.ForeignUIDSkipped != 0 {
					t.Fatalf("unavailable adapter leaked proof: %+v %v", got, err)
				}
			}
			if child == nil || child.Err() != context.Canceled {
				t.Fatal("adapter did not release its bounded context")
			}
		})
	}
}

func TestHostUpCensusParentCancellation2208(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	censusDependencies2208(t, func(*hostpoller.Config) func(context.Context) error {
		return func(ctx context.Context) error { return ctx.Err() }
	}, func(ctx context.Context, _ string) (hostrelaunch.Observation, error) {
		cancel()
		select {
		case <-ctx.Done():
		default:
			t.Fatal("probe context did not inherit cancellation")
		}
		return hostrelaunch.Observation{Proven: true}, ctx.Err()
	})
	got, err := hostUpCensus("private-orca")(parent, t.TempDir())
	if !errors.Is(err, context.Canceled) || got.Proven {
		t.Fatalf("cancelled proof accepted: %+v %v", got, err)
	}
}

func TestHostUpObserverCensusOperands2208(t *testing.T) {
	known := "claude"
	for _, tc := range []struct {
		name                          string
		identified, unknown           int
		pids                          []int
		calls, wantKnown, wantUnknown int
	}{
		{name: "no unknown rows skips census", identified: 1, calls: 0, wantKnown: 1},
		{name: "more identified rows than pids", identified: 2, unknown: 1, pids: []int{42}, calls: 1, wantKnown: 2},
		{name: "duplicate pids", identified: 2, unknown: 1, pids: []int{42, 42}, calls: 1, wantKnown: 3, wantUnknown: 1},
		{name: "negative pid", identified: 1, unknown: 1, pids: []int{-42}, calls: 1, wantKnown: 2, wantUnknown: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			terms := []teamrouter.Terminal{}
			for i := 0; i < tc.identified; i++ {
				terms = append(terms, teamrouter.Terminal{WorktreePath: work, AgentIdentity: &known})
			}
			for i := 0; i < tc.unknown; i++ {
				terms = append(terms, teamrouter.Terminal{WorktreePath: work})
			}
			raw, err := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminals": terms}})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			observer := hostUpObserver{root: work, registered: map[string]bool{}, runner: func(context.Context, []string) teamrouter.RunResult { return teamrouter.RunResult{Stdout: string(raw)} }, census: func(context.Context, string) (hostrelaunch.Observation, error) {
				calls++
				return hostrelaunch.Observation{Proven: true, PIDs: tc.pids}, nil
			}}
			got, err := observer.Observe(context.Background(), hostup.Worker{Setup: hostup.Setup{Worktree: "."}})
			if err != nil || calls != tc.calls || got.AgentTerminals != tc.wantKnown || got.UnidentifiedTerminals != tc.wantUnknown {
				t.Fatalf("observation=%+v calls=%d err=%v; want possible=%d unknown=%d calls=%d", got, calls, err, tc.wantKnown, tc.wantUnknown, tc.calls)
			}
		})
	}
}

func TestHostUpCommandUsesRealCensusAdapter2208(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "synthetic-manager")
	work := filepath.Join(root, "worker")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	readyCalls, probeCalls := 0, 0
	censusDependencies2208(t, func(*hostpoller.Config) func(context.Context) error {
		return func(context.Context) error { readyCalls++; return nil }
	}, func(_ context.Context, got string) (hostrelaunch.Observation, error) {
		probeCalls++
		if got != work {
			t.Errorf("wrong worktree %q", got)
		}
		return hostrelaunch.Observation{Proven: true, PIDs: []int{42}}, nil
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(api.SessionHeader) != "" {
			t.Error("host read carried worker attribution")
		}
		var req struct {
			Operation string `json:"operationName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if req.Operation != "HostRegistrations" {
			t.Errorf("unexpected server call %s", req.Operation)
			fmt.Fprint(w, `{"errors":[{"message":"unexpected fixture operation"}]}`)
			return
		}
		fmt.Fprint(w, `{"data":{"hostRegistrations":[{"hostId":"fixture","live":true,"workers":[{"workerId":"w"}]}]}}`)
	}))
	defer srv.Close()
	terminalJSON, err := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminals": []map[string]any{{"handle": "agent", "worktreePath": work, "agentIdentity": "claude"}, {"handle": "shell", "worktreePath": work}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HADRON_TEST_TERMINALS_2208", string(terminalJSON))
	orca := filepath.Join(root, "orca")
	script := "#!/bin/sh\ncase \"$1 $2\" in\n'status --json') printf '%s' '{\"ok\":true,\"result\":{\"runtime\":{\"reachable\":true}}}';;\n'terminal list') printf '%s' \"$HADRON_TEST_TERMINALS_2208\";;\n*) exit 19;;\nesac\n"
	if err := os.WriteFile(orca, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	desired := filepath.Join(root, "desired.json")
	source := `{"hostId":"fixture","sweepMode":"APPLY","workers":[{"workerId":"w","name":"Worker","desired":"RUNNING","reason":"LONG_LIVED","sessionId":"open","setup":{"worktree":"worker","family":"claude"}}]}`
	if err := os.WriteFile(desired, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	streams, out, _ := output.Test()
	factory := &cmdutil.Factory{JSON: true, IOStreams: streams, HTTPClient: srv.Client(), ServerFlag: srv.URL, AppFlag: "hrn:app:example.com:team", ConfigFn: func() (*config.Config, error) { return &config.Config{}, nil }}
	command := newCmdTeamHostUp(factory)
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetArgs([]string{"--host", "fixture", "--worktrees-root", root, "--desired-state-file", desired, "--orca-bin", orca})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var got hostUpDTO
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if readyCalls != 2 || probeCalls != 1 {
		t.Fatalf("command bypassed real census adapter: ready=%d probe=%d", readyCalls, probeCalls)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].Reason != hostup.ReasonInSync || len(got.Planned) != 0 || len(got.Failed) != 0 {
		t.Fatalf("proven shell retained by command: %+v", got)
	}
}
