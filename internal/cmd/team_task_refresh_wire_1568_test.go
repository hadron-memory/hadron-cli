package cmd

// hadron-cli#1568 (Andi LOCAL P2): the snapshot refresh from hadron-server
// d7ae518d added CreateAgentTaskInput.match and AgentTaskRequirementsInput.roles/
// .team (server#2829/#2830, tracked by #1549, not built here). An unnamed field
// must stay ABSENT: an always-null key is rejected by a provider whose schema
// predates the field, and an ordinary create must not change on the wire.

import (
	"strings"
	"testing"
)

func TestTaskCreateOmitsRefreshFieldsUnlessNamed1568(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"plain create", []string{"team", "task", "create", "T"}},
		{"create with requirements", []string{"team", "task", "create", "T", "--require-role", "qa-engineer", "--family-not-in", "codex"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskResponses(), tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			creates := 0
			for _, c := range *calls {
				if c.Op != "AgentTaskCreate" {
					continue
				}
				creates++
				for _, key := range []string{`"match"`, `"roles"`, `"team"`} {
					if strings.Contains(string(c.RawVars), key) {
						t.Errorf("task create sent %s although no flag names it: %s", key, c.RawVars)
					}
				}
			}
			if creates != 1 {
				t.Fatalf("AgentTaskCreate requests = %d, want exactly 1", creates)
			}
		})
	}
}
