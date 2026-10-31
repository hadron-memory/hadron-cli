package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const agent1336 = "0123456789abcdef0123456789abcdef"
const paused1336 = "2026-10-31T05:00:00Z"

func managerResponses1336() map[string]string {
	r := taskResponses()
	r["SetTeamPause"] = `{"data":{"setTeamPause":{"teamPausedAt":"` + paused1336 + `"}}}`
	r["ChangeWorkerAgent"] = `{"data":{"changeWorkerAgent":` + strings.ReplaceAll(strings.ReplaceAll(irisWorkerJSON, `"agentId":"agt1"`, `"agentId":"`+agent1336+`"`), `"role":"backend-engineer"`, `"role":"qa-engineer"`) + `}}`
	r["Workers"] = `{"data":{"workers":{"items":[` + irisWorkerJSON + `],"total":1}}}`
	r["GetWorker"] = `{"data":{"worker":` + irisWorkerJSON + `}}`
	return r
}

func runManager1336(t *testing.T, responses map[string]string, args ...string) (*[]attnCall, keyRun) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, args...))
	err := root.Execute()
	if err != nil {
		renderFailure(f, args, err)
	}
	return calls, keyRun{out: out.String(), errOut: f.IOStreams.ErrOut.(*strings.Builder).String(), err: err}
}

func TestMcpC3TeamPauseResumeWire(t *testing.T) {
	for _, verb := range []string{"pause", "resume"} {
		for _, explicit := range []bool{false, true} {
			t.Run(verb+map[bool]string{false: " binding", true: " explicit"}[explicit], func(t *testing.T) {
				responses := managerResponses1336()
				at := any(paused1336)
				if verb == "resume" {
					responses["SetTeamPause"] = `{"data":{"setTeamPause":{"teamPausedAt":null}}}`
					at = nil
				}
				args := []string{"team", verb, "--json"}
				app := "capp100000000000000000000"
				if explicit {
					app = "hrn:app:other.org:team"
					args = append(args, "--app", app)
				}
				calls, r := runManager1336(t, responses, args...)
				if r.err != nil {
					t.Fatal(r.err)
				}
				writes := stopCalls(calls, "SetTeamPause")
				if len(writes) != 1 {
					t.Fatalf("writes=%v", writes)
				}
				if !reflect.DeepEqual(writes[0].Vars, map[string]any{"appRef": app, "paused": verb == "pause"}) {
					t.Fatalf("variables=%v", writes[0].Vars)
				}
				if writes[0].Session != "" {
					t.Fatalf("manager write carried worker session %q", writes[0].Session)
				}
				var dto map[string]any
				if err := json.Unmarshal([]byte(r.out), &dto); err != nil {
					t.Fatal(err, r.out)
				}
				if !reflect.DeepEqual(dto, map[string]any{"app": app, "paused": verb == "pause", "teamPausedAt": at}) {
					t.Fatalf("DTO=%v", dto)
				}
			})
		}
	}
}

func TestMcpC3TeamPauseTextAndServerTruth(t *testing.T) {
	for _, verb := range []string{"pause", "resume"} {
		responses := managerResponses1336()
		// Output follows the returned state, not the requested boolean.
		if verb == "pause" {
			responses["SetTeamPause"] = `{"data":{"setTeamPause":{"teamPausedAt":null}}}`
		}
		_, r := runManager1336(t, responses, "team", verb)
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !strings.Contains(r.out, "app:") || !strings.Contains(r.out, "binding") {
			t.Fatal(r.out)
		}
		want := "team not paused"
		if verb == "resume" {
			want = "team paused since " + paused1336
		}
		if !strings.Contains(r.out, want) {
			t.Fatalf("want %q in %s", want, r.out)
		}
	}
}

func TestMcpC3ChangeAgentWireAndAttribution(t *testing.T) {
	for _, agent := range []string{agent1336, "hrn:agent:acme.com:qa-engineer"} {
		calls, r := runManager1336(t, managerResponses1336(), "team", "worker", "change-agent", "Iris", "--agent", agent, "--yes", "--json")
		if r.err != nil {
			t.Fatal(r.err)
		}
		writes := stopCalls(calls, "ChangeWorkerAgent")
		if len(writes) != 1 || !reflect.DeepEqual(writes[0].Vars, map[string]any{"workerRef": "wkr1", "agentRef": agent}) {
			t.Fatalf("writes=%v", writes)
		}
		if writes[0].Session != "" {
			t.Fatalf("manager write carried worker session %q", writes[0].Session)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(r.out), &dto); err != nil {
			t.Fatal(err, r.out)
		}
		if dto["id"] != "wkr1" || dto["agentId"] != agent1336 || dto["role"] != "qa-engineer" {
			t.Fatal(dto)
		}
	}
	// Positive control: this harness really attaches the binding to authored writes.
	calls, r := runManager1336(t, managerResponses1336(), "team", "task", "start", "task1", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	writes := stopCalls(calls, "AgentTaskStart")
	if len(writes) != 1 || writes[0].Session == "" {
		t.Fatalf("attribution positive control missing: %v", writes)
	}
}

func TestMcpC3ChangeAgentInputAndConfirmation(t *testing.T) {
	for _, args := range [][]string{
		{"Iris", "--yes"}, {"Iris", "--agent", "", "--yes"},
		{"Iris", "--agent", "plain-name", "--yes"}, {"Iris", "--agent", agent1336},
	} {
		calls, r := runManager1336(t, managerResponses1336(), append([]string{"team", "worker", "change-agent"}, args...)...)
		if exitCodeFor(r.err) != 2 || len(*calls) != 0 {
			t.Fatalf("%v: err=%v calls=%v", args, r.err, *calls)
		}
	}
	writeTeamBinding(t)
	srv, calls := attnServer(t, managerResponses1336())
	setTeamBindingServer(t, srv.URL)
	f, out, stderr := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "worker", "change-agent", "Iris", "--agent", agent1336})
	if err := root.Execute(); err == nil || len(*calls) != 0 {
		t.Fatalf("decline err=%v calls=%v", err, *calls)
	}
	if out.Len() != 0 || !strings.Contains(stderr.String(), "boot-prompt") || !strings.Contains(stderr.String(), "tickers") {
		t.Fatalf("prompt=%q out=%q", stderr.String(), out.String())
	}
}

func TestMcpC3ManagerRefusalsKeepCodesAndNeverRetry(t *testing.T) {
	for code, want := range map[string]int{
		"FORBIDDEN": 8, "APP_NOT_FOUND": 4, "WORKER_NOT_FOUND": 4, "WORKER_RETIRED": 5,
		"WORKER_CASTING_STALE": 5, "WORKER_AGENT_UNCHANGED": 5, "WORKER_AGENT_HAS_NO_ROLE": 5,
		"WORKER_AGENT_NOT_INSTALLED": 2, "AGENT_ORG_GRANT_INACTIVE": 8, "AGENT_ORG_GRANT_REQUIRED": 8,
		"BOOT_PROMPT_GRANT_REQUIRED": 8, "BOOT_PROMPT_INCLUDE_UNAVAILABLE": 4, "APP_UNINSTALLED": 5,
		"TICKER_LIMIT_REACHED": 5, "TICKER_NAME_TAKEN": 5, "BAD_USER_INPUT": 2,
	} {
		t.Run(code, func(t *testing.T) {
			responses := managerResponses1336()
			responses["ChangeWorkerAgent"] = gqlErrorJSON(code)
			calls, r := runManager1336(t, responses, "team", "worker", "change-agent", "Iris", "--agent", agent1336, "--yes", "--json")
			if exitCodeFor(r.err) != want || len(stopCalls(calls, "ChangeWorkerAgent")) != 1 {
				t.Fatalf("err=%v exit=%d calls=%v", r.err, exitCodeFor(r.err), *calls)
			}
			var dto struct {
				Error struct {
					Code       int    `json:"code"`
					ServerCode string `json:"serverCode"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(r.out), &dto); err != nil || dto.Error.Code != want || dto.Error.ServerCode != code {
				t.Fatalf("envelope=%s error=%v", r.out, err)
			}
		})
	}
	for _, verb := range []string{"pause", "resume"} {
		for code, want := range map[string]int{"FORBIDDEN": 8, "APP_NOT_FOUND": 4} {
			responses := managerResponses1336()
			responses["SetTeamPause"] = gqlErrorJSON(code)
			calls, r := runManager1336(t, responses, "team", verb, "--json")
			if exitCodeFor(r.err) != want || len(stopCalls(calls, "SetTeamPause")) != 1 || strings.Contains(r.out, "team paused") {
				t.Fatalf("%s/%s: %v %s", verb, code, r.err, r.out)
			}
		}
	}
}

func TestMcpC3NullAnswersAreNotSuccess(t *testing.T) {
	for op, body := range map[string]string{"SetTeamPause": `{"data":{"setTeamPause":null}}`, "ChangeWorkerAgent": `{"data":{"changeWorkerAgent":null}}`} {
		responses := managerResponses1336()
		responses[op] = body
		args := []string{"team", "pause", "--json"}
		if op == "ChangeWorkerAgent" {
			args = []string{"team", "worker", "change-agent", "Iris", "--agent", agent1336, "--yes", "--json"}
		}
		_, r := runManager1336(t, responses, args...)
		if exitCodeFor(r.err) != 1 {
			t.Fatalf("%s: %v %s", op, r.err, r.out)
		}
	}
}

func TestMcpC3ChangeAgentTTYYes(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, managerResponses1336())
	setTeamBindingServer(t, srv.URL)
	f, out, _ := testFactoryTTY(t, "y\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "worker", "change-agent", "Iris", "--agent", agent1336, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(stopCalls(calls, "ChangeWorkerAgent")) != 1 || !strings.Contains(out.String(), agent1336) {
		t.Fatalf("calls=%v output=%s", *calls, out.String())
	}
}

func TestMcpC3AgentInputShapeNotResolution(t *testing.T) {
	for _, agent := range []string{"cagent0000000000000000000", "urn:agent:acme.com:qa", "hrn:agent:unknown.example:missing"} {
		responses := managerResponses1336()
		responses["ChangeWorkerAgent"] = gqlErrorJSON("WORKER_AGENT_NOT_INSTALLED")
		calls, r := runManager1336(t, responses, "team", "worker", "change-agent", "Iris", "--agent", agent, "--yes", "--json")
		if exitCodeFor(r.err) != 2 || len(stopCalls(calls, "ChangeWorkerAgent")) != 1 {
			t.Fatalf("agent=%s err=%v calls=%v", agent, r.err, *calls)
		}
		for _, c := range *calls {
			if c.Op != "Workers" && c.Op != "ChangeWorkerAgent" {
				t.Fatalf("unexpected agent resolution: %v", c)
			}
		}
	}
}

func TestMcpC3LostMutationAnswerIsNotRetried(t *testing.T) {
	for _, op := range []string{"SetTeamPause", "ChangeWorkerAgent"} {
		t.Run(op, func(t *testing.T) {
			writeTeamBinding(t)
			var writes atomic.Int32
			responses := managerResponses1336()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					OperationName string `json:"operationName"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body.OperationName == op {
					writes.Add(1)
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				response, ok := responses[body.OperationName]
				if !ok {
					response, ok = unstubbedDefault(body.OperationName)
				}
				if !ok {
					t.Errorf("unexpected operation %s", body.OperationName)
					response = `{ "errors":[{"message":"unexpected operation"}]}`
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(response))
			}))
			t.Cleanup(srv.Close)
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"--server", srv.URL, "team", "pause", "--json"}
			if op == "ChangeWorkerAgent" {
				args = []string{"--server", srv.URL, "team", "worker", "change-agent", "Iris", "--agent", agent1336, "--yes", "--json"}
			}
			root.SetArgs(args)
			err := root.Execute()
			if exitCodeFor(err) != 7 || writes.Load() != 1 || out.Len() != 0 {
				t.Fatalf("err=%v writes=%d output=%s", err, writes.Load(), out.String())
			}
		})
	}
}
