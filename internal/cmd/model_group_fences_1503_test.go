package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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
//
// The handler runs on the server's goroutine, where t.Errorf is not safe (a
// request landing after the test finished would panic). It records a failure
// under the mutex instead, and a cleanup on the test goroutine reports them,
// after closing the server so no request is still in flight (cli#2421 F2).
func mgHeaderServer1503(t *testing.T, responses map[string]string) (string, func() []mgCall1503) {
	t.Helper()
	var mu sync.Mutex
	var calls []mgCall1503
	var failures []string
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
		// cli#2421 F3: a malformed body is reported as that, not as a
		// confusing 'unexpected operation ""'.
		decodeErr := json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, mgCall1503{op: body.OperationName, session: r.Header.Get(api.SessionHeader), vars: body.Variables})
		if decodeErr != nil {
			failures = append(failures, "malformed request body: "+decodeErr.Error())
		}
		mu.Unlock()
		resp, ok := responses[body.OperationName]
		if !ok {
			if decodeErr == nil {
				mu.Lock()
				failures = append(failures, "unexpected operation "+body.OperationName)
				mu.Unlock()
			}
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(func() {
		srv.Close() // waits for in-flight requests; Close is safe to repeat
		mu.Lock()
		defer mu.Unlock()
		for _, f := range failures {
			t.Error(f)
		}
	})
	return srv.URL, func() []mgCall1503 {
		mu.Lock()
		defer mu.Unlock()
		return append([]mgCall1503(nil), calls...)
	}
}

const mgApp1503 = "hrn:app:acme.com:eng"

// withJSON1503 returns args plus --json without writing into a slice the table
// rows share. Defensive (cli#2421 F4): today's rows are literals with no spare
// capacity, so an append could not alias, but a row built with append could.
func withJSON1503(args []string) []string {
	return append(append([]string(nil), args...), "--json")
}

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
			if _, err := mgRun(t, url, withJSON1503(tc.args)...); err != nil {
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
		// cli#2421 F5: FORBIDDEN on every other model-group operation too.
		{"FORBIDDEN", exitcode.Forbidden, "AgentModelGroups", []string{"agent", "model-group", "list", "agt1"}},
		{"FORBIDDEN", exitcode.Forbidden, "SetAgentModelGroup", agentSet},
		{"FORBIDDEN", exitcode.Forbidden, "EffectiveModelGroups", appList},
		{"FORBIDDEN", exitcode.Forbidden, "SetAppModelGroup", appSet},
		{"FORBIDDEN", exitcode.Forbidden, "DeleteAppModelGroup", appClear},
		{"AGENT_OWNERSHIP_CHANGED", exitcode.Conflict, "DeleteAgentModelGroup", agentDelete},
		{"AGENT_OWNERSHIP_CHANGED", exitcode.Conflict, "SetAgentModelGroup", agentSet},
		{"MODEL_GROUP_LIMIT_REACHED", exitcode.Conflict, "SetAgentModelGroup", agentSet},
		{"MODEL_GROUP_LIMIT_REACHED", exitcode.Conflict, "SetAppModelGroup", appSet},
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
			out, err := mgRun(t, url, withJSON1503(tc.args)...)
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
			if _, err := mgRun(t, url, withJSON1503(tc.args)...); err != nil {
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
				t.Fatalf("human output differs\n--- got ---\n%s--- want ---\n%s--- end ---", out, tc.want)
			}
		})
	}
}

// cli#2421 F5: the App-side human output, golden, beside the Agent side's.
func TestAppModelGroup1503HumanOutput(t *testing.T) {
	list := `{"data":{"effectiveModelGroups":[{"name":"a","patterns":["x-*","y"],"origin":"AGENT"},{"name":"b","patterns":[],"origin":"APP_OVERRIDE"},{"name":"c","patterns":["z"],"origin":"APP"}]}}`
	for _, tc := range []struct {
		name, op, resp string
		args           []string
		want           string
	}{
		{"list", "EffectiveModelGroups", list, []string{"app", "model-group", "list", mgApp1503},
			"NAME  ORIGIN        PATTERNS\n" +
				"a     agent         x-*, y\n" +
				"b     app-override  (none)\n" +
				"c     app-only      z\n"},
		{"list empty", "EffectiveModelGroups", `{"data":{"effectiveModelGroups":[]}}`, []string{"app", "model-group", "list", mgApp1503},
			"No model groups are in force in this App.\n"},
		{"set", "SetAppModelGroup", `{"data":{"setAppModelGroup":` + mgGroupJSON + `}}`, []string{"app", "model-group", "set", mgApp1503, "frontier", "--pattern", "claude-opus-*", "--pattern", "claude-sonnet-*"},
			"✓ set model group frontier in app " + mgApp1503 + "\n  patterns: claude-opus-*, claude-sonnet-*\n"},
		{"set empty", "SetAppModelGroup", `{"data":{"setAppModelGroup":{"name":"parked","patterns":[],"updatedAt":"2026-11-01T00:00:00Z"}}}`, []string{"app", "model-group", "set", mgApp1503, "parked"},
			"✓ set model group parked in app " + mgApp1503 + "\n  patterns: (none)\n"},
		{"clear", "DeleteAppModelGroup", `{"data":{"deleteAppModelGroup":true}}`, []string{"app", "model-group", "clear", mgApp1503, "parked", "--yes"},
			"✓ cleared model group parked in app " + mgApp1503 + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, _ := mgHeaderServer1503(t, map[string]string{tc.op: tc.resp})
			out, err := mgRun(t, url, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if out != tc.want {
				t.Fatalf("human output differs\n--- got ---\n%s--- want ---\n%s--- end ---", out, tc.want)
			}
		})
	}
}

// cli#2421 F5: a delete or clear the server answers with false is not shown as
// done. The server's contract is the source (no spec states it): in
// hadron-server's resolvers.modelGroup.ts (checked at origin/main, 2026-11-21)
// deleteAgentModelGroup and deleteAppModelGroup return true on success and have
// no false path: an absent group is the typed MODEL_GROUP_NOT_FOUND refusal, not
// an idempotent false (typeDefs.ts lists that refusal on both). So a false is
// outside the contract, and the CLI treats it as unconfirmed: exit 1 with the
// recovery advice, nothing printed, exactly one request, in the text and the
// --json mode. A server that gave false meaning "no-op" would need this
// reconsidered.
func TestModelGroup1503DeleteFalseIsNotConfirmed(t *testing.T) {
	for _, tc := range []struct {
		op, resp, advice string
		args             []string
	}{
		{"DeleteAgentModelGroup", `{"data":{"deleteAgentModelGroup":false}}`, "before retrying", []string{"agent", "model-group", "delete", "agt1", "parked", "--yes"}},
		{"DeleteAppModelGroup", `{"data":{"deleteAppModelGroup":false}}`, "before retrying", []string{"app", "model-group", "clear", mgApp1503, "parked", "--yes"}},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(tc.op+"/json="+map[bool]string{false: "no", true: "yes"}[asJSON], func(t *testing.T) {
				url, calls := mgHeaderServer1503(t, map[string]string{tc.op: tc.resp})
				args := tc.args
				if asJSON {
					args = withJSON1503(args)
				}
				out, err := mgRun(t, url, args...)
				if exitCodeFor(err) != exitcode.Error || err == nil || !strings.Contains(err.Error(), tc.advice) {
					t.Fatalf("want exit %d with %q, got %d: %v", exitcode.Error, tc.advice, exitCodeFor(err), err)
				}
				if out != "" {
					t.Fatalf("an unconfirmed delete prints nothing, got %q", out)
				}
				if got := calls(); len(got) != 1 || got[0].op != tc.op {
					t.Fatalf("one call, no retry: %v", got)
				}
			})
		}
	}
}

// mgStatusServer1503 answers every GraphQL request with the given status and
// body (the /capabilities probe aside) and counts them, so a transport row can
// pin "one request, no retry" without re-implementing the stub per test.
func mgStatusServer1503(t *testing.T, status int, contentType, body string) (string, func() int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		n.Add(1)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, n.Load
}

var mgAllOps1503 = map[string][]string{
	"agent list":   {"agent", "model-group", "list", "agt1"},
	"agent set":    {"agent", "model-group", "set", "agt1", "g", "--pattern", "x"},
	"agent delete": {"agent", "model-group", "delete", "agt1", "g", "--yes"},
	"app list":     {"app", "model-group", "list", mgApp1503},
	"app set":      {"app", "model-group", "set", mgApp1503, "g", "--pattern", "x"},
	"app clear":    {"app", "model-group", "clear", mgApp1503, "g", "--yes"},
}

// cli#2421 F5: a non-200 answer that is not a GraphQL envelope (a gateway page)
// is the transport failure, exit Unavailable, on reads and writes alike, sent
// once and never retried, with nothing printed.
func TestModelGroup1503TransportFailureRows(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		for name, args := range mgAllOps1503 {
			t.Run(fmt.Sprintf("%d/%s", status, name), func(t *testing.T) {
				url, requests := mgStatusServer1503(t, status, "text/html", "<html>gateway</html>")
				out, err := mgRun(t, url, args...)
				if exitCodeFor(err) != exitcode.Unavailable || out != "" {
					t.Fatalf("want exit %d and no output, got %d %q: %v", exitcode.Unavailable, exitCodeFor(err), out, err)
				}
				if n := requests(); n != 1 {
					t.Fatalf("one request, no retry: %d", n)
				}
			})
		}
	}
}

// cli#2421 F5 (blind P3): a 200 whose body is not a GraphQL envelope (a login or
// proxy page) is the other common gateway shape. Today it is a generic failure
// (exit Error: the decoder's message), one request, nothing printed, in text and
// --json. This pins that behaviour, not a promise about its wording.
func TestModelGroup1503Non200HTMLBodyRows(t *testing.T) {
	for name, args := range mgAllOps1503 {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", name, asJSON), func(t *testing.T) {
				url, requests := mgStatusServer1503(t, http.StatusOK, "text/html", "<html>login</html>")
				a := args
				if asJSON {
					a = withJSON1503(a)
				}
				out, err := mgRun(t, url, a...)
				if exitCodeFor(err) != exitcode.Error || out != "" {
					t.Fatalf("want exit %d and no output, got %d %q: %v", exitcode.Error, exitCodeFor(err), out, err)
				}
				if n := requests(); n != 1 {
					t.Fatalf("one request, no retry: %d", n)
				}
			})
		}
	}
}
