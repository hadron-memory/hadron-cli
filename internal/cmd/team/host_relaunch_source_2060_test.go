package team

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

func TestRelaunchSnapshotRequiresCanonicalIdentityAndHeaderlessRead2060(t *testing.T) {
	for _, test := range []struct {
		name, body string
		pass       bool
	}{
		{"valid", `{"myApp":{"id":"app-id"},"hostDesiredState":{"hostId":"h","sweepMode":"APPLY","workers":[{"workerId":"w","name":"W","family":"claude","desired":"RUNNING","reason":"LONG_LIVED","openSessionId":"s","worktrees":["repo/w"]}]}}`, true},
		{"missing app", `{"myApp":null,"hostDesiredState":{"hostId":"h","sweepMode":"APPLY","workers":[]}}`, false},
		{"missing desired", `{"myApp":{"id":"app-id"},"hostDesiredState":null}`, false},
		{"wrong host", `{"myApp":{"id":"app-id"},"hostDesiredState":{"hostId":"other","sweepMode":"APPLY","workers":[]}}`, false},
		{"duplicate worker", `{"myApp":{"id":"app-id"},"hostDesiredState":{"hostId":"h","sweepMode":"APPLY","workers":[{"workerId":"w"},{"workerId":"w"}]}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get(api.SessionHeader) != "" {
					t.Error("worker attribution leaked to host read")
				}
				var body struct {
					OperationName string
					Variables     map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.OperationName != "HostRelaunchSnapshot" || body.Variables["appRef"] != "app-ref" || body.Variables["hostId"] != "h" {
					t.Errorf("unexpected request %+v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":` + test.body + `}`))
			}))
			defer srv.Close()
			client, err := api.NewClient(srv.URL, "private-test-key", srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			source := &hostServer{client: client, app: "app-ref", serverURL: srv.URL}
			got, err := source.RelaunchSnapshot(api.WithSession(context.Background(), "caller-session"), "h")
			if (err == nil) != test.pass {
				t.Fatalf("snapshot=%+v error=%v", got, err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			if test.pass && (got.AppID != "app-id" || got.HostID != "h" || got.Server != srv.URL || len(got.Workers) != 1 || got.Workers[0].SessionID != "s") {
				t.Fatalf("%+v", got)
			}
		})
	}
}
