package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #1485 S1 (cor:agt:060:12, cor:api:240:01): Agent and App model-group CRUD.
// The CLI is a pass-through: names and patterns go on the wire exactly as
// given, an empty pattern list is an explicit [], every refusal is the
// server's, and nothing is pre-read or retried.

const mgGroupJSON = `{"name":"frontier","patterns":["claude-opus-*","claude-sonnet-*"],"updatedAt":"2026-11-01T00:00:00Z"}`

// mgVars decodes one captured request's variables, keeping null/absent
// distinguishable via json.RawMessage.
func mgVars(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	vars := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("variables are not a JSON object: %v (%s)", err, raw)
	}
	return vars
}

func mgRun(t *testing.T, url string, args ...string) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", url))
	err := root.Execute()
	// --json is indented; compact it so json.RawMessage fields compare as
	// their literal wire spelling ([] vs null, list order).
	var buf bytes.Buffer
	if json.Valid([]byte(out.String())) && json.Compact(&buf, []byte(out.String())) == nil {
		return buf.String(), err
	}
	return out.String(), err
}

// mgCountingGraphQL answers from a fixed map and counts calls per operation, so
// a test can pin "exactly one write, no preflight read, no retry".
func mgCountingGraphQL(t *testing.T, responses map[string]string) (string, map[string]json.RawMessage, func(string) int) {
	t.Helper()
	var mu sync.Mutex
	calls := map[string]int{}
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		calls[op]++
		mu.Unlock()
		return responses[op]
	})
	return gql.URL, captured, func(op string) int {
		mu.Lock()
		defer mu.Unlock()
		return calls[op]
	}
}

func TestAgentModelGroupSetSendsTheLiteralRepeatedPatternList(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SetAgentModelGroup": `{"data":{"setAgentModelGroup":` + mgGroupJSON + `}}`,
	})
	out, err := mgRun(t, gql.URL, "agent", "model-group", "set", "agt1", "Frontier",
		"--pattern", "claude-opus-*", "--pattern", "claude-sonnet-*", "--pattern", "a,b", "--json")
	if err != nil {
		t.Fatal(err)
	}
	vars := mgVars(t, captured["SetAgentModelGroup"])
	if string(vars["agentRef"]) != `"agt1"` {
		t.Fatalf("agentRef = %s", vars["agentRef"])
	}
	// Not lowercased: the server owns normalisation (Vera FIT note 3).
	if string(vars["name"]) != `"Frontier"` {
		t.Fatalf("name must be sent as given, got %s", vars["name"])
	}
	// Repeated flags, in order, and a comma is part of one glob, never a split.
	if string(vars["patterns"]) != `["claude-opus-*","claude-sonnet-*","a,b"]` {
		t.Fatalf("patterns = %s", vars["patterns"])
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got["name"] != "frontier" || got["updatedAt"] != "2026-11-01T00:00:00Z" {
		t.Fatalf("the returned (stored) group is printed: %v", got)
	}
	if _, has := got["origin"]; has {
		t.Fatalf("an Agent group has no origin: %v", got)
	}
}

func TestModelGroupSetWithNoPatternSendsAnExplicitEmptyList(t *testing.T) {
	cases := []struct {
		name, op, field string
		args            []string
	}{
		{"agent", "SetAgentModelGroup", "setAgentModelGroup", []string{"agent", "model-group", "set", "agt1", "parked"}},
		{"app", "SetAppModelGroup", "setAppModelGroup", []string{"app", "model-group", "set", "hrn:app:acme.com:eng", "parked"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				tc.op: `{"data":{"` + tc.field + `":{"name":"parked","patterns":[],"updatedAt":"2026-11-01T00:00:00Z"}}}`,
			})
			out, err := mgRun(t, gql.URL, append(tc.args, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			vars := mgVars(t, captured[tc.op])
			raw, present := vars["patterns"]
			if !present || string(raw) != `[]` {
				t.Fatalf("no --pattern must send patterns: [] (never null or absent), got present=%v %s", present, raw)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out)
			}
			if string(got["patterns"]) != `[]` {
				t.Fatalf("an empty group renders patterns: [], got %s", got["patterns"])
			}
		})
	}
}

func TestAppModelGroupSetSendsAppRefNameAndPatterns(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SetAppModelGroup": `{"data":{"setAppModelGroup":` + mgGroupJSON + `}}`,
	})
	if _, err := mgRun(t, gql.URL, "app", "model-group", "set", "hrn:app:acme.com:eng", "frontier", "--pattern", "claude-opus-*"); err != nil {
		t.Fatal(err)
	}
	vars := mgVars(t, captured["SetAppModelGroup"])
	if string(vars["appRef"]) != `"hrn:app:acme.com:eng"` || string(vars["name"]) != `"frontier"` || string(vars["patterns"]) != `["claude-opus-*"]` {
		t.Fatalf("vars = %v", vars)
	}
}

func TestAppModelGroupListMapsEveryOrigin(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"EffectiveModelGroups": `{"data":{"effectiveModelGroups":[
			{"name":"a","patterns":["x-*"],"origin":"AGENT"},
			{"name":"b","patterns":[],"origin":"APP_OVERRIDE"},
			{"name":"c","patterns":["y"],"origin":"APP"}]}}`,
	})
	out, err := mgRun(t, gql.URL, "app", "model-group", "list", "hrn:app:acme.com:eng", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if string(mgVars(t, captured["EffectiveModelGroups"])["appRef"]) != `"hrn:app:acme.com:eng"` {
		t.Fatalf("appRef not sent as given: %s", captured["EffectiveModelGroups"])
	}
	var got []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	want := []struct{ name, patterns, origin string }{
		{`"a"`, `["x-*"]`, `"agent"`},
		{`"b"`, `[]`, `"app-override"`},
		{`"c"`, `["y"]`, `"app-only"`},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %d, want %d: %s", len(got), len(want), out)
	}
	for i, w := range want {
		if string(got[i]["name"]) != w.name || string(got[i]["patterns"]) != w.patterns || string(got[i]["origin"]) != w.origin {
			t.Fatalf("row %d = %v, want %+v", i, got[i], w)
		}
		if len(got[i]) != 3 {
			t.Fatalf("row %d carries exactly {name, patterns, origin}: %v", i, got[i])
		}
	}
}

func TestAppModelGroupListHumanShowsOriginLabels(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"EffectiveModelGroups": `{"data":{"effectiveModelGroups":[
			{"name":"a","patterns":["x-*"],"origin":"AGENT"},
			{"name":"b","patterns":[],"origin":"APP_OVERRIDE"},
			{"name":"c","patterns":["y"],"origin":"APP"}]}}`,
	})
	out, err := mgRun(t, gql.URL, "app", "model-group", "list", "hrn:app:acme.com:eng")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"ORIGIN", "agent", "app-override", "app-only", "(none)"} {
		if !strings.Contains(out, s) {
			t.Fatalf("human list lacks %q:\n%s", s, out)
		}
	}
}

func TestModelGroupListsRenderEmptyAsArray(t *testing.T) {
	cases := []struct {
		name, op, body string
		args           []string
	}{
		{"agent", "AgentModelGroups", `{"data":{"agentModelGroups":[]}}`, []string{"agent", "model-group", "list", "agt1"}},
		{"app", "EffectiveModelGroups", `{"data":{"effectiveModelGroups":[]}}`, []string{"app", "model-group", "list", "hrn:app:acme.com:eng"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{tc.op: tc.body})
			out, err := mgRun(t, gql.URL, append(tc.args, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(out) != "[]" {
				t.Fatalf("an empty list is [], got %q", out)
			}
		})
	}
}

func TestAgentModelGroupListJSONHasNoOrigin(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AgentModelGroups": `{"data":{"agentModelGroups":[` + mgGroupJSON + `]}}`,
	})
	out, err := mgRun(t, gql.URL, "agent", "model-group", "list", "hrn:agent:acme.com:iris", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if string(mgVars(t, captured["AgentModelGroups"])["agentRef"]) != `"hrn:agent:acme.com:iris"` {
		t.Fatalf("agentRef = %s", captured["AgentModelGroups"])
	}
	var got []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 1 || len(got[0]) != 3 || string(got[0]["patterns"]) != `["claude-opus-*","claude-sonnet-*"]` {
		t.Fatalf("rows = %s", out)
	}
}

// Vera FIT note 1 / Xylon fence: clear on an Agent-only name goes straight to
// deleteAppModelGroup; the server's MODEL_GROUP_NOT_FOUND comes back through
// MapError unchanged, the exit is non-zero, and nothing is read first or
// retried.
func TestAppModelGroupClearForwardsTheServerRefusal(t *testing.T) {
	const msg = `No model group named "frontier" is declared here.`
	url, captured, calls := mgCountingGraphQL(t, map[string]string{
		"DeleteAppModelGroup": `{"errors":[{"message":"` + strings.ReplaceAll(msg, `"`, `\"`) + `","extensions":{"code":"MODEL_GROUP_NOT_FOUND"}}],"data":null}`,
	})
	_, err := mgRun(t, url, "app", "model-group", "clear", "hrn:app:acme.com:eng", "frontier", "--yes")
	if err == nil {
		t.Fatal("an Agent-only clear must fail with the server's refusal")
	}
	if got := exitCodeFor(err); got != exitcode.NotFound {
		t.Fatalf("exit = %d, want %d (MapError's NOT_FOUND mapping)", got, exitcode.NotFound)
	}
	if !strings.Contains(err.Error(), msg) {
		t.Fatalf("the server message must be shown verbatim, got %q", err.Error())
	}
	if n := calls("DeleteAppModelGroup"); n != 1 {
		t.Fatalf("one delete, never retried: %d calls", n)
	}
	if n := calls("EffectiveModelGroups"); n != 0 {
		t.Fatalf("no effective-list pre-check: %d calls", n)
	}
	vars := mgVars(t, captured["DeleteAppModelGroup"])
	if string(vars["appRef"]) != `"hrn:app:acme.com:eng"` || string(vars["name"]) != `"frontier"` {
		t.Fatalf("vars = %v", vars)
	}
}

func TestModelGroupDeleteAndClearConfirm(t *testing.T) {
	cases := []struct {
		name, op, field string
		args            []string
		wantKey         string
	}{
		{"agent delete", "DeleteAgentModelGroup", "deleteAgentModelGroup", []string{"agent", "model-group", "delete", "agt1", "parked"}, "agent"},
		{"app clear", "DeleteAppModelGroup", "deleteAppModelGroup", []string{"app", "model-group", "clear", "hrn:app:acme.com:eng", "parked"}, "app"},
	}
	for _, tc := range cases {
		t.Run(tc.name+" without --yes sends nothing", func(t *testing.T) {
			url, _, calls := mgCountingGraphQL(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":true}}`})
			_, err := mgRun(t, url, tc.args...)
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("exit = %d (%v), want usage", got, err)
			}
			if n := calls(tc.op); n != 0 {
				t.Fatalf("nothing is sent before consent: %d calls", n)
			}
		})
		t.Run(tc.name+" with --yes", func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":true}}`})
			out, err := mgRun(t, gql.URL, append(tc.args, "--yes", "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			if string(mgVars(t, captured[tc.op])["name"]) != `"parked"` {
				t.Fatalf("vars = %s", captured[tc.op])
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out)
			}
			if got["name"] != "parked" || got[tc.wantKey] == nil {
				t.Fatalf("confirmation DTO = %v", got)
			}
		})
	}
}

// Refusals are the server's: each code keeps MapError's mapping and message,
// and a refused write is sent once.
func TestModelGroupWritesForwardRefusalsUnchanged(t *testing.T) {
	cases := []struct {
		code string
		want int
		op   string
		args []string
	}{
		{"FORBIDDEN", exitcode.Forbidden, "SetAgentModelGroup", []string{"agent", "model-group", "set", "agt1", "g", "--pattern", "x"}},
		{"AGENT_NOT_FOUND", exitcode.NotFound, "AgentModelGroups", []string{"agent", "model-group", "list", "agt1"}},
		{"APP_MANAGER_REQUIRED", exitcode.Forbidden, "SetAppModelGroup", []string{"app", "model-group", "set", "hrn:app:acme.com:eng", "g"}},
		{"APP_NOT_FOUND", exitcode.NotFound, "EffectiveModelGroups", []string{"app", "model-group", "list", "hrn:app:acme.com:eng"}},
		{"BAD_USER_INPUT", exitcode.Usage, "SetAgentModelGroup", []string{"agent", "model-group", "set", "agt1", "bad name!"}},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			url, _, calls := mgCountingGraphQL(t, map[string]string{
				tc.op: `{"errors":[{"message":"server says ` + tc.code + `","extensions":{"code":"` + tc.code + `"}}],"data":null}`,
			})
			_, err := mgRun(t, url, tc.args...)
			if err == nil {
				t.Fatalf("%s must fail", tc.code)
			}
			if got := exitCodeFor(err); got != tc.want {
				t.Fatalf("exit = %d, want %d", got, tc.want)
			}
			if !strings.Contains(err.Error(), "server says "+tc.code) {
				t.Fatalf("message not forwarded: %q", err.Error())
			}
			if n := calls(tc.op); n != 1 {
				t.Fatalf("one call, no retry: %d", n)
			}
		})
	}
}

// Vera FIT note 6: S1 help says a group is a matching input only, and does
// not claim task matching exists yet.
func TestModelGroupHelpStatesMatchingOnly(t *testing.T) {
	for _, args := range [][]string{
		{"agent", "model-group", "--help"},
		{"app", "model-group", "--help"},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		help := strings.Join(strings.Fields(out.String()), " ")
		if !strings.Contains(help, "matching input only, never an authorization input") {
			t.Fatalf("%v help must state the matching-only rule:\n%s", args, out.String())
		}
		for _, claim := range []string{"--model-group", "modelGroupIn", "tasks require"} {
			if strings.Contains(help, claim) {
				t.Fatalf("%v help claims an S2 surface (%q):\n%s", args, claim, out.String())
			}
		}
	}
}
