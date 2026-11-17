package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestC5SkillLintConsumesOnlyProviderBrokenIncludeWarning(t *testing.T) {
	good := skillNode(nodeID, "mem1", "hrn:node:hadronmemory.com:core:tasks:x", "tasks:x", true, `{"exports":{"claudeSkill":{"name":"hadron-x","description":"Use when testing.","enable":true}}}`, `"Body with a broken include."`)
	plan := skillLintPlan(skillLintEntry(nodeID, skillLintFinding("hrn:node:hadronmemory.com:core:tasks:x", "hrn:mem:hadronmemory.com:core", "skill-include-broken", "warning", "Provider says one include could not be resolved")))
	responses := map[string]string{"GetMemory": skillMemOrg, "FindNodes": listOf(nodeID), "NodeBatch": batchOf(good), "SkillLintPlan": plan}
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
	for _, plan := range []string{skillLintPlan(skillLintEntry(nodeID)), skillLintPlan(
		skillLintEntry(nodeID),
		skillLintEntry("other-id", skillLintFinding("concealed-other-urn", "mem1", "skill-include-broken", "warning", "unselected sentinel")))} {
		responses := map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batchOf(good), "SkillLintPlan": plan}
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
		out, err := runSkillLint(t, map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batchOf(good), "SkillLintPlan": plan}, "--node", nodeID, "--json")
		if err == nil || strings.TrimSpace(out) == "[]" {
			t.Fatalf("provider failure forged clean: %v %s", err, out)
		}
	}
}

func TestC5HumanOutputAndProviderFailureNeverClaimPartialLint(t *testing.T) {
	good := skillNode(nodeID, "mem1", "hrn:node:hadronmemory.com:core:tasks:x", "tasks:x", false, `{"exports":{"claudeSkill":{"name":"hadron-x","description":"Use when testing."}}}`, `"Body."`)
	warning := skillLintPlan(skillLintEntry(nodeID,
		skillLintFinding("source1", "mem1", "skill-not-runnable", "error", "server runnable check"),
		skillLintFinding("source1", "mem1", "skill-include-broken", "warning", "include check warning")))
	out, err := runSkillLint(t, map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batchOf(good), "SkillLintPlan": warning}, "--node", nodeID)
	if exitCodeFor(err) != 5 || !strings.Contains(out, "skill-not-runnable") || !strings.Contains(out, "include check warning") || !strings.Contains(out, "finding(s)") {
		t.Fatalf("server human findings lost: %v %s", err, out)
	}
	for _, plan := range []string{gqlErrorJSON("FORBIDDEN"), `{"errors":[{"message":"Cannot query field skillPlan","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, `{"data":{"skillPlan":{"entries":[],"unrecognized":null}}}`, `{"data":{"skillPlan":{"entries":[null],"unrecognized":[]}}}`, `{"data":{"skillPlan":{"entries":[{"nodeId":"` + nodeID + `","findings":null}],"unrecognized":[]}}}`} {
		out, err := runSkillLint(t, map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batchOf(good), "SkillLintPlan": plan}, "--node", nodeID, "--json")
		if err == nil || out != "" {
			t.Fatalf("incomplete lint printed usable partial result: %v %s", err, out)
		}
	}
}

// cli#1171: the fixture helper must not write into its caller's map — a
// shared fixture reused across calls would otherwise silently gain a plan.
func TestRunSkillLintDoesNotMutateCallerFixture(t *testing.T) {
	good := skillNode(nodeID, "mem1", "hrn:node:hadronmemory.com:core:tasks:x", "tasks:x", true, `{"exports":{"claudeSkill":{"name":"hadron-x","description":"Use when testing."}}}`, `"Body."`)
	responses := map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batchOf(good)}
	if _, err := runSkillLint(t, responses, "--node", nodeID, "--json"); err != nil {
		t.Fatal(err)
	}
	if _, ok := responses["SkillLintPlan"]; ok {
		t.Fatalf("runSkillLint injected SkillLintPlan into the caller's map: %v", responses)
	}
}

// cli#1171: hosted findings keep the server's entry/finding order, followed by
// unavailable refs; pin that at the command level so a sort or reorder is a
// visible decision, not drift.
func TestC5ProviderRowsKeepServerOrderBeforeUnavailable(t *testing.T) {
	const id2 = "01a0a5ba59a377d2a01a8ea32ae98196"
	bad := skillNode(nodeID, "mem1", "hrn:node:hadronmemory.com:core:tasks:x", "tasks:x", true, `{"exports":{"claudeSkill":{"name":"Bad_Name","description":"Use when testing."}}}`, `"Body."`)
	ok := skillNode(id2, "mem1", "hrn:node:hadronmemory.com:core:tasks:y", "tasks:y", true, `{"exports":{"claudeSkill":{"name":"hadron-y","description":"Use when testing."}}}`, `"Body."`)
	batch := `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":["gone-node"],"nodes":[` + bad + `,` + ok + `]}}}`
	// The provider answers id2 BEFORE nodeID, and "zzz" before "aaa": neither
	// order is alphabetical nor the selection order. The error on id2 also
	// proves server findings retain their severity and exit behavior.
	plan := skillLintPlan(
		skillLintEntry(id2,
			skillLintFinding("urn-y", "mem1", "skill-name-invalid", "error", "server invalid name"),
			skillLintFinding("urn-y", "mem1", "skill-include-broken", "warning", "zzz second include")),
		skillLintEntry(nodeID, skillLintFinding("urn-x", "mem1", "skill-include-broken", "warning", "aaa first include")))
	out, err := runSkillLint(t, map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batch, "SkillLintPlan": plan}, "--node", nodeID, "--node", id2, "--json")
	if exitCodeFor(err) != 5 {
		t.Fatalf("want exit 5: %v\n%s", err, out)
	}
	var got []string
	for _, r := range lintRows(t, out) {
		got = append(got, r.Rule+"|"+r.Message)
	}
	rules := strings.Join(got, "\n")
	iServer := strings.Index(rules, "skill-name-invalid")
	iZ := strings.Index(rules, "zzz second include")
	iA := strings.Index(rules, "aaa first include")
	iGone := strings.Index(rules, "skill-node-unavailable")
	if iServer < 0 || iZ < 0 || iA < 0 || iGone < 0 || iServer >= iZ || iZ >= iA || iA >= iGone {
		t.Fatalf("row order not server entry/finding order < unavailable:\n%s", rules)
	}
}

// cli#1171: --all hands the provider the memory ids of the selected nodes (here
// one of two listed memories holds a node), never every listed memory.
func TestC5SkillLintAllScopesProviderPlanToTheSelectedNodesMemories(t *testing.T) {
	memory := func(id, urn string) string {
		return `{"id":"` + id + `","urn":"` + urn + `","name":"` + id + `","shortDescription":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org1","organization":{"skillPrefix":"hadron-"},"isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-06-11T00:00:00Z"}`
	}
	good := skillNode(nodeID, "mem1", "hrn:node:hadronmemory.com:core:tasks:x", "tasks:x", true, `{"exports":{"claudeSkill":{"name":"hadron-x","description":"Use when testing."}}}`, `"Body."`)
	gql, captured := captureGraphQL(t, map[string]string{
		"Memories":             `{"data":{"memories":{"total":2,"items":[` + memory("mem1", "hrn:mem:hadronmemory.com:core") + `,` + memory("mem2", "hrn:mem:hadronmemory.com:other") + `]}}}`,
		"MemoriesSharedWithMe": `{"data":{"memories":{"total":0,"items":[]}}}`,
		"FindNodes":            listOf(nodeID),
		"NodeBatch":            batchOf(good),
		"SkillLintPlan":        skillLintPlan(skillLintEntry(nodeID)),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"skill", "lint", "--all", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("--all: %v", err)
	}
	var vars struct {
		Input struct {
			Intent   string
			Memories []string
		}
	}
	if err := json.Unmarshal(captured["SkillLintPlan"], &vars); err != nil {
		t.Fatalf("SkillLintPlan variables: %v %s", err, captured["SkillLintPlan"])
	}
	if vars.Input.Intent != "LINT" || len(vars.Input.Memories) == 0 {
		t.Fatalf("--all sent no provider scope: %s", captured["SkillLintPlan"])
	}
	// Scope is by memory id and only the memories the selected nodes live in.
	if got := strings.Join(vars.Input.Memories, ","); got != "mem1" {
		t.Fatalf("provider scope = %q, want mem1 (the memory holding the selected node)", got)
	}
}
