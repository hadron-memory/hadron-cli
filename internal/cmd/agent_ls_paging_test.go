package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func agentRows(n int) []string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = strings.Replace(agentJSON, `"id":"agt1"`, fmt.Sprintf(`"id":"agt%d"`, i), 1)
	}
	return rows
}

func runAgentLs(t *testing.T, serverURL string, args ...string) ([]struct {
	ID string `json:"id"`
}, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"agent", "ls", "--json", "--server", serverURL}, args...))
	err := root.Execute()
	var agents []struct {
		ID string `json:"id"`
	}
	if err == nil {
		if jerr := json.Unmarshal([]byte(out.String()), &agents); jerr != nil {
			t.Fatalf("not a JSON array: %v\n%s", jerr, out.String())
		}
	}
	return agents, err
}

// cli#967: with no --limit/--offset the list is complete, even when the
// server serves fewer rows per page than asked for.
func TestAgentLsDrainsPastAClampedPage(t *testing.T) {
	for name, tc := range map[string]struct {
		op, field string
		args      []string
	}{
		"member": {"Agents", "agents", nil},
		"public": {"PublicAgents", "publicAgents", []string{"--public"}},
	} {
		t.Run(name, func(t *testing.T) {
			gql, rec := pagedGraphQL(t, map[string]*pagedSource{
				tc.op: {rows: agentRows(45), serve: 20, wrap: itemsEnvelope(tc.field)},
			}, nil)
			agents, err := runAgentLs(t, gql.URL, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if len(agents) != 45 {
				t.Fatalf("want 45 agents, got %d (silent truncation)", len(agents))
			}
			for i, a := range agents {
				if a.ID != fmt.Sprintf("agt%d", i) {
					t.Fatalf("row %d is %s: order lost or duplicated", i, a.ID)
				}
			}
			if n := len(rec[tc.op].Calls); n != 3 {
				t.Errorf("want 3 page requests (20+20+5), got %d", n)
			}
		})
	}
}

func TestAgentLsExplicitLimitOrOffsetIsOneRequestAsAsked(t *testing.T) {
	for _, args := range [][]string{{"--limit", "5"}, {"--offset", "3"}, {"--limit", "5", "--offset", "2"}} {
		gql, rec := pagedGraphQL(t, map[string]*pagedSource{
			"Agents": {rows: agentRows(45), serve: 20, wrap: itemsEnvelope("agents")},
		}, nil)
		agents, err := runAgentLs(t, gql.URL, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(rec["Agents"].Calls) != 1 {
			t.Errorf("%v: want exactly one request, got %d", args, len(rec["Agents"].Calls))
		}
		if len(agents) == 0 || len(agents) > 20 {
			t.Errorf("%v: unexpected page of %d", args, len(agents))
		}
		if v, ok := rec["Agents"].Calls[0]["limit"]; ok && args[0] == "--limit" && v != float64(5) {
			t.Errorf("%v: limit sent as %v", args, v)
		}
	}
}

func TestAgentLsLaterPageErrorFailsTheListing(t *testing.T) {
	gql, _ := pagedGraphQL(t, map[string]*pagedSource{
		"Agents": {rows: agentRows(45), serve: 20, wrap: itemsEnvelope("agents"), failAtCall: 2},
	}, nil)
	agents, err := runAgentLs(t, gql.URL)
	if err == nil {
		t.Fatalf("a failed page 2 must not read as a complete list (got %d agents)", len(agents))
	}
}
