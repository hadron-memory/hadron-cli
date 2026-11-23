package team

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

func TestRelaunchRepairWireUsesOnlyHonestClosedOutcomes2060(t *testing.T) {
	cases := []struct {
		name, status, reason, failure, repair, repairReason string
		attempt                                             int
		reject                                              bool
	}{
		{name: "profile", status: hostrelaunch.Skipped, reason: hostrelaunch.NoProfile, repair: "RELAUNCH_REFUSED", repairReason: "NO_LAUNCHER_PROFILE", attempt: 1},
		{name: "identity", status: hostrelaunch.Skipped, reason: hostrelaunch.IdentityChanged, repair: "RELAUNCH_REFUSED", repairReason: "IDENTITY_MISMATCH", attempt: 1},
		{name: "duplicate", status: hostrelaunch.Skipped, reason: hostrelaunch.DuplicateAgents, repair: "RELAUNCH_REFUSED", repairReason: "DUPLICATE_AGENT", attempt: 1},
		{name: "pause", status: hostrelaunch.Skipped, reason: "PAUSED", repair: "RELAUNCH_REFUSED", repairReason: "PAUSED", attempt: 1},
		{name: "halt", status: hostrelaunch.Skipped, reason: "HALTED", repair: "RELAUNCH_REFUSED", repairReason: "HALTED", attempt: 1},
		{name: "leave", status: hostrelaunch.Skipped, reason: "LEAVE_ALONE", repair: "RELAUNCH_REFUSED", repairReason: "LEAVE_ALONE", attempt: 1},
		{name: "timeout", status: hostrelaunch.Failed, reason: hostrelaunch.LaunchFailed, failure: "TIMEOUT", repair: "RELAUNCH_FAILED", repairReason: "TIMEOUT", attempt: 1},
		{name: "create", status: hostrelaunch.Failed, reason: hostrelaunch.LaunchFailed, failure: "TERMINAL_CREATE_REFUSED", repair: "RELAUNCH_FAILED", repairReason: "TERMINAL_CREATE_REFUSED", attempt: 1},
		{name: "exit", status: hostrelaunch.Failed, reason: hostrelaunch.LaunchFailed, failure: "LAUNCH_EXIT_NONZERO", repair: "RELAUNCH_FAILED", repairReason: "LAUNCH_EXIT_NONZERO", attempt: 1},
		{name: "cap", status: hostrelaunch.Skipped, reason: hostrelaunch.RateLimited, repair: "CAP_REACHED"},
		{name: "success", status: hostrelaunch.Applied, reason: "TERMINAL_MISSING", repair: "RELAUNCHED", attempt: 1},
		{name: "unproven", status: hostrelaunch.Skipped, reason: hostrelaunch.Unproven},
		{name: "unclassified failure", status: hostrelaunch.Failed, reason: hostrelaunch.VerifyFailed, reject: true},
		{name: "server refusal", status: hostrelaunch.Skipped, reason: hostrelaunch.NoProfile, repair: "RELAUNCH_REFUSED", repairReason: "NO_LAUNCHER_PROFILE", attempt: 1, reject: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mutations, queries := 0, 0
			stamp := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(api.SessionHeader) != "" {
					t.Error("repair used caller worker session")
				}
				var request struct {
					OperationName string
					Variables     map[string]json.RawMessage
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				if request.OperationName == "HostRelaunchWorker" {
					queries++
					_, _ = w.Write([]byte(`{"data":{"myApp":{"id":"app-id"},"hostDesiredState":{"hostId":"h1","sweepMode":"APPLY","workers":[{"workerId":"w","family":"claude","reason":"LONG_LIVED","desired":"RUNNING","openSessionId":"still-open","worktrees":["repo/w"]}]}}}`))
					return
				}
				if request.OperationName != "HealthSweepSetTerminals" {
					t.Errorf("unexpected operation %s", request.OperationName)
					return
				}
				mutations++
				var input struct {
					AppRef  string
					Reports []map[string]any
				}
				if err := json.Unmarshal(request.Variables["input"], &input); err != nil {
					t.Error(err)
				}
				if input.AppRef != "app-id" || len(input.Reports) != 1 {
					t.Errorf("input=%+v", input)
					return
				}
				report := input.Reports[0]
				for k, want := range map[string]any{"worker": "w", "status": "dark", "prompt": "unknown", "reason": "no_agent", "repair": test.repair, "checkedAt": stamp.Format(time.RFC3339Nano)} {
					if report[k] != want {
						t.Errorf("%s=%v want%v", k, report[k], want)
					}
				}
				if test.repairReason != "" {
					if report["repairReason"] != test.repairReason {
						t.Error(report)
					}
				} else if _, exists := report["repairReason"]; exists {
					t.Error("unrelated repairReason", report)
				}
				if test.attempt > 0 {
					if report["attempt"] != float64(test.attempt) {
						t.Error(report)
					}
				} else if _, exists := report["attempt"]; exists {
					t.Error("false attempt on cap", report)
				}
				if test.reject {
					_, _ = w.Write([]byte(`{"data":{"setHostWorkerTerminals":{"stored":[],"refused":[{"worker":"w","code":"STALE_CHECK"}]}}}`))
				} else {
					_, _ = w.Write([]byte(`{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w","urn":"private"}],"refused":[]}}}`))
				}
			}))
			defer srv.Close()
			client, err := api.NewClient(srv.URL, "private-host-key", srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			source := &hostServer{client: client, app: "app-ref", serverURL: srv.URL}
			result := hostrelaunch.Result{Status: test.status, Reason: test.reason, FailureReason: test.failure, Attempt: test.attempt, Reference: "own-terminal", AbsentObservedAt: stamp}
			err = source.ReportRelaunch(api.WithSession(context.Background(), "caller"), hostrelaunch.Target{AppID: "app-id", HostID: "h1", WorkerID: "w", SessionID: "still-open", Server: srv.URL, Worktree: "/private/worktree"}, result)
			if (err != nil) != test.reject {
				t.Fatalf("error=%v reject=%v", err, test.reject)
			}
			want := 0
			if test.repair != "" {
				want = 1
			}
			if mutations != want || queries != want {
				t.Fatalf("mutations=%d queries=%d want%d", mutations, queries, want)
			}
		})
	}
}
