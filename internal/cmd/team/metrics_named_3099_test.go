package team

// cli#3099 item 2: TeamMetricsCount.named (server#4992) — one entry per id of
// ids, same order, the id and its name (a task title or a worker name), null
// when no name is readable. `team metrics` selects it, shows the names beside
// the ids under --ids, and --json gains `named` additively beside `ids`.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const metricsNamedFixture3099 = `{"data":{"teamMetrics":{
	"appId":"01a1app0000000000000000000aaaaa","asOf":"2026-11-25T01:00:00Z",
	"window":{"since":"2026-11-18T01:00:00Z","until":"2026-11-25T01:00:00Z"},
	"startedOwned":null,"projects":null,
	"workers":{
		"active":{"total":2,"ids":["01a1w1","01a1w2"],"truncated":false,
			"named":[{"id":"01a1w1","name":"Jimmy"},{"id":"01a1w2","name":null}]},
		"idle":{"total":0,"ids":[],"truncated":false,"named":[]},
		"overdue":{"total":0,"ids":[],"truncated":false,"named":[]},
		"inop":{"total":0,"ids":[],"truncated":false,"named":[]}},
	"flow":{
		"completed":{"total":0,"ids":[],"truncated":false,"named":[]},
		"started":{"total":1,"ids":["01a1t9"],"truncated":false,
			"named":[{"id":"01a1t9","name":"Build the thing"}]}}
}}}`

// runMetrics3099 runs the real metrics command against a one-operation fake
// server that answers TeamMetrics with counts carrying `named`.
func runMetrics3099(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "hdr_user_test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName != "TeamMetrics" {
			t.Errorf("unexpected operation %q", body.OperationName)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(metricsNamedFixture3099))
	}))
	t.Cleanup(srv.Close)
	io, out, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: io, ServerFlag: srv.URL, HTTPClient: srv.Client()}
	root := &cobra.Command{Use: "team", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVar(&f.JSON, "json", false, "")
	root.PersistentFlags().StringVar(&f.AppFlag, "app", "", "")
	root.AddCommand(newCmdTeamMetrics(f))
	root.SetArgs(append([]string{"metrics", "--app", "hrn:app:example.com:team"}, args...))
	root.SetOut(&bytes.Buffer{})
	err := root.Execute()
	return out.String(), err
}

// TestTeamMetricsShowsNamesBesideIds_3099 pins the text rendering: an id with a
// readable name shows the name beside it, an id without one stays bare.
func TestTeamMetricsShowsNamesBesideIds_3099(t *testing.T) {
	out, err := runMetrics3099(t, "--ids")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "01a1w1 (Jimmy)") {
		t.Errorf("an id with a readable name must show the name beside it:\n%s", out)
	}
	if strings.Contains(out, "01a1w2 (") {
		t.Errorf("an id with no readable name must stay bare:\n%s", out)
	}
	if !strings.Contains(out, "01a1t9 (Build the thing)") {
		t.Errorf("a task id must show its title beside it:\n%s", out)
	}
}

// TestTeamMetricsJSONNamesAdditive_3099 pins the --json contract: `named`
// appears beside `ids`, one entry per id with its name, null when unreadable,
// and every existing key keeps its shape.
func TestTeamMetricsJSONNamesAdditive_3099(t *testing.T) {
	out, err := runMetrics3099(t, "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var doc struct {
		Workers struct {
			Active struct {
				Total int      `json:"total"`
				IDs   []string `json:"ids"`
				Named []struct {
					ID   string  `json:"id"`
					Name *string `json:"name"`
				} `json:"named"`
			} `json:"active"`
		} `json:"workers"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode --json: %v\n%s", err, out)
	}
	act := doc.Workers.Active
	if len(act.IDs) != 2 || act.Total != 2 {
		t.Fatalf("ids/total must be unchanged by the additive field, got ids=%v total=%d", act.IDs, act.Total)
	}
	if len(act.Named) != 2 || act.Named[0].ID != "01a1w1" || act.Named[0].Name == nil || *act.Named[0].Name != "Jimmy" {
		t.Fatalf("--json must carry named, one entry per id with its name, got %+v", act.Named)
	}
	if act.Named[1].Name != nil {
		t.Fatalf("an unreadable name must be null, got %v", *act.Named[1].Name)
	}
}
