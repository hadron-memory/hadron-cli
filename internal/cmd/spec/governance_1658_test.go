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
