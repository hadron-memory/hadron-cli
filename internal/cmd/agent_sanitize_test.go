package cmd

import (
	"strings"
	"testing"
)

// cli#1104: the agent command's tables and `agent get` detail view render
// server-supplied cells; text sanitizes them, --json keeps the originals.

func agentSanitizeJSON(name, urn string) string {
	base := strings.Replace(agentJSON, `"name":"Support Bot"`, `"name":`+jsonText(name), 1)
	base = strings.Replace(base, `"urn":"acme.com::support-bot"`, `"urn":`+jsonText(urn), 1)
	return base
}

func TestAgentLsSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Agents": `{"data":{"agents":{"total":2,"items":[` +
			agentSanitizeJSON(hostileCellLabel, "acme.com::support") + `,` +
			agentSanitizeJSON("Eng", longCellText) + `]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "ls", "--org", "acme.com", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertSafeTableText(t, out.String(), 2, 1)
}

func TestAgentGetSanitizesDetailValues(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetAgent": `{"data":{"agent":` + agentSanitizeJSON(hostileCellLabel, longCellText) + `}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "get", "acme.com::support-bot", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertSafeTableText(t, text, -1, 1)
	// the detail view keeps one line per field: none of them may be forged
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.Contains(line, "\x1b") {
			t.Fatalf("an escape survived on line %q", line)
		}
	}
}

func listedNodeSanitizeJSON(name, urn string) string {
	base := strings.Replace(listedNodeJSON, `"name":"Onboarding"`, `"name":`+jsonText(name), 1)
	base = strings.Replace(base, `"urn":"hrn:node:acme.com:base-mem:tasks:onboarding"`, `"urn":`+jsonText(urn), 1)
	return base
}

func TestAgentTasksListSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"AgentTaskIndexList": `{"data":{"agentTaskList":[
			{"node":` + listedNodeSanitizeJSON(hostileCellLabel, "hrn:node:acme.com:base-mem:tasks:x") + `,"trigger":` + jsonText(hostileCellLabel) + `},
			{"node":` + listedNodeSanitizeJSON("Escalate", longCellText) + `,"trigger":null}]}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "tasks", "list", "hrn:agent:acme.com:support", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertSafeTableText(t, out.String(), 2, 1)
}

func TestAgentProcessesListSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"AgentProcessIndexList": `{"data":{"agentProcessList":[
			{"node":` + listedNodeSanitizeJSON(hostileCellLabel, "hrn:node:acme.com:base-mem:tasks:x") + `,"kind":"STARTS","trigger":` + jsonText(hostileCellLabel) + `},
			{"node":` + listedNodeSanitizeJSON("Escalate", longCellText) + `,"kind":"INVOLVED","trigger":null}]}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "processes", "list", "hrn:agent:acme.com:support", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertSafeTableText(t, out.String(), 2, 1)
}

func TestAgentSubscriptionLsSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"OrgAgentGrants": `{"data":{"organization":{"agentOrgGrants":[
			{"agentId":` + jsonText(hostileCellLabel) + `,"orgId":"org1","isActive":true,
			  "createdAt":` + jsonText(longCellText) + `,"activatedAt":"2026-10-01T00:00:00Z","expiresAt":null,"revokedAt":null},
			{"agentId":"agt2","orgId":"org1","isActive":false,
			  "createdAt":"2026-09-01T00:00:00Z","activatedAt":null,"expiresAt":null,"revokedAt":null}]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "subscription", "ls", "--org", "org1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertSafeTableText(t, out.String(), 2, 1)
}
