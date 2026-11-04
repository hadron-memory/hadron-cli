package cmd

import (
	"strings"
	"testing"
)

func TestC6RetiredNameProbeNoQueryNoAdviceStableNull(t *testing.T) {
	responses := map[string]string{
		"TeamRoles": teamRolesJSON, "TeamAppIdentity": teamAppIdentityJSON, "AppAgentRoster": engTeamRosterJSON,
		"CastWorkerPreview": `{"data":{"castWorkerPreview":{"name":"Joe","role":"backend-engineer","agentId":"agt1","agentName":"backend-engineer","prompt":"You are Joe.","hasNamePlaceholder":false}}}`,
	}
	for _, args := range [][]string{
		{"team", "role", "list"},
		{"team", "role", "get", "backend-engineer"},
		{"team", "worker", "cast", "--dry-run", "--role", "backend-engineer", "--name", "Joe"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			args = append(args, "--app", "hrn:app:acme.com:eng-team")
			calls, out, err := runTeamTask(t, responses, append(args, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"hasNamePlaceholder": null`) || strings.Contains(out, `"hasNamePlaceholder": true`) || strings.Contains(out, `"hasNamePlaceholder": false`) {
				t.Fatalf("retired key must remain null: %s", out)
			}
			for _, c := range *calls {
				if strings.Contains(c.Query, "hasNamePlaceholder") || strings.Contains(c.Query, "mutation ") {
					t.Fatalf("retired probe or mutation sent: %s", c.Query)
				}
			}
			_, human, err := runTeamTask(t, responses, args...)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(human, "nameless") || strings.Contains(human, "never binds {{name}}") {
				t.Fatalf("obsolete probe advice: %s", human)
			}
		})
	}
}
