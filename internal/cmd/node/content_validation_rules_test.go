package node

import (
	"reflect"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestEffectiveValidationRules1706(t *testing.T) {
	rule := func(role string, enabled, strict bool, state gen.NodeRoleRuleRefState) *validationRule {
		return &validationRule{Role: role, Enabled: enabled, StrictSubRoles: strict, ValidationTaskState: state}
	}
	ok := gen.NodeRoleRuleRefStateOk
	none := gen.NodeRoleRuleRefStateNone
	for _, tc := range []struct {
		name, role string
		runnable   bool
		rules      []*validationRule
		want       []string
	}{
		{"ancestor", "spec.rule.detail", false, []*validationRule{rule("spec", true, false, ok), rule("spec.rule", true, false, ok)}, []string{"spec.rule"}},
		{"disabled-exact", "spec.rule", false, []*validationRule{rule("spec", true, false, ok), rule("spec.rule", false, false, ok)}, []string{"spec"}},
		{"strict-undeclared", "spec.rule", false, []*validationRule{rule("spec", true, true, ok)}, []string{}},
		{"strict-declared-disabled", "spec.rule", false, []*validationRule{rule("spec", true, true, ok), rule("spec.rule", false, false, ok)}, []string{"spec"}},
		{"role-and-task", "spec.rule", true, []*validationRule{rule("spec", true, false, ok), rule("task", true, false, ok)}, []string{"spec", "task"}},
		{"task-once", "task", true, []*validationRule{rule("task", true, false, ok)}, []string{"task"}},
		{"role-none-no-parent-fallback", "spec.rule", false, []*validationRule{rule("spec", true, false, ok), rule("spec.rule", true, false, none)}, []string{}},
		{"broken-remains", "spec", false, []*validationRule{rule("spec", true, false, gen.NodeRoleRuleRefStateBroken)}, []string{"spec"}},
		{"unreadable-remains", "spec", false, []*validationRule{rule("spec", true, false, gen.NodeRoleRuleRefStateUnreadable)}, []string{"spec"}},
		{"no-rules", "", false, []*validationRule{}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := []string{}
			for _, r := range effectiveValidationRules(&tc.role, tc.runnable, tc.rules) {
				got = append(got, r.Role)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got%v want%v", got, tc.want)
			}
		})
	}
}
