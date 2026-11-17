package team

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

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
	var kept ast.EnumValueList
	for _, value := range values {
		if value.Name != "SETUP_FAILED" && value.Name != "VERIFY_FAILED" {
			kept = append(kept, value)
		}
	}
	// The older provider really lacks both values: the removal removed exactly
	// those two, so a validation failure below is about them and nothing else.
	if len(kept) != len(values)-2 {
		t.Fatalf("the old-SDL step removed %d of %d enum values, want exactly the two new ones", len(values)-len(kept), len(values))
	}
	for _, value := range kept {
		if value.Name == "SETUP_FAILED" || value.Name == "VERIFY_FAILED" {
			t.Fatalf("%s survived the old-SDL step", value.Name)
		}
	}
	oldSchema.Types["WorkerTerminalRepairReason"].EnumValues = kept
	for _, reason := range []string{"SETUP_FAILED", "VERIFY_FAILED"} {
		t.Run(reason, func(t *testing.T) {
			var reads, writes atomic.Int32
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
					reads.Add(1)
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
				writes.Add(1)
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
			if reads.Load() != 1 || writes.Load() != 1 {
				t.Fatalf("reads=%d writes=%d; want one guarded attempt and no retry", reads.Load(), writes.Load())
			}
		})
	}
}

func TestNewRelaunchReasonReportsAndErrors2149(t *testing.T) {
	// Each unconfirmed answer keeps its own cause in the local error: the
	// refused codes (the server's closed set, never free text from the host),
	// and the stored count or the worker mismatch (cli#2172).
	modes := []struct {
		name, body string
		want       []string // substrings of the local error; nil = confirmed stored
	}{
		{"stored", `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"worker","urn":"private"}],"refused":[]}}}`, nil},
		{"refused", `{"data":{"setHostWorkerTerminals":{"stored":[],"refused":[{"worker":"worker","code":"TERMINAL_REPORT_STALE"}]}}}`, []string{"not confirmed stored", "TERMINAL_REPORT_STALE", "stored 0"}},
		{"refused with a row stored", `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"worker","urn":"p"}],"refused":[{"worker":"worker","code":"REPAIR_REASON_INVALID"}]}}}`, []string{"REPAIR_REASON_INVALID", "stored 1"}},
		{"empty stored", `{"data":{"setHostWorkerTerminals":{"stored":[],"refused":[]}}}`, []string{"not confirmed stored", "stored 0", "want 1"}},
		{"multiple stored", `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"worker","urn":"p"},{"id":"other","urn":"q"}],"refused":[]}}}`, []string{"not confirmed stored", "stored 2", "want 1"}},
		{"returned worker mismatch", `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"another-worker","urn":"p"}],"refused":[]}}}`, []string{"not confirmed stored", "another worker"}},
		{"forbidden", `{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`, []string{"denied"}},
		{"transport", "", []string{"HTTP 503", "not a GraphQL error"}},
	}
	for _, reason := range []string{"SETUP_FAILED", "VERIFY_FAILED"} {
		for _, mode := range modes {
			t.Run(reason+"/"+mode.name, func(t *testing.T) {
				var reads, writes atomic.Int32
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
						reads.Add(1)
						_, _ = w.Write([]byte(`{"data":{"myApp":{"id":"app"},"hostDesiredState":{"hostId":"host","sweepMode":"APPLY","workers":[{"workerId":"worker","family":"claude","reason":"LONG_LIVED","desired":"RUNNING","openSessionId":"session","worktrees":["repo/worker"]}]}}}`))
						return
					}
					writes.Add(1)
					if !strings.Contains(strings.Join(strings.Fields(req.Query), ""), "repairReason:"+reason) || strings.Contains(req.Query, "CAP_REACHED") {
						t.Error("incorrect new-code document")
					}
					if mode.name == "transport" {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`unavailable`))
						return
					}
					_, _ = w.Write([]byte(mode.body))
				}))
				defer srv.Close()
				c, err := api.NewClient(srv.URL, "synthetic", srv.Client())
				if err != nil {
					t.Fatal(err)
				}
				s := hostServer{client: c, app: "app", serverURL: srv.URL}
				err = s.ReportRelaunch(context.Background(), hostrelaunch.Target{AppID: "app", HostID: "host", WorkerID: "worker", SessionID: "session", Server: srv.URL, Worktree: "/private"}, hostrelaunch.Result{Status: hostrelaunch.Failed, Reason: hostrelaunch.LaunchFailed, FailureReason: reason, Attempt: 1, AbsentObservedAt: time.Now()})
				if (err == nil) != (mode.want == nil) {
					t.Fatalf("%s result=%v", mode.name, err)
				}
				if mode.name == "forbidden" && exitcode.FromError(err) != exitcode.Forbidden {
					t.Fatal(err)
				}
				if err != nil {
					if strings.Contains(err.Error(), "out of sync") {
						t.Fatalf("non-schema error misclassified: %v", err)
					}
					for _, want := range mode.want {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("the local error lost %q: %v", want, err)
						}
					}
					// Local diagnostics only: neither ids nor the private
					// worktree path are put into the error.
					for _, banned := range []string{"/private", "another-worker", "private\"", "session"} {
						if mode.name != "forbidden" && strings.Contains(err.Error(), banned) {
							t.Errorf("the local error carries %q: %v", banned, err)
						}
					}
				}
				if reads.Load() != 1 || writes.Load() != 1 {
					t.Fatalf("reads=%d writes=%d", reads.Load(), writes.Load())
				}
			})
		}
	}
}
