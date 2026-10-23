package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestAgentOrigin1001Selection(t *testing.T) {
	if !strings.Contains(gen.GetAgent_Operation, "clonedFromAgentRef") {
		t.Fatal("GetAgent must select origin")
	}
	for op, doc := range map[string]string{"Agents": gen.Agents_Operation, "CloneAgent": gen.CloneAgent_Operation, "CreateAgent": gen.CreateAgent_Operation} {
		if strings.Contains(doc, "clonedFromAgentRef") {
			t.Fatalf("%s selection inflated", op)
		}
	}
}

func TestAgentOrigin1001(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		want        any
	}{
		{"current origin", `,"clonedFromAgentRef":"hrn:agent:source.test:current"`, "hrn:agent:source.test:current"},
		{"concealed", `,"clonedFromAgentRef":null`, nil},
		{"omitted", "", nil},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: " JSON", false: " text"}[asJSON], func(t *testing.T) {
				fixture := strings.TrimSuffix(agentJSON, "}") + tc.field + "}"
				srv, ops := captureGraphQL(t, map[string]string{"GetAgent": `{"data":{"agent":` + fixture + `}}`})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"agent", "get", "unchanged-caller-ref", "--server", srv.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if len(ops) != 1 {
					t.Fatalf("extra lookup/mutation: %v", ops)
				}
				var vars map[string]any
				_ = json.Unmarshal(ops["GetAgent"], &vars)
				if vars["ref"] != "unchanged-caller-ref" {
					t.Fatal(vars)
				}
				if asJSON {
					var got map[string]any
					_ = json.Unmarshal([]byte(out.String()), &got)
					v, ok := got["clonedFromAgentRef"]
					if !ok || v != tc.want {
						t.Fatalf("origin key/value %v %v", ok, v)
					}
				} else if tc.want != nil {
					if !strings.Contains(out.String(), "Cloned from: "+tc.want.(string)) {
						t.Fatal(out.String())
					}
				} else if strings.Contains(out.String(), "Cloned from:") || strings.Contains(out.String(), "unknown") || strings.Contains(out.String(), "deleted") {
					t.Fatal(out.String())
				}
			})
		}
	}
}
