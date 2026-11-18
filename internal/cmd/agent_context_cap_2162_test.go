package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// hadron-cli#2162 (S4b of #4229, server#4258): agent create and update set and
// clear Agent.contextCap, and agent get shows it. The field is selected ONLY by
// documents of its own, so a server older than #4258 keeps every other agent
// call working (the split-selection rule).

func agentWithCap2162(cap string) string {
	return strings.Replace(agentJSON, `"usesTasks":true,`, `"usesTasks":true,"contextCap":`+cap+`,`, 1)
}

func TestAgentCreateUpdateSetTheContextCap2162(t *testing.T) {
	for name, tc := range map[string]struct {
		op, field string
		argv      []string
	}{
		"create": {"CreateAgentWithContextCap", "createAgent", []string{"agent", "create", "--name", "Engineer", "--context-cap", "50000"}},
		"update": {"UpdateAgentWithContextCap", "updateAgent", []string{"agent", "update", "agt1", "--context-cap", "50000"}},
	} {
		t.Run(name, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + agentWithCap2162("50000") + `}}`}, tc.argv...)
			if err != nil {
				t.Fatal(err)
			}
			c := findCall(t, calls, tc.op)
			if c.Vars["contextCap"] != float64(50000) {
				t.Fatalf("contextCap on the wire: %s", c.RawVars)
			}
			if !strings.Contains(out, "  context cap: 50000 tokens\n") {
				t.Fatalf("the receipt must show the returned cap:\n%s", out)
			}
		})
	}
}

func TestAgentUpdateClearsTheContextCapWithAnExplicitNull2162(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"UpdateAgentWithContextCap": `{"data":{"updateAgent":` + agentWithCap2162("null") + `}}`},
		"agent", "update", "agt1", "--no-context-cap")
	if err != nil {
		t.Fatal(err)
	}
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(findCall(t, calls, "UpdateAgentWithContextCap").RawVars, &vars); err != nil {
		t.Fatal(err)
	}
	if v, ok := vars["contextCap"]; !ok || string(v) != "null" {
		t.Fatalf("a clear must send an explicit null: %v", vars)
	}
	if !strings.Contains(out, "  context cap: none\n") {
		t.Fatalf("the receipt must say there is no cap:\n%s", out)
	}
}

// Without either flag the write is the old document and never mentions the cap.
func TestAgentWritesWithoutACapFlagKeepTheOldDocuments2162(t *testing.T) {
	for name, tc := range map[string]struct {
		op, field string
		argv      []string
	}{
		"create": {"CreateAgent", "createAgent", []string{"agent", "create", "--name", "Engineer"}},
		"update": {"UpdateAgent", "updateAgent", []string{"agent", "update", "agt1", "--name", "Renamed"}},
	} {
		t.Run(name, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + agentJSON + `}}`}, tc.argv...)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(findCall(t, calls, tc.op).RawVars), "contextCap") || strings.Contains(out, "context cap") {
				t.Fatalf("no cap flag: nothing about the cap:\n%s", out)
			}
		})
	}
}

func TestAgentContextCapFlagsAreRefusedLocally2162(t *testing.T) {
	for name, argv := range map[string][]string{
		"both":     {"agent", "update", "agt1", "--context-cap", "5", "--no-context-cap"},
		"zero":     {"agent", "update", "agt1", "--context-cap", "0"},
		"negative": {"agent", "update", "agt1", "--context-cap", "-3"},
		"create0":  {"agent", "create", "--name", "Engineer", "--context-cap", "0"},
	} {
		t.Run(name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, map[string]string{}, argv...)
			if err == nil {
				t.Fatal("must be refused")
			}
			if len(*calls) != 0 {
				t.Fatalf("nothing may be sent for a refused flag: %+v", *calls)
			}
		})
	}
}

// A lifecycle sent with a cap travels in the same document and is still reported.
func TestAgentUpdateWithLifecycleAndContextCap2162(t *testing.T) {
	body := strings.Replace(agentWithCap2162("9000"), `"usesTasks":true,`, `"usesTasks":true,"lifecycle":"LONG_LIVED",`, 1)
	calls, out, err := runTeamTask(t, map[string]string{"UpdateAgentWithContextCap": `{"data":{"updateAgent":` + body + `}}`},
		"agent", "update", "agt1", "--lifecycle", "LONG_LIVED", "--context-cap", "9000")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "UpdateAgentWithContextCap")
	if c.Vars["lifecycle"] != "LONG_LIVED" || c.Vars["contextCap"] != float64(9000) {
		t.Fatalf("both must be sent: %s", c.RawVars)
	}
	if !strings.Contains(out, "  lifecycle: LONG_LIVED\n") || !strings.Contains(out, "  context cap: 9000 tokens\n") {
		t.Fatalf("receipt:\n%s", out)
	}
}

func TestAgentGetShowsTheContextCap2162(t *testing.T) {
	for name, tc := range map[string]struct {
		resp, text, json string
	}{
		"set":  {`{"data":{"agent":{"id":"agt1","contextCap":50000}}}`, "  context cap: 50000 tokens\n", `"contextCap": 50000`},
		"none": {`{"data":{"agent":{"id":"agt1","contextCap":null}}}`, "  context cap: none\n", `"contextCap": null`},
		"old server": {`{"errors":[{"message":"Cannot query field \"contextCap\" on type \"Agent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
			"  context cap: unknown (server before #4258)\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := map[string]string{"GetAgent": `{"data":{"agent":` + agentJSON + `}}`, "GetAgentLifecycle": `{"data":{"agent":{"id":"agt1","lifecycle":"LONG_LIVED"}}}`, "GetAgentContextCap": tc.resp}
			_, out, err := runTeamTask(t, r, "agent", "get", "agt1")
			if err != nil || !strings.Contains(out, tc.text) {
				t.Fatalf("%v\n%s", err, out)
			}
			_, out, err = runTeamTask(t, r, "agent", "get", "agt1", "--json")
			if err != nil {
				t.Fatal(err)
			}
			if tc.json == "" {
				if strings.Contains(out, "contextCap") {
					t.Fatalf("an older server's get carries no contextCap key:\n%s", out)
				}
			} else if !strings.Contains(out, tc.json) {
				t.Fatalf("--json:\n%s", out)
			}
		})
	}
}

// Another refusal on the cap read is the answer, not a silent "unknown".
func TestAgentGetContextCapOtherRefusalsSurface2162(t *testing.T) {
	r := map[string]string{"GetAgent": `{"data":{"agent":` + agentJSON + `}}`, "GetAgentLifecycle": `{"data":{"agent":{"id":"agt1","lifecycle":"LONG_LIVED"}}}`,
		"GetAgentContextCap": `{"errors":[{"message":"no.","extensions":{"code":"FORBIDDEN"}}]}`}
	if _, _, err := runTeamTask(t, r, "agent", "get", "agt1"); err == nil {
		t.Fatal("a refusal other than the unknown field must surface")
	}
}

// Split selection: no document an older server must keep answering selects the field.
func TestNoSharedAgentDocumentSelectsTheContextCap2162(t *testing.T) {
	for name, q := range map[string]string{
		"GetAgent":                 gen.GetAgent_Operation,
		"CreateAgent":              gen.CreateAgent_Operation,
		"UpdateAgent":              gen.UpdateAgent_Operation,
		"CreateAgentWithLifecycle": gen.CreateAgentWithLifecycle_Operation,
		"UpdateAgentWithLifecycle": gen.UpdateAgentWithLifecycle_Operation,
		"GetAgentLifecycle":        gen.GetAgentLifecycle_Operation,
	} {
		if strings.Contains(q, "contextCap") {
			t.Errorf("%s mentions contextCap; a server older than #4258 would reject it", name)
		}
	}
}
