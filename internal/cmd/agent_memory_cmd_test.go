package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#58 slice 1: `agent memory add|set-role|rm <agent> <memory>` wrap
// addMemoryToAgent / updateAgentMemoryRole / removeMemoryFromAgent. Refs go to
// the server exactly as typed (no client pre-resolve); the server owns authority.

type memCall struct {
	ops  []string
	vars map[string]map[string]any
}

func runAgentMemory(t *testing.T, responses map[string]string, args ...string) (*memCall, string, error) {
	t.Helper()
	calls := &memCall{vars: map[string]map[string]any{}}
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		calls.ops = append(calls.ops, op)
		return responses[op]
	})
	f, _ := testFactory(t)
	var out bytes.Buffer
	root := NewRootCmd(f)
	root.SetOut(&out)
	root.SetArgs(append(append([]string{"agent", "memory"}, args...), "--server", gql.URL))
	err := root.Execute()
	for op, raw := range captured {
		v := map[string]any{}
		_ = json.Unmarshal(raw, &v)
		calls.vars[op] = v
	}
	return calls, out.String(), err
}

func TestAgentMemoryAddSendsRefsVerbatim(t *testing.T) {
	calls, _, err := runAgentMemory(t, map[string]string{
		"AddMemoryToAgent": `{"data":{"addMemoryToAgent":` + agentJSON + `}}`,
	}, "add", "hrn:agent:acme.com:bot", "hrn:mem:acme.com:notes", "--role", "read")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls.ops) != 1 || calls.ops[0] != "AddMemoryToAgent" {
		t.Fatalf("want exactly one AddMemoryToAgent, got %v", calls.ops)
	}
	v := calls.vars["AddMemoryToAgent"]
	if v["agentRef"] != "hrn:agent:acme.com:bot" || v["memoryRef"] != "hrn:mem:acme.com:notes" || v["role"] != "read" {
		t.Fatalf("vars: %v", v)
	}
}

func TestAgentMemoryAddOmitsUnsetRole(t *testing.T) {
	calls, _, err := runAgentMemory(t, map[string]string{
		"AddMemoryToAgent": `{"data":{"addMemoryToAgent":` + agentJSON + `}}`,
	}, "add", "agt1", "mem1")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := calls.vars["AddMemoryToAgent"]["role"]; present {
		t.Fatalf("an unset --role must be omitted, got %v", calls.vars["AddMemoryToAgent"])
	}
}

func TestAgentMemorySetRoleRequiresRoleAndSendsOneCall(t *testing.T) {
	calls, _, err := runAgentMemory(t, nil, "set-role", "agt1", "mem1")
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(calls.ops) != 0 {
		t.Fatalf("missing --role must be a usage error with no call: err=%v ops=%v", err, calls.ops)
	}
	calls, _, err = runAgentMemory(t, map[string]string{
		"UpdateAgentMemoryRole": `{"data":{"updateAgentMemoryRole":` + agentJSON + `}}`,
	}, "set-role", "agt1", "mem1", "--role", "read-write")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls.ops) != 1 || calls.vars["UpdateAgentMemoryRole"]["role"] != "read-write" {
		t.Fatalf("ops=%v vars=%v", calls.ops, calls.vars)
	}
}

func TestAgentMemoryRmConfirmsThenSendsOneCall(t *testing.T) {
	calls, _, err := runAgentMemory(t, nil, "rm", "agt1", "mem1")
	if err == nil || !strings.Contains(err.Error(), "--yes") || len(calls.ops) != 0 {
		t.Fatalf("non-TTY rm without --yes must refuse before any call: err=%v ops=%v", err, calls.ops)
	}
	calls, _, err = runAgentMemory(t, map[string]string{
		"RemoveMemoryFromAgent": `{"data":{"removeMemoryFromAgent":` + agentJSON + `}}`,
	}, "rm", "agt1", "mem1", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls.ops) != 1 || calls.ops[0] != "RemoveMemoryFromAgent" {
		t.Fatalf("ops=%v", calls.ops)
	}
}

func TestAgentMemoryRefusalMapsAndDoesNothingElse(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{{"FORBIDDEN", exitcode.Forbidden}, {"AGENT_NOT_FOUND", exitcode.NotFound}} {
		calls, out, err := runAgentMemory(t, map[string]string{
			"AddMemoryToAgent": `{"errors":[{"message":"refused","extensions":{"code":"` + tc.code + `"}}]}`,
		}, "add", "agt1", "mem1", "--role", "read")
		if err == nil || exitCodeFor(err) != tc.want {
			t.Fatalf("%s: err=%v", tc.code, err)
		}
		if len(calls.ops) != 1 || out != "" {
			t.Fatalf("%s: no retry/follow-up, no output: ops=%v out=%q", tc.code, calls.ops, out)
		}
	}
}
