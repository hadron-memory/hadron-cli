package cmd

import (
	"encoding/json"
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
	return []dryCase1938{
		{"supersede", []string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--dry-run"}, func() map[string]string { return supersedeRawMocks(cleanSpecDetailContent, cleanSpecDetailContent) }, "GetNode", "hrn:mem:micromentor.org:platform-specs"},
		{"extract", []string{"spec", "extract", "cor:dmo:060:02", "-m", specMem, "--to-feature", "020", "--title", "Node type", "--dry-run"}, extractMocks, "GetSpecNodeRaw", "hrn:mem:micromentor.org:platform-specs"},
		{"node", []string{"node", "update", testNodeURN, "--name", "Renamed", "--dry-run"}, func() map[string]string { return bodyOnlyStubs(delimiterBody) }, "GetNodeRaw", "mem1"},
	}
}

// Record every operation, including repeated calls. Existing fixture paging and
// default responses are handled by captureGraphQLFunc, not duplicated here.
func runDry1938(t *testing.T, c dryCase1938, mocks map[string]string, asJSON bool) (string, map[string]json.RawMessage, []string, error) {
	t.Helper()
	var mu sync.Mutex
	calls := []string{}
	srv, captured := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, op)
		return translateFindNodes(op, mocks[op])
	})
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
	allowed := map[string]bool{"ResolveUrn": true, "GetNode": true, "GetNodeRaw": true, "GetSpecNodeRaw": true, "FindNodes": true, "NodeBatch": true, "SpecCorpusState": true, "GetMemory": true, "Memories": true, "AuthContext": true, "EffectiveAccess": true}
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
