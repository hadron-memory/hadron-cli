package cmd

import (
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
	f, out := testFactory(t)
	root := NewRootCmd(f)
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

const agentMemoryOps = `[["add","AddMemoryToAgent","addMemoryToAgent",["--role","read"]],` +
	`["set-role","UpdateAgentMemoryRole","updateAgentMemoryRole",["--role","read"]],` +
	`["rm","RemoveMemoryFromAgent","removeMemoryFromAgent",["--yes"]]]`

type agentMemoryVerb struct {
	verb, op, field string
	extra           []string
}

func agentMemoryVerbs(t *testing.T) []agentMemoryVerb {
	t.Helper()
	var raw [][]any
	if err := json.Unmarshal([]byte(agentMemoryOps), &raw); err != nil {
		t.Fatal(err)
	}
	var out []agentMemoryVerb
	for _, r := range raw {
		v := agentMemoryVerb{verb: r[0].(string), op: r[1].(string), field: r[2].(string)}
		for _, e := range r[3].([]any) {
			v.extra = append(v.extra, e.(string))
		}
		out = append(out, v)
	}
	return out
}

// Refs go out EXACTLY as typed on every verb: padding is the server's to
// judge, never the client's to trim (review: LOCAL P2, BLIND P3).
func TestAgentMemoryPaddedRefsAreVerbatim(t *testing.T) {
	for _, v := range agentMemoryVerbs(t) {
		t.Run(v.verb, func(t *testing.T) {
			calls, _, err := runAgentMemory(t, map[string]string{
				v.op: `{"data":{"` + v.field + `":` + agentJSON + `}}`,
			}, append([]string{v.verb, " agt1 ", " mem1 "}, v.extra...)...)
			if err != nil {
				t.Fatal(err)
			}
			got := calls.vars[v.op]
			if got["agentRef"] != " agt1 " || got["memoryRef"] != " mem1 " {
				t.Fatalf("refs must be sent untrimmed, got %v", got)
			}
		})
	}
}

// A blank ref is a usage error before any call.
func TestAgentMemoryBlankRefIsUsageWithNoCall(t *testing.T) {
	for _, v := range agentMemoryVerbs(t) {
		for _, refs := range [][]string{{"  ", "mem1"}, {"agt1", ""}} {
			calls, out, err := runAgentMemory(t, nil, append(append([]string{v.verb}, refs...), v.extra...)...)
			if err == nil || exitCodeFor(err) != exitcode.Usage || len(calls.ops) != 0 || out != "" {
				t.Fatalf("%s %q: err=%v ops=%v out=%q", v.verb, refs, err, calls.ops, out)
			}
		}
	}
}

// A null mutation result (declared non-null, but a fault can still send one)
// is a controlled error with exactly one call and no output, never a panic.
func TestAgentMemoryNullResultIsControlledError(t *testing.T) {
	for _, v := range agentMemoryVerbs(t) {
		for name, body := range map[string]string{
			"null":    `{"data":{"` + v.field + `":null}}`,
			"missing": `{"data":{}}`,
		} {
			t.Run(v.verb+"/"+name, func(t *testing.T) {
				calls, out, err := runAgentMemory(t, map[string]string{v.op: body},
					append([]string{v.verb, "agt1", "mem1"}, v.extra...)...)
				if err == nil || exitCodeFor(err) != exitcode.Error {
					t.Fatalf("err=%v", err)
				}
				if len(calls.ops) != 1 || out != "" {
					t.Fatalf("ops=%v out=%q", calls.ops, out)
				}
			})
		}
	}
}

// Refusals map through MapError on every verb: one call, nothing written.
func TestAgentMemoryRefusalMapsOnEveryVerb(t *testing.T) {
	for _, v := range agentMemoryVerbs(t) {
		for _, tc := range []struct {
			code string
			want int
		}{{"FORBIDDEN", exitcode.Forbidden}, {"AGENT_NOT_FOUND", exitcode.NotFound}, {"BAD_USER_INPUT", exitcode.Usage}} {
			t.Run(v.verb+"/"+tc.code, func(t *testing.T) {
				calls, out, err := runAgentMemory(t, map[string]string{
					v.op: `{"errors":[{"message":"refused","extensions":{"code":"` + tc.code + `"}}]}`,
				}, append([]string{v.verb, "agt1", "mem1"}, v.extra...)...)
				if err == nil || exitCodeFor(err) != tc.want || len(calls.ops) != 1 || out != "" {
					t.Fatalf("err=%v ops=%v out=%q", err, calls.ops, out)
				}
			})
		}
	}
}

// --json carries the original agent values with empty slices as [], and the
// text line neutralises control characters from server-authored names.
func TestAgentMemoryOutputShapes(t *testing.T) {
	body := `{"data":{"addMemoryToAgent":` + agentJSON + `}}`
	_, out, err := runAgentMemory(t, map[string]string{"AddMemoryToAgent": body}, "add", "agt1", "mem1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"surfaces", "memberRoles"} {
		if arr, ok := dto[k].([]any); !ok || len(arr) != 0 {
			t.Fatalf("%s must be [], got %#v", k, dto[k])
		}
	}
	if dto["id"] != "agt1" || dto["name"] != "Support Bot" {
		t.Fatalf("dto: %v", dto)
	}

	evil := strings.Replace(agentJSON, `"Support Bot"`, `"Bad\u001b[31mBot\nX"`, 1)
	_, out, err = runAgentMemory(t, map[string]string{"AddMemoryToAgent": `{"data":{"addMemoryToAgent":` + evil + `}}`}, "add", "agt1", "mem1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "\x1b") || strings.Count(out, "\n") != 1 {
		t.Fatalf("text output must be one control-free line: %q", out)
	}
}
