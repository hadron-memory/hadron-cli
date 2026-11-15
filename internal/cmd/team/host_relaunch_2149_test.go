package team

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// The old provider rejects the new enum before executing the mutation. It
// must leave the failure local, never retry with a borrowed reason or success.
func TestRelaunchNewReasonsOlderServerFallback2149(t *testing.T) {
	for _, reason := range []string{"SETUP_FAILED", "VERIFY_FAILED"} {
		t.Run(reason, func(t *testing.T) {
			reads, writes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(api.SessionHeader) != "" {
					t.Error("report inherited a session")
				}
				var req struct {
					OperationName string
					Variables     map[string]json.RawMessage
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if req.OperationName == "HostRelaunchSnapshot" {
					reads++
					_, _ = w.Write([]byte(`{"data":{"myApp":{"id":"app"},"hostDesiredState":{"hostId":"host","sweepMode":"APPLY","workers":[{"workerId":"worker","family":"claude","reason":"LONG_LIVED","desired":"RUNNING","openSessionId":"session","worktrees":["repo/worker"]}]}}}`))
					return
				}
				if req.OperationName != "HealthSweepSetTerminals" {
					t.Errorf("unexpected operation %s", req.OperationName)
					return
				}
				writes++
				var input struct {
					Reports []struct{ Repair, RepairReason string }
				}
				if err := json.Unmarshal(req.Variables["input"], &input); err != nil {
					t.Error(err)
					return
				}
				if len(input.Reports) != 1 || input.Reports[0].Repair != "RELAUNCH_FAILED" || input.Reports[0].RepairReason != reason {
					t.Errorf("wrong failure report %+v", input)
				}
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": fmt.Sprintf("Value %q does not exist in WorkerTerminalRepairReason enum.", reason), "extensions": map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}}})
			}))
			defer server.Close()
			client, err := api.NewClient(server.URL, "synthetic-host-key", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			source := &hostServer{client: client, app: "app", serverURL: server.URL}
			result := hostrelaunch.Result{Status: hostrelaunch.Failed, Reason: hostrelaunch.LaunchFailed, FailureReason: reason, Attempt: 1, AbsentObservedAt: time.Now().UTC(), LocalCause: "original local failure"}
			err = source.ReportRelaunch(context.Background(), hostrelaunch.Target{AppID: "app", HostID: "host", WorkerID: "worker", SessionID: "session", Server: server.URL, Worktree: "/private/tree"}, result)
			if err == nil || exitcode.FromError(err) != exitcode.Usage || !strings.Contains(err.Error(), "out of sync") {
				t.Fatalf("older provider did not take schema-skew fallback: %v", err)
			}
			if reads != 1 || writes != 1 {
				t.Fatalf("reads=%d writes=%d; want one guarded attempt and no retry", reads, writes)
			}
			if result.Status != hostrelaunch.Failed || result.LocalCause != "original local failure" {
				t.Fatal("local failure changed")
			}
		})
	}
}
