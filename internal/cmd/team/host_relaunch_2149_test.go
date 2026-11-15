package team

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
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
	snapshot, err := os.ReadFile("../../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	oldSchema, err := gqlparser.LoadSchema(&ast.Source{Name: "old-provider", Input: string(snapshot)})
	if err != nil {
		t.Fatal(err)
	}
	values := oldSchema.Types["WorkerTerminalRepairReason"].EnumValues
	kept := values[:0]
	for _, value := range values {
		if value.Name != "SETUP_FAILED" && value.Name != "VERIFY_FAILED" {
			kept = append(kept, value)
		}
	}
	oldSchema.Types["WorkerTerminalRepairReason"].EnumValues = kept
	for _, reason := range []string{"SETUP_FAILED", "VERIFY_FAILED"} {
		t.Run(reason, func(t *testing.T) {
			reads, writes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(api.SessionHeader) != "" {
					t.Error("report inherited a session")
				}
				var req struct {
					OperationName string
					Query         string
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
				wantOp := "HostRelaunchSetupFailure"
				if reason == "VERIFY_FAILED" {
					wantOp = "HostRelaunchVerifyFailure"
				}
				if req.OperationName != wantOp {
					t.Errorf("operation=%s want%s", req.OperationName, wantOp)
					return
				}
				writes++
				if !strings.Contains(strings.Join(strings.Fields(req.Query), ""), "repairReason:"+reason) {
					t.Errorf("new enum must be static for schema-skew detection: %s", req.Query)
				}
				_, validationErrors := gqlparser.LoadQuery(oldSchema, req.Query)
				if len(validationErrors) == 0 || !strings.Contains(validationErrors.Error(), reason) {
					t.Errorf("old SDL did not reject static new enum: %v", validationErrors)
				}
				var worker string
				if err := json.Unmarshal(req.Variables["worker"], &worker); err != nil || worker != "worker" {
					t.Errorf("worker=%q error=%v", worker, err)
				}
				if string(req.Variables["attempt"]) != "1" {
					t.Error("candidate attempt was lost")
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
		})
	}
}

func TestNewRelaunchReasonReportsAndErrors2149(t *testing.T) {
	for _, reason := range []string{"SETUP_FAILED", "VERIFY_FAILED"} {
		for _, mode := range []string{"stored", "refused", "forbidden", "transport"} {
			t.Run(reason+"/"+mode, func(t *testing.T) {
				reads, writes := 0, 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						OperationName string
						Query         string
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
					writes++
					if !strings.Contains(strings.Join(strings.Fields(req.Query), ""), "repairReason:"+reason) || strings.Contains(req.Query, "CAP_REACHED") {
						t.Error("incorrect new-code document")
					}
					switch mode {
					case "stored":
						_, _ = w.Write([]byte(`{"data":{"setHostWorkerTerminals":{"stored":[{"id":"worker","urn":"private"}],"refused":[]}}}`))
					case "refused":
						_, _ = w.Write([]byte(`{"data":{"setHostWorkerTerminals":{"stored":[],"refused":[{"worker":"worker","code":"STALE_CHECK"}]}}}`))
					case "forbidden":
						_, _ = w.Write([]byte(`{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`))
					case "transport":
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`unavailable`))
					}
				}))
				defer srv.Close()
				c, err := api.NewClient(srv.URL, "synthetic", srv.Client())
				if err != nil {
					t.Fatal(err)
				}
				s := hostServer{client: c, app: "app", serverURL: srv.URL}
				err = s.ReportRelaunch(context.Background(), hostrelaunch.Target{AppID: "app", HostID: "host", WorkerID: "worker", SessionID: "session", Server: srv.URL, Worktree: "/private"}, hostrelaunch.Result{Status: hostrelaunch.Failed, Reason: hostrelaunch.LaunchFailed, FailureReason: reason, Attempt: 1, AbsentObservedAt: time.Now()})
				if (err == nil) != (mode == "stored") {
					t.Fatalf("%s result=%v", mode, err)
				}
				if mode == "forbidden" && exitcode.FromError(err) != exitcode.Forbidden {
					t.Fatal(err)
				}
				if err != nil && strings.Contains(err.Error(), "out of sync") {
					t.Fatalf("non-schema error misclassified: %v", err)
				}
				if reads != 1 || writes != 1 {
					t.Fatalf("reads=%d writes=%d", reads, writes)
				}
			})
		}
	}
}
