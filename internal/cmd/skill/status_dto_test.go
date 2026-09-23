package skill

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// Both @codex and @copilot found `unchecked` serializing as null on the
// server-backed path and [] on the empty-scope path — the same DTO changing
// shape by control path, which an agent iterating it cannot survive.
//
// Pinned as a CLASS rather than as that one field: every slice in statusDTO,
// on BOTH constructors, reflectively. A test naming `Unchecked` would pass
// again the next time a field is added and missed, which is exactly how this
// one arrived.
//
// NESTED slices too (#671, Copilot): lint's finding DTO gained `hosts`, and
// status reuses that type, so every status finding emitted `"hosts": null`.
// The top-level walk could not see it, and the empty plan it was fed had no
// finding to carry the field. The walk now recurses into every struct and
// slice element, and one input carries an entry with a finding.
func TestStatusDTOHasNoNilSlices(t *testing.T) {
	var walk func(t *testing.T, name string, v reflect.Value)
	walk = func(t *testing.T, name string, v reflect.Value) {
		t.Helper()
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(t, name+"."+v.Type().Field(i).Name, v.Field(i))
			}
		case reflect.Slice:
			if v.IsNil() {
				t.Errorf("%s is a nil slice — --json renders it as null, not []", name)
				return
			}
			for i := 0; i < v.Len(); i++ {
				walk(t, fmt.Sprintf("%s[%d]", name, i), v.Index(i))
			}
		}
	}
	check := func(t *testing.T, name string, dto statusDTO) {
		t.Helper()
		walk(t, name, reflect.ValueOf(dto))
	}
	// The server-backed path, with an otherwise empty plan.
	check(t, "toStatusDTO", toStatusDTO("/root", "claudeSkill",
		&gen.SkillPlanSkillPlan{}, []statusUnreadableDTO{}, []statusUnreadableDTO{}))
	// The server-backed path with an entry carrying a finding: the nested
	// slices only exist once there is something to nest.
	class := "stale"
	withFinding := &gen.SkillPlanSkillPlan{Entries: []*gen.SkillPlanSkillPlanEntriesSkillPlanEntry{{
		Urn: "hrn:node:example.com:demo:tasks:a", NodeId: "n1", Name: "hadron-a", Class: &class,
		Findings: []*gen.SkillPlanSkillPlanEntriesSkillPlanEntryFindingsSkillFinding{{
			Urn: "hrn:node:example.com:demo:tasks:a", Memory: "hrn:mem:example.com:demo",
			Rule: "skill-description-no-trigger", Severity: "warning", Message: "m",
		}},
	}}}
	dto := toStatusDTO("/root", "codexSkill", withFinding, []statusUnreadableDTO{}, []statusUnreadableDTO{})
	check(t, "toStatusDTO(with a finding)", dto)
	if got := dto.Entries[0].Findings[0].Hosts; !reflect.DeepEqual(got, []string{"codexSkill"}) {
		t.Errorf("a status finding's hosts = %v, want the one host the plan judged", got)
	}
	// The empty-scope path.
	check(t, "emptyStatusDTO", emptyStatusDTO("/root", "claudeSkill", nil,
		[]statusUnreadableDTO{}, []statusUnreadableDTO{}))
}
