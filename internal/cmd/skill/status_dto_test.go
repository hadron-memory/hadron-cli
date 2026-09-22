package skill

import (
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
func TestStatusDTOHasNoNilSlices(t *testing.T) {
	check := func(t *testing.T, name string, dto statusDTO) {
		t.Helper()
		v := reflect.ValueOf(dto)
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.Type.Kind() != reflect.Slice {
				continue
			}
			if v.Field(i).IsNil() {
				t.Errorf("%s: field %s is a nil slice — --json renders it as null, not []", name, f.Name)
			}
		}
	}
	// The server-backed path, with an otherwise empty plan.
	check(t, "toStatusDTO", toStatusDTO("/root", "claudeSkill",
		&gen.SkillPlanSkillPlan{}, []statusUnreadableDTO{}, []statusUnreadableDTO{}))
	// The empty-scope path.
	check(t, "emptyStatusDTO", emptyStatusDTO("/root", "claudeSkill", nil,
		[]statusUnreadableDTO{}, []statusUnreadableDTO{}, []statusExcludedDTO{}))
}
