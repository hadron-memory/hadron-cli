package cmd

import (
	"strings"
	"testing"
)

func TestC5SkillLintConsumesOnlyProviderBrokenIncludeWarning(t *testing.T) {
	good := skillNode(nodeID, "mem1", "hrn:node:hadronmemory.com:core:tasks:x", "tasks:x", true, `{"exports":{"claudeSkill":{"name":"hadron-x","description":"Use when testing.","enable":true}}}`, `"Body with a broken include."`)
	responses := map[string]string{"GetMemory": skillMemOrg, "FindNodes": listOf(nodeID), "NodeBatch": batchOf(good), "SkillPlan": `{"data":{"skillPlan":{"scanned":1,"judged":1,"entries":[{"nodeId":"` + nodeID + `","urn":"hrn:node:hadronmemory.com:core:tasks:x","findings":[{"rule":"skill-include-broken","severity":"warning","message":"Provider says one include could not be resolved","urn":"hrn:node:hadronmemory.com:core:tasks:x","memory":"hrn:mem:hadronmemory.com:core"}]}],"orphans":[]}}}`}
	out, err := runSkillLint(t, responses, "--node", nodeID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if findingRules(t, out)["skill-include-broken"] != "warning" || !strings.Contains(out, "Provider says one include could not be resolved") {
		t.Fatalf("provider warning lost: %s", out)
	}
}
