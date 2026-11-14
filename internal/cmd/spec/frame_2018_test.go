package spec

import "testing"

func TestLintFrameBasics2018(t *testing.T) {
	role := "spec.rule"
	body := "# Rule Spec: A rule\n\n- **Status:** Proposed\n"
	abs := "A rule."
	n := specNode{Loc: "cor:acl:140:04", NodeType: "info", Name: "Rule: A rule", Role: &role, Tags: []string{"spec"}, Content: &body, Abstract: &abs}
	fs := lintNode(n, "hadronmemory.com::specs")
	for _, rule := range []string{"frame-name", "frame-tags"} {
		if !hasRule(fs, rule) {
			t.Errorf("missing %s: %+v", rule, fs)
		}
	}
}
