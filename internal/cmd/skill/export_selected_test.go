package skill

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestSelectedExportQueryRequestsScopedPlan(t *testing.T) {
	q := gen.SelectedSkillFilePlan_Operation
	for _, field := range []string{"selectedSkillFilePlan(input: $input)", "orphanAssessmentSkipped", "selectionResults", "renderedBody"} {
		if !strings.Contains(q, field) {
			t.Errorf("selected export query does not request %s", field)
		}
	}
	if strings.Contains(q, "\n  skillPlan(input:") {
		t.Fatal("selected export query called the unscoped planner")
	}
}

func TestSkillPlanQueriesSelectOutOfExportReason(t *testing.T) {
	for name, operation := range map[string]string{
		"status":          gen.SkillPlan_Operation,
		"export":          gen.SkillExportPlan_Operation,
		"selected export": gen.SelectedSkillFilePlan_Operation,
	} {
		count := 0
		for _, line := range strings.Split(operation, "\n") {
			if strings.TrimSpace(line) == "outOfExportReason" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("%s selects outOfExportReason %d times, want once", name, count)
		}
	}
}

func TestSelectedExportKeepsServerOutOfExportReason(t *testing.T) {
	reason := "render failed: source could not be rendered"
	plan := &selectedPlan{Entries: []*gen.SelectedSkillFilePlanSelectedSkillFilePlanSkillPlanEntriesSkillPlanEntry{{
		Urn: "hrn:node:example.com:demo:tasks:a", NodeId: "n1", Name: "a", OutOfExportReason: &reason,
	}}}
	got := outOfExportItems("claudeSkill", adaptSelectedPlan(plan))
	if len(got) != 1 || got[0].Reason != reason || got[0].Node != plan.Entries[0].Urn {
		t.Fatalf("selected export lost the server reason: %+v", got)
	}
}

func TestSelectedNodeIdentityKey(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"hrn:node:example.com:demo:tasks:demo", "hrn:node:example.com:demo:tasks:demo"},
		{"hrn:node:example.com::demo::tasks:demo", "hrn:node:example.com:demo:tasks:demo"},
		{"hrn:node:example.com::agent:app-mem:slug::tasks:demo", "hrn:node:example.com:agent:app-mem:slug:tasks:demo"},
		{"hrn:node:@holger:inbox:tasks:demo", "hrn:node:holger:inbox:tasks:demo"},
	} {
		got, err := nodeIdentityKey(tc.ref)
		if err != nil || got != tc.want {
			t.Errorf("nodeIdentityKey(%q) = %q, %v; want %q", tc.ref, got, err, tc.want)
		}
	}
	if _, err := nodeIdentityKey("hrn:node:example.com:demo:tasks:demo#data"); err == nil {
		t.Fatal("a node-data fragment must not name a skill source")
	}
}
