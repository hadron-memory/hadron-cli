package spec

import (
	"strings"
	"testing"
)

func TestTagOnlyLint1658(t *testing.T) {
	for _, role := range []string{"", "special", "specification", "other"} {
		n := specNode{Loc: "msg:010:02", Tags: []string{"spec"}, Role: &role, NodeType: "task"}
		fs := lintReadNode(n, lintMem)
		if len(fs) != 1 || fs[0].Rule != "not-governed" || fs[0].Severity != sevWarning || !strings.Contains(fs[0].Message, "hadron node update hrn:node:acme.com:specs:msg:010:02 --role spec") {
			t.Fatalf("role=%q findings=%+v", role, fs)
		}
	}
}

func TestGovernedRoleBoundary1658(t *testing.T) {
	for _, role := range []string{"spec", "spec.rule", "spec.rule.flow"} {
		n := specNode{Loc: "freeform", Role: &role, NodeType: "info"}
		if fs := lintReadNode(n, lintMem); hasRule(fs, "not-governed") || hasRule(fs, "tag-spec") {
			t.Fatalf("role=%s findings=%+v", role, fs)
		}
	}
	n := specNode{Loc: "msg:010:01", NodeType: "info"}
	if !hasRule(lintReadNode(n, lintMem), "tag-spec") {
		t.Fatal("neither marker must retain tag-spec")
	}
}

// #1779: a tag-only node that already carries another governed role is refused
// by `--role spec` (two governed kinds), so the hint must not promise it.
func TestTagOnlyOtherGovernedRoleRemedy1779(t *testing.T) {
	for _, role := range []string{"review", "review.pr"} {
		r := role
		n := specNode{Loc: "msg:010:02", Tags: []string{"spec"}, Role: &r, NodeType: "info"}
		fs := lintReadNode(n, lintMem)
		if len(fs) != 1 || fs[0].Rule != "not-governed" || fs[0].Severity != sevWarning {
			t.Fatalf("role=%q classification changed: %+v", role, fs)
		}
		m := fs[0].Message
		if !strings.Contains(m, "role review") || !strings.Contains(m, "is refused") || !strings.Contains(m, "deliberate") {
			t.Fatalf("role=%q hint does not qualify the remedy: %q", role, m)
		}
		if strings.Contains(m, "use hadron node update") {
			t.Fatalf("role=%q hint still promises the single command: %q", role, m)
		}
	}
}

func TestGovernanceRemedyKeepsPlainHint1779(t *testing.T) {
	notes := "notes"
	for name, role := range map[string]*string{"null": nil, "notes": &notes} {
		got := governanceRemedy("'urn'", role)
		if got != "use hadron node update 'urn' --role spec" {
			t.Fatalf("%s role lost its working guidance: %q", name, got)
		}
	}
}
