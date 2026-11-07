package team

import (
	"context"
	"encoding/json"
	"github.com/Khan/genqlient/graphql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestDescribeSession1721Lifecycle(t *testing.T) {
	user, tool, host := "user-id", "codex", "fixture-host"
	ended := "2026-11-07T09:00:00Z"
	for _, tc := range []struct {
		name         string
		ended        *string
		want, absent string
	}{
		{"live", nil, "since 2026-11-07T08:00:00Z", "ended "},
		{"ended", &ended, "ended " + ended, "since 2026-11-07T08:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := describeSession(&gen.TeamSessionFields{Id: "session-id", UserId: &user, Tool: &tool, Host: &host, StartedAt: "2026-11-07T08:00:00Z", EndedAt: tc.ended})
			for _, want := range []string{tc.want, "user-id", "via codex", "on fixture-host", "session session-id"} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in %q", want, got)
				}
			}
			if strings.Contains(got, tc.absent) {
				t.Errorf("unexpected %q in %q", tc.absent, got)
			}
		})
	}
}

func TestSessionDescriber1721MemoizesWithoutChangingSession(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Ref string `json:"ref"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.OperationName != "GetUser" {
			t.Errorf("unexpected %q", req.OperationName)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"user": map[string]any{"id": req.Variables.Ref, "urn": "hrn:user:" + req.Variables.Ref}}})
	}))
	defer srv.Close()
	describe := sessionDescriber(sessionUserLabeller(context.Background(), graphql.NewClient(srv.URL, srv.Client())))
	alpha, beta := "alpha", "beta"
	session := gen.TeamSessionFields{Id: "s", UserId: &alpha, StartedAt: "start"}
	before, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got := describe(&session); !strings.Contains(got, "user hrn:user:alpha") {
			t.Errorf("URN missing: %s", got)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("same user should resolve once, got %d", calls.Load())
	}
	after, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("narration mutated source/JSON: %s -> %s", before, after)
	}
	session.UserId = &beta
	if got := describe(&session); !strings.Contains(got, "user hrn:user:beta") {
		t.Errorf("distinct user's label missing: %s", got)
	}
	if calls.Load() != 2 {
		t.Errorf("distinct user should resolve separately, got %d", calls.Load())
	}
}
