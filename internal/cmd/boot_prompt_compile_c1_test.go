package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var c1Commands = []struct {
	name, op, field string
	args            []string
}{
	{"agent", "CompileAgentBootPrompt", "compileAgentBootPrompt", []string{"agent", "compile", "hrn:agent:acme.com:support-bot"}},
	{"app", "CompileAppAgentBootPrompt", "compileAppAgentBootPrompt", []string{"app", "agent", "compile", "hrn:app:acme.com:eng-team", "hrn:agent:acme.com:support-bot"}},
	{"worker", "CompileWorkerBootPrompt", "compileWorkerBootPrompt", []string{"team", "worker", "compile", "hrn:worker:acme.com:eng-team:iris"}},
}

func c1Responses() map[string]string {
	r := map[string]string{"GetWorker": `{"data":{"worker":` + irisWorkerJSON + `}}`}
	for _, c := range c1Commands {
		r[c.op] = `{"data":{"` + c.field + `":{"node":{"id":"n1","urn":"` + c2NodeURN + `"},"revNo":7,"contentHash":"abc123","pointerSet":true,"approvalIssued":false}}}`
	}
	return r
}

func TestC1CompileVariablesAndStableJSON(t *testing.T) {
	for _, c := range c1Commands {
		t.Run(c.name, func(t *testing.T) {
			calls, out, err := runTeamTask(t, c1Responses(), append(c.args, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			call := c2Mutation(t, calls, c.op)
			switch c.name {
			case "agent":
				if call.Vars["agentRef"] != c.args[2] {
					t.Fatalf("wrong agent: %s", call.RawVars)
				}
			case "app":
				if call.Vars["appRef"] != c.args[3] || call.Vars["agentRef"] != c.args[4] {
					t.Fatalf("wrong app/agent: %s", call.RawVars)
				}
			case "worker":
				if call.Vars["workerRef"] != "wkr1" {
					t.Fatalf("wrong resolved worker: %s", call.RawVars)
				}
			}

			if call.Session != "" {
				t.Fatalf("compile sent worker session %q", call.Session)
			}
			if _, present := call.Vars["setPointer"]; present {
				t.Fatalf("default must omit setPointer: %s", call.RawVars)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			if len(dto) != 5 || dto["compiledNodeRef"] != c2NodeURN || dto["revision"] != float64(7) || dto["contentHash"] != "abc123" || dto["pointerSet"] != true || dto["approvalIssued"] != false {
				t.Fatalf("unexpected stable result %s", out)
			}
		})
	}
}

func TestC1NoSetPointerAndAppRefusal(t *testing.T) {
	for _, c := range c1Commands {
		t.Run(c.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, c1Responses(), append(c.args, "--no-set-pointer")...)
			if c.name == "app" {
				if err == nil || len(*calls) != 0 {
					t.Fatalf("App must refuse flag before requests: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			call := c2Mutation(t, calls, c.op)
			if call.Vars["setPointer"] != false {
				t.Fatalf("must send explicit false: %s", call.RawVars)
			}
		})
	}
}

func TestC1RefusalsNeverRetry(t *testing.T) {
	codes := map[string]int{
		"BOOT_PROMPT_COMPILE_NOT_PERMITTED": 8, "BOOT_PROMPT_TARGET_RETIRED": 5, "BOOT_PROMPT_NO_SOURCE": 5, "BOOT_PROMPT_SOURCE_INVALID": 5,
		"BOOT_PROMPT_INCLUDE_RETIRED": 5, "BOOT_PROMPT_INCLUDE_REVISION_UNAVAILABLE": 5, "BOOT_PROMPT_INCLUDE_UNAVAILABLE": 4,
		"BOOT_PROMPT_INCLUDE_CYCLE": 2, "BOOT_PROMPT_INCLUDE_TOO_DEEP": 2, "BOOT_PROMPT_INCLUDE_SOURCE_DISALLOWED": 2,
		"BOOT_PROMPT_INCLUDE_MALFORMED": 2, "BOOT_PROMPT_COMPILE_LIMIT_EXCEEDED": 2, "BOOT_PROMPT_GRANT_REQUIRED": 8, "BAD_USER_INPUT": 2, "CONFLICT": 5,
	}
	for code, want := range codes {
		t.Run(code, func(t *testing.T) {
			r := c1Responses()
			r[c1Commands[0].op] = `{"errors":[{"message":"bounded refusal","extensions":{"code":"` + code + `"}}]}`
			calls, _, err := runTeamTask(t, r, c1Commands[0].args...)
			if code != "CONFLICT" && (err == nil || err.Error() != "bounded refusal") {
				t.Fatalf("concealed server message changed: %v", err)
			}
			if exitCodeFor(err) != want {
				t.Fatalf("got %v, want exit %d", err, want)
			}
			c2Mutation(t, calls, c1Commands[0].op)
		})
	}
}

func TestC1HumanOutputDoesNotClaimApprovalStatus(t *testing.T) {
	_, out, err := runTeamTask(t, c1Responses(), c1Commands[0].args...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "issues no approval") || strings.Contains(out, "unapproved") || !strings.Contains(out, "node get") {
		t.Fatalf("misleading approval output %s", out)
	}
}

func TestC1TransportOutcomeUnknownNoRetry(t *testing.T) {
	var mutations int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutations++
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("private gateway body"))
	}))
	defer srv.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, c1Commands[0].args...))
	err := root.Execute()
	if exitCodeFor(err) != 7 || mutations != 1 {
		t.Fatalf("err=%v requests=%d output=%s", err, mutations, out.String())
	}
	if !strings.Contains(err.Error(), "outcome is unknown") || !strings.Contains(err.Error(), "node get") || strings.Contains(err.Error(), "private gateway body") {
		t.Fatalf("unsafe recovery: %v", err)
	}
}
