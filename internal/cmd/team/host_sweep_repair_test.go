package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

func TestRecoverySweepUsesHostKeyAndRemovesSession2056(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer host-only" || r.Header.Get(api.SessionHeader) != "" {
			t.Errorf("wrong host report credential/header")
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
		if len(q.Vars.Input["lines"].([]any)) != 50 {
			t.Error("repair report exceeded line cap")
		}
		if q.Op != "RecordHostSweepRun" || q.Vars.Input["applied"] != float64(60) {
			t.Errorf("wrong report: %+v", q)
		}
		fmt.Fprint(w, `{"data":{"recordHostSweepRun":{"id":"r1","runId":"recovery:1"}}}`)
	}))
	defer srv.Close()
	client, err := api.NewClient(srv.URL, "host-only", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	s := &hostServer{client: client, app: "app"}
	now := time.Now()
	report := hostpoller.SweepReport{AppID: "app", HostID: "h1", RunID: "recovery:1", Mode: "APPLY", StartedAt: now, EndedAt: now, Applied: 60, Lines: []hostpoller.SweepReportLine{}}
	for i := 0; i < 60; i++ {
		report.Lines = append(report.Lines, hostpoller.SweepReportLine{Worker: fmt.Sprintf("w%d", i), Outcome: "APPLIED", Reason: "TERMINAL_MISSING"})
	}
	if err := s.RecordRecoverySweep(api.WithSession(context.Background(), "must-remove"), report); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("requests=%d", calls)
	}
}

func TestSweepReadinessSeparatesDownFromUnproven2056(t *testing.T) {
	for _, tc := range []struct {
		name, scopes      string
		scopeErr, listErr error
		down              bool
	}{
		{name: "managed daemon absent", down: true},
		{name: "scope read unavailable", scopeErr: fmt.Errorf("unavailable")},
		{name: "ambiguous scopes", scopes: "orca-daemon-a.scope loaded active running\norca-daemon-b.scope loaded active running\n"},
		{name: "list unreachable", scopes: "orca-daemon-a.scope loaded active running\n", listErr: fmt.Errorf("unreachable"), down: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := relaunchDaemonReady(context.Background(), func(context.Context) ([]byte, error) { return []byte(tc.scopes), tc.scopeErr }, func(context.Context) error { return tc.listErr })
			if errors.Is(err, hostpoller.ErrOrcaDown) != tc.down {
				t.Fatalf("wrong readiness class: %v", err)
			}
		})
	}
}

func TestSweepExecOnlyDoesNotRequireManagedScope2137(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	if err := sweepDaemonReady(context.Background(), &hostpoller.Config{}); err != nil {
		t.Fatalf("exec-only host requires C5 daemon scope: %v", err)
	}
	cfg := &hostpoller.Config{}
	cfg.Delivery.OrcaBin = "/delivery/orca"
	cfg.OrcaLaunch.OrcaBin = "/launch/orca"
	if got := configuredOrcaBin(cfg); got != "/launch/orca" {
		t.Fatalf("inconsistent Orca selection %q", got)
	}
}
