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
	strict, err := runSkillLint(t, responses, "--node", nodeID, "--strict", "--json")
	if exitCodeFor(err) != 5 || findingRules(t, strict)["skill-include-broken"] != "error" {
		t.Fatalf("strict contract lost: %v %s", err, strict)
	}
}

func TestC5SkillLintNoNoticeHeuristicAndNoWidenedNodeSelection(t *testing.T) {
	good := skillNode(nodeID, "mem1", "hrn:node:hadronmemory.com:core:tasks:x", "tasks:x", true, `{"exports":{"claudeSkill":{"name":"hadron-x","description":"Use when testing.","enable":true}}}`, `"The literal text [include unavailable] is an author's example."`)
	for _, plan := range []string{emptyIncludeLintPlan, `{"data":{"skillPlan":{"scanned":2,"judged":2,"entries":[{"nodeId":"other-id","urn":"concealed-other-urn","findings":[{"rule":"skill-include-broken","severity":"warning","message":"unselected sentinel","urn":"concealed-other-urn","memory":"mem1"}]}],"orphans":[]}}}`} {
		responses := map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batchOf(good), "SkillPlan": plan}
		out, err := runSkillLint(t, responses, "--node", nodeID, "--json")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out) != "[]" {
			t.Fatalf("invented/widened warning: %s", out)
		}
	}
}

func TestC5ProviderLintErrorsNeverReportClean(t *testing.T) {
	good := skillNode(nodeID, "mem1", "hrn:node:hadronmemory.com:core:tasks:x", "tasks:x", true, `{"exports":{"claudeSkill":{"name":"hadron-x","description":"Use when testing."}}}`, `"Body."`)
	for _, plan := range []string{`{"data":{"skillPlan":null}}`, `{"data":{"skillPlan":{"entries":null}}}`, gqlErrorJSON("FORBIDDEN")} {
		out, err := runSkillLint(t, map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batchOf(good), "SkillPlan": plan}, "--node", nodeID, "--json")
		if err == nil || strings.TrimSpace(out) == "[]" {
			t.Fatalf("provider failure forged clean: %v %s", err, out)
		}
	}
}
