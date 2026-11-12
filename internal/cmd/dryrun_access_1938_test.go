package cmd

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type dryCase1938 struct {
	name     string
	args     []string
	mocks    func() map[string]string
	targetOp string
	memory   string
}

func dryCases1938() []dryCase1938 {
	cases := []dryCase1938{
		{"supersede", []string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--dry-run"}, func() map[string]string { return supersedeRawMocks(cleanSpecDetailContent, cleanSpecDetailContent) }, "GetNode", "hrn:mem:micromentor.org:platform-specs"},
		{"extract", []string{"spec", "extract", "cor:dmo:060:02", "-m", specMem, "--to-feature", "020", "--title", "Node type", "--dry-run"}, extractMocks, "GetSpecNodeRaw", "hrn:mem:micromentor.org:platform-specs"},
		{"node", []string{"node", "update", testNodeURN, "--name", "Renamed", "--dry-run"}, func() map[string]string { return bodyOnlyStubs(delimiterBody) }, "GetNodeRaw", "mem1"},
	}
	by := cases[0]
	by.name = "supersede-by"
	by.args = []string{"spec", "supersede", "msg:010:02", "-m", specMem, "--by", "msg:010:03", "--dry-run"}
	by.mocks = func() map[string]string {
		m := supersedeRawMocks(cleanSpecDetailContent, cleanSpecDetailContent)
		m["ResolveUrn:2"] = `{"data":{"resolveUrn":{"id":"new1","kind":"node","memoryId":"mem1"}}}`
		target, _ := json.Marshal(node579("new1", "msg:010:03"))
		m["GetSpecSupersedeTarget"] = `{"data":{"node":` + string(target) + `}}`
		return m
	}
	cases = append(cases, by)
	direct := cases[0]
	direct.name = "supersede-to"
	direct.args = append(append([]string{}, direct.args...), "--to", "msg:next")
	direct.mocks = func() map[string]string {
		m := supersedeRawMocks(cleanSpecDetailContent, cleanSpecDetailContent)
		m["ResolveUrn:2"] = `{"data":{"resolveUrn":null}}`
		return m
	}
	cases = append(cases, direct)
	for _, done := range []bool{false, true} {
		resumed := cases[0]
		resumed.name = "supersede-resume"
		if done {
			resumed.name = "supersede-unchanged"
		}
		resumed.mocks = func() map[string]string {
			m := supersedeRawMocks(cleanSpecDetailContent, cleanSpecDetailContent)
			var envelope map[string]any
			if err := json.Unmarshal([]byte(m["GetNode"]), &envelope); err != nil {
				panic(err)
			}
			old := envelope["data"].(map[string]any)["node"].(map[string]any)
			old["outgoingEdges"] = []any{map[string]any{"id": "edge1", "name": "superseded-by", "target": map[string]any{"id": "new1", "loc": "msg:010:03", "memoryId": "mem1"}}}
			if done {
				old["abstract"] = "SUPERSEDED (2026-01-01) by msg:010:03 (W2 v2); do not apply. Historical: Old."
				old["tags"] = []string{"spec", "superseded"}
			}
			raw, err := json.Marshal(envelope)
			if err != nil {
				panic(err)
			}
			m["GetNode"] = string(raw)
			return m
		}
		cases = append(cases, resumed)
	}
	return cases
}

// Record every operation, including repeated calls. Existing fixture paging and
// default responses are handled by captureGraphQLFunc, not duplicated here.
func runDry1938(t *testing.T, c dryCase1938, mocks map[string]string, asJSON bool) (string, map[string]json.RawMessage, []string, error) {
	t.Helper()
	var mu sync.Mutex
	calls := []string{}
	counts := map[string]int{}
	var accessVariables map[string]json.RawMessage
	srv, captured := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, op)
		counts[op]++
		// Match the real effectiveAccess dispatcher: bare IDs are AI configs,
		// not memories (server source 0c816205, effectiveAccess.ts resolveResource).
		if op == "EffectiveAccess" {
			capturedMu.Lock()
			raw := accessVariables[op]
			capturedMu.Unlock()
			var resource struct{ Resource string }
			if err := json.Unmarshal(raw, &resource); err != nil {
				t.Error(err)
			}
			if !strings.Contains(resource.Resource, ":") {
				return `{"errors":[{"message":"Resource not found","extensions":{"code":"NOT_FOUND"}}]}`
			}
		}
		if response, ok := mocks[op+":"+strconv.Itoa(counts[op])]; ok {
			return translateFindNodes(op, response)
		}
		return translateFindNodes(op, mocks[op])
	})
	accessVariables = captured
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := append([]string{}, c.args...)
	if asJSON {
		args = append(args, "--json")
	}
	root.SetArgs(append(args, "--server", srv.URL))
	err := root.Execute()
	mu.Lock()
	defer mu.Unlock()
	return out.String(), captured, calls, err
}

func noWrites1938(t *testing.T, calls []string) {
	t.Helper()
	allowed := map[string]bool{"ResolveUrn": true, "GetNode": true, "GetSpecSupersedeTarget": true, "GetNodeRaw": true, "GetSpecNodeRaw": true, "FindNodes": true, "NodeBatch": true, "SpecCorpusState": true, "GetMemory": true, "Memories": true, "AuthContext": true, "EffectiveAccess": true}
	for _, op := range calls {
		if !allowed[op] {
			t.Errorf("dry run sent unexpected operation %s", op)
		}
	}
}

func TestDryRun1938ReaderRefused(t *testing.T) {
	for _, c := range dryCases1938() {
		for _, asJSON := range []bool{false, true} {
			t.Run(c.name+map[bool]string{false: "/text", true: "/json"}[asJSON], func(t *testing.T) {
				out, vars, calls, err := runDry1938(t, c, accessMocks(c.mocks(), false), asJSON)
				if exitCodeFor(err) != exitcode.Forbidden || err == nil || err.Error() != writeRefusal {
					t.Fatalf("got %v exit %d output %q; want refusal exit8", err, exitCodeFor(err), out)
				}
				if out != "" {
					t.Errorf("reader got plan: %s", out)
				}
				var input struct{ User, Resource string }
				if err := json.Unmarshal(vars["EffectiveAccess"], &input); err != nil || input.User != "u1" || input.Resource != c.memory {
					t.Fatalf("wrong access target: %s (%v)", vars["EffectiveAccess"], err)
				}
				seenTarget := false
				auth, access := 0, 0
				for _, op := range calls {
					if op == c.targetOp {
						seenTarget = true
					}
					if op == "AuthContext" {
						auth++
						if !seenTarget {
							t.Error("access before target")
						}
					}
					if op == "EffectiveAccess" {
						access++
					}
				}
				if auth != 1 || access != 1 {
					t.Errorf("want one access check: %v", calls)
				}
				noWrites1938(t, calls)
			})
		}
	}
}

func TestDryRun1938WriterAndNoUserPlan(t *testing.T) {
	for _, c := range dryCases1938() {
		for _, mode := range []string{"writer", "no-user", "app-key"} {
			t.Run(c.name+"/"+mode, func(t *testing.T) {
				m := accessMocks(c.mocks(), true)
				if mode == "no-user" {
					m["AuthContext"] = `{"data":{"authContext":null}}`
				}
				if mode == "app-key" {
					m["AuthContext"] = `{"data":{"authContext":{"principalType":"APP","user":null,"appId":"app1","agentId":null,"apiKey":null,"impersonation":null}}}`
				}
				out, vars, calls, err := runDry1938(t, c, m, true)
				if err != nil || strings.TrimSpace(out) == "" {
					t.Fatalf("plan: %v %q calls %v", err, out, calls)
				}
				var plan map[string]any
				if err := json.Unmarshal([]byte(out), &plan); err != nil || plan["dryRun"] != true {
					t.Fatalf("not dry plan: %s %v", out, err)
				}
				if mode != "writer" {
					if _, ok := vars["EffectiveAccess"]; ok {
						t.Error("no-user consulted access")
					}
				}
				noWrites1938(t, calls)
			})
		}
	}
}

func TestDryRun1938UnreadableTargetKeepsNotFound(t *testing.T) {
	for _, c := range dryCases1938() {
		for _, response := range []string{`{"data":{"node":null}}`, `{"errors":[{"message":"not found","extensions":{"code":"NOT_FOUND"}}]}`} {
			t.Run(c.name+response, func(t *testing.T) {
				m := accessMocks(c.mocks(), false)
				m[c.targetOp] = response
				out, vars, calls, err := runDry1938(t, c, m, false)
				if exitCodeFor(err) != exitcode.NotFound || out != "" {
					t.Fatalf("want notfound/no plan: %v exit%d %q", err, exitCodeFor(err), out)
				}
				for _, op := range []string{"AuthContext", "EffectiveAccess"} {
					if _, ok := vars[op]; ok {
						t.Errorf("%s queried after unreadable target", op)
					}
				}
				noWrites1938(t, calls)
			})
		}
	}
}

func TestDryRun1938AccessFailureNeverPlans(t *testing.T) {
	for _, c := range dryCases1938() {
		for _, op := range []string{"AuthContext", "EffectiveAccess"} {
			t.Run(c.name+"/"+op, func(t *testing.T) {
				m := accessMocks(c.mocks(), true)
				m[op] = `{"errors":[{"message":"lookup failed","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
				out, _, calls, err := runDry1938(t, c, m, false)
				if err == nil || exitCodeFor(err) == exitcode.Forbidden || out != "" {
					t.Fatalf("lookup failure misread: %v %q", err, out)
				}
				noWrites1938(t, calls)
			})
		}
	}
}

func TestDryRun1938UnreadableSuccessorBeforeAccess(t *testing.T) {
	var c dryCase1938
	for _, candidate := range dryCases1938() {
		if candidate.name == "supersede-by" {
			c = candidate
		}
	}
	m := accessMocks(c.mocks(), false)
	m["GetSpecSupersedeTarget"] = `{"data":{"node":null}}`
	out, vars, _, err := runDry1938(t, c, m, false)
	if exitCodeFor(err) != exitcode.NotFound || out != "" {
		t.Fatalf("successor: %v %q", err, out)
	}
	if _, ok := vars["AuthContext"]; ok {
		t.Error("access queried before unreadable successor refusal")
	}
}

// These controls require reaching the actual mutation, not completing the
// entire stateful supersede flow; its existing dedicated suites cover that.
func TestDryRun1938RealPathsLeaveAccessToTheWrite(t *testing.T) {
	for _, c := range dryCases1938() {
		if c.name != "supersede" && c.name != "extract" && c.name != "node" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			args := []string{}
			for _, arg := range c.args {
				if arg != "--dry-run" {
					args = append(args, arg)
				}
			}
			c.args = args
			if c.name == "supersede" {
				c.args = append(c.args, "--yes")
			}
			_, vars, calls, _ := runDry1938(t, c, accessMocks(c.mocks(), false), false)
			for _, op := range []string{"AuthContext", "EffectiveAccess"} {
				if _, ok := vars[op]; ok {
					t.Errorf("real path consulted %s", op)
				}
			}
			write := map[string]string{"supersede": "CreateSpecNode", "extract": "CreateSpecNode", "node": "UpdateNode"}[c.name]
			if _, ok := vars[write]; !ok {
				t.Fatalf("real path never reached %s: %v", write, calls)
			}
		})
	}
}
