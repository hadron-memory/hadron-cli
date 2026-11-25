package team

// cli#3099 item 2: TeamMetricsCount.named (server#4992) — one entry per id of
// ids, same order, the id and its name (a task title or a worker name), null
// when no name is readable. `team metrics` selects it, shows the names beside
// the ids under --ids, and --json gains `named` additively beside `ids`.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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
	return runMetrics3099With(t, metricsNamedFixture3099, args...)
}

// runMetrics3099With is runMetrics3099 with the body the fake server answers
// with, so the floor tests can serve a stale-server rejection.
func runMetrics3099With(t *testing.T, response string, args ...string) (string, error) {
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
		_, _ = w.Write([]byte(response))
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
	// The []T{} rule, asserted on the raw text: an EMPTY named list must
	// render as [], never null — a typed decode cannot tell them apart, so a
	// nil-init mutant would stay green without this (the fixture's idle,
	// overdue, inop and flow.completed counts are all empty).
	if !strings.Contains(out, `"named": []`) {
		t.Errorf("an empty named list must render as [] (the []T{} rule):\n%s", out)
	}
	if strings.Contains(out, `"named": null`) {
		t.Errorf("named must never render as null:\n%s", out)
	}
}

// Jerry (cli#3099 BLIND P2): a server too old for the counts' named field —
// one from before 2026-11-24, server#4992 — must say so in the command's own
// words at exit 2 (Usage), the same house answer as the teamMetrics floor,
// not fall through as a raw GraphQL validation error. Floors do not coincide:
// a 2026-11-22..24 server serves teamMetrics but rejects `named`.
func TestTeamMetricsNamedFloorSaidNotRaw_3099(t *testing.T) {
	stale := `{"errors":[{"message":"Cannot query field \"named\" on type \"TeamMetricsCount\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	out, err := runMetrics3099With(t, stale, "--ids")
	if err == nil {
		t.Fatalf("a server without the counts' named field must be an error, got output:\n%s", out)
	}
	var coded *exitcode.CodedError
	if !errors.As(err, &coded) || coded.Code != exitcode.Usage {
		t.Fatalf("the named-field floor must be exit 2 (Usage), got %v", err)
	}
	msg := coded.Error()
	if !strings.Contains(msg, "server#4992") || !strings.Contains(msg, "2026-11-24") {
		t.Fatalf("the floor message must name server#4992 and 2026-11-24:\n%s", msg)
	}
	if strings.Contains(msg, "Cannot query field") {
		t.Fatalf("the raw GraphQL error must not leak:\n%s", msg)
	}
}
