package cmd

import (
	"encoding/json"
	"testing"
)

// cli#968: the host's collect sends an EXPLICIT first of 50 — today's effective
// batch (hadron-server resolvers.hostOperation.ts: omitted first defaults to 50,
// cap 200) — so a server default change cannot silently shrink a host's page.
func TestTeamHostCollectSendsAnExplicitFirstOf50(t *testing.T) {
	cfg := writeHostConfig(t, "")
	srv, calls := hostServerFake(t, map[string]string{"RegisterHost": hostRegistrationAnswer, "CollectHostOperations": noOps})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("run --once: %v", err)
	}
	collects := 0
	for _, c := range calls() {
		if c.op != "CollectHostOperations" {
			continue
		}
		collects++
		var vars map[string]json.RawMessage
		if err := json.Unmarshal(c.vars, &vars); err != nil {
			t.Fatal(err)
		}
		if got := string(vars["first"]); got != "50" {
			t.Errorf("collect sent first=%s (vars %s), want an explicit 50", got, c.vars)
		}
	}
	if collects != 1 {
		t.Errorf("one Step must send exactly one collect, got %d", collects)
	}
}
