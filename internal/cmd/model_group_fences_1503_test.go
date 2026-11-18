package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1503: pins for the #1500 (model groups S1) review fences that
// held in source but had no test, so their mutants survived. Test-only.

type mgCall1503 struct {
	op, session string
	vars        json.RawMessage
}

// mgHeaderServer1503 answers from a fixed map and records every GraphQL call
// with its X-Hadron-Session header, so a test can pin that no session goes on
// the wire.
func mgHeaderServer1503(t *testing.T, responses map[string]string) (string, func() []mgCall1503) {
	t.Helper()
	var mu sync.Mutex
	var calls []mgCall1503
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, mgCall1503{op: body.OperationName, session: r.Header.Get(api.SessionHeader), vars: body.Variables})
		mu.Unlock()
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []mgCall1503 {
		mu.Lock()
		defer mu.Unlock()
		return append([]mgCall1503(nil), calls...)
	}
}

const mgApp1503 = "hrn:app:acme.com:eng"

// Item 1: none of the six model-group operations sends a worker session
// (§10 P3-1): an X-Hadron-Session added to any of them fails here. The
// worktree is BOUND to the fake server, so a session that rides in only when a
// binding exists (AuthoredWriteContext, root.go, attaching it to any mutation
// on its authored-write list) would show too, not just a hard-coded one.
func TestModelGroup1503NoSessionHeader(t *testing.T) {
	for _, tc := range []struct {
		op, resp string
		args     []string
	}{
		{"AgentModelGroups", `{"data":{"agentModelGroups":[` + mgGroupJSON + `]}}`, []string{"agent", "model-group", "list", "agt1"}},
		{"SetAgentModelGroup", `{"data":{"setAgentModelGroup":` + mgGroupJSON + `}}`, []string{"agent", "model-group", "set", "agt1", "frontier", "--pattern", "x"}},
		{"DeleteAgentModelGroup", `{"data":{"deleteAgentModelGroup":true}}`, []string{"agent", "model-group", "delete", "agt1", "frontier", "--yes"}},
		{"EffectiveModelGroups", `{"data":{"effectiveModelGroups":[]}}`, []string{"app", "model-group", "list", mgApp1503}},
		{"SetAppModelGroup", `{"data":{"setAppModelGroup":` + mgGroupJSON + `}}`, []string{"app", "model-group", "set", mgApp1503, "frontier", "--pattern", "x"}},
		{"DeleteAppModelGroup", `{"data":{"deleteAppModelGroup":true}}`, []string{"app", "model-group", "clear", mgApp1503, "frontier", "--yes"}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			url, calls := mgHeaderServer1503(t, map[string]string{tc.op: tc.resp})
			writeTeamBinding(t)
			setTeamBindingServer(t, url)
			if _, err := mgRun(t, url, append(tc.args, "--json")...); err != nil {
				t.Fatal(err)
			}
			got := calls()
			if len(got) != 1 || got[0].op != tc.op {
				t.Fatalf("want exactly one %s, got %v", tc.op, got)
			}
			if got[0].session != "" {
				t.Fatalf("%s sent %s %q; model-group calls carry no worker session", tc.op, api.SessionHeader, got[0].session)
			}
		})
	}
}

// Items 2 and 3: every documented refusal maps to its exit code, the server
// code survives on the error (the --json envelope's serverCode), the server
// message is kept, and the refused write is sent once. Covers the agent delete
// refusal and the codes #1500 documented without a row.
func TestModelGroup1503RefusalRows(t *testing.T) {
	agentSet := []string{"agent", "model-group", "set", "agt1", "g", "--pattern", "x"}
	agentDelete := []string{"agent", "model-group", "delete", "agt1", "g", "--yes"}
	appSet := []string{"app", "model-group", "set", mgApp1503, "g", "--pattern", "x"}
	appClear := []string{"app", "model-group", "clear", mgApp1503, "g", "--yes"}
	appList := []string{"app", "model-group", "list", mgApp1503}
	for _, tc := range []struct {
		code string
		want int
		op   string
		args []string
	}{
		{"MODEL_GROUP_NOT_FOUND", exitcode.NotFound, "DeleteAgentModelGroup", agentDelete},
		{"FORBIDDEN", exitcode.Forbidden, "DeleteAgentModelGroup", agentDelete},
		{"AGENT_OWNERSHIP_CHANGED", exitcode.Error, "DeleteAgentModelGroup", agentDelete},
		{"AGENT_OWNERSHIP_CHANGED", exitcode.Error, "SetAgentModelGroup", agentSet},
		{"MODEL_GROUP_LIMIT_REACHED", exitcode.Error, "SetAgentModelGroup", agentSet},
		{"MODEL_GROUP_LIMIT_REACHED", exitcode.Error, "SetAppModelGroup", appSet},
		{"SESSION_NOT_BOUND", exitcode.Usage, "SetAppModelGroup", appSet},
		{"APP_UNINSTALLED", exitcode.Conflict, "SetAppModelGroup", appSet},
		{"SESSION_NOT_BOUND", exitcode.Usage, "DeleteAppModelGroup", appClear},
		{"APP_UNINSTALLED", exitcode.Conflict, "DeleteAppModelGroup", appClear},
		{"SESSION_NOT_BOUND", exitcode.Usage, "EffectiveModelGroups", appList},
		{"TEAM_AGENT_AMBIGUOUS", exitcode.Usage, "EffectiveModelGroups", appList},
	} {
		t.Run(tc.op+"/"+tc.code, func(t *testing.T) {
			url, calls := mgHeaderServer1503(t, map[string]string{
				tc.op: `{"errors":[{"message":"server says ` + tc.code + `","extensions":{"code":"` + tc.code + `"}}],"data":null}`,
			})
			out, err := mgRun(t, url, append(tc.args, "--json")...)
			if got := exitCodeFor(err); got != tc.want {
				t.Fatalf("exit = %d, want %d (%v)", got, tc.want, err)
			}
			var coded interface{ ServerCode() string }
			if !errors.As(err, &coded) || coded.ServerCode() != tc.code {
				t.Fatalf("the --json error envelope's serverCode must be %s: %v", tc.code, err)
			}
			if !strings.Contains(err.Error(), "server says "+tc.code) {
				t.Fatalf("server message not kept: %q", err.Error())
			}
			if out != "" {
				t.Fatalf("a refusal prints no document, got %q", out)
			}
			if got := calls(); len(got) != 1 || got[0].op != tc.op {
				t.Fatalf("one call, no retry: %v", got)
			}
		})
	}
}

// Item 2: the App side sends the group name raw, like the Agent side; a
// local lower-casing or trim fails here.
func TestAppModelGroup1503NameSentRaw(t *testing.T) {
	for _, tc := range []struct {
		op, resp string
		args     []string
	}{
		{"SetAppModelGroup", `{"data":{"setAppModelGroup":` + mgGroupJSON + `}}`, []string{"app", "model-group", "set", mgApp1503, " Frontier-MiXed ", "--pattern", "x"}},
		{"DeleteAppModelGroup", `{"data":{"deleteAppModelGroup":true}}`, []string{"app", "model-group", "clear", mgApp1503, " Frontier-MiXed ", "--yes"}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			url, calls := mgHeaderServer1503(t, map[string]string{tc.op: tc.resp})
			if _, err := mgRun(t, url, append(tc.args, "--json")...); err != nil {
				t.Fatal(err)
			}
			got := calls()
			if len(got) != 1 {
				t.Fatalf("calls = %v", got)
			}
			if name := string(mgVars(t, got[0].vars)["name"]); name != `" Frontier-MiXed "` {
				t.Fatalf("name sent %s, want it exactly as given", name)
			}
		})
	}
}

// Item 4: the agent-side human output, golden.
func TestAgentModelGroup1503HumanOutput(t *testing.T) {
	list := `{"data":{"agentModelGroups":[` + mgGroupJSON + `,{"name":"parked","patterns":[],"updatedAt":"2026-11-02T00:00:00Z"}]}}`
	for _, tc := range []struct {
		name, op, resp string
		args           []string
		want           string
	}{
		{"list", "AgentModelGroups", list, []string{"agent", "model-group", "list", "agt1"},
			"NAME      PATTERNS                        UPDATED\n" +
				"frontier  claude-opus-*, claude-sonnet-*  2026-11-01T00:00:00Z\n" +
				"parked    (none)                          2026-11-02T00:00:00Z\n"},
		{"list empty", "AgentModelGroups", `{"data":{"agentModelGroups":[]}}`, []string{"agent", "model-group", "list", "agt1"},
			"This Agent declares no model groups.\n"},
		{"set", "SetAgentModelGroup", `{"data":{"setAgentModelGroup":` + mgGroupJSON + `}}`, []string{"agent", "model-group", "set", "agt1", "frontier", "--pattern", "claude-opus-*", "--pattern", "claude-sonnet-*"},
			"✓ set model group frontier on agent agt1\n  patterns: claude-opus-*, claude-sonnet-*\n"},
		{"set empty", "SetAgentModelGroup", `{"data":{"setAgentModelGroup":{"name":"parked","patterns":[],"updatedAt":"2026-11-01T00:00:00Z"}}}`, []string{"agent", "model-group", "set", "agt1", "parked"},
			"✓ set model group parked on agent agt1\n  patterns: (none)\n"},
		{"delete", "DeleteAgentModelGroup", `{"data":{"deleteAgentModelGroup":true}}`, []string{"agent", "model-group", "delete", "agt1", "parked", "--yes"},
			"✓ deleted model group parked from agent agt1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, _ := mgHeaderServer1503(t, map[string]string{tc.op: tc.resp})
			out, err := mgRun(t, url, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if out != tc.want {
				t.Fatalf("human output:\n got %q\nwant %q", out, tc.want)
			}
		})
	}
}
