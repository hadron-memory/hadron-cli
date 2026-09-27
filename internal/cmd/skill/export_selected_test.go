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

func TestSelectedNodeIdentityKey(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"hrn:node:example.com:demo:tasks:demo", "hrn:node:example.com:demo:tasks:demo"},
		{"hrn:node:example.com::demo::tasks:demo", "hrn:node:example.com:demo:tasks:demo"},
		{"hrn:node:example.com::agent:app-mem:slug::tasks:demo", "hrn:node:example.com:agent:app-mem:slug:tasks:demo"},
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
