package cmd

import (
	"encoding/json"
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

// Detail values render through output.SafeText (cli#1110): same rune
// neutralization as SafeCell, but NO length cut — a detail view is the human
// way to read long server text (a system prompt), so cutting it there loses
// information (Joshua P2-1).
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
	assertSafeTableText(t, text, -1, 0)
	assertDetailLineCount(t, text, 12)
	if !strings.Contains(text, longCellText) {
		t.Errorf("the long URN must render in full in the detail view (SafeText does not cut):\n%s", text)
	}
}

// A hostile value carrying ONLY a newline is invisible to the control-rune
// scan; only the pinned line count and the forged tail's absence catch it
// (cli#1110, Joshua P2-2).
func TestAgentGetDetailNeutralizesNewlineOnlyForgery(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetAgent": `{"data":{"agent":` + agentSanitizeJSON("Support\nForged: yes", "acme.com::support") + `}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "get", "acme.com::support-bot", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertDetailLineCount(t, text, 12)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "Forged:") {
			t.Fatalf("a newline-only hostile value forged a detail line:\n%s", text)
		}
	}
}

func TestAgentGetJSONKeepsTheOriginals(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetAgent": `{"data":{"agent":` + agentSanitizeJSON(hostileCellLabel, longCellText) + `}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "get", "acme.com::support-bot", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var agent map[string]any
	if err := json.Unmarshal([]byte(out.String()), &agent); err != nil {
		t.Fatal(err)
	}
	if agent["name"] != hostileCellLabel || agent["urn"] != longCellText {
		t.Errorf("--json must round-trip the original values, got %v", agent)
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
