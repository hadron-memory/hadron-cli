package spec

import (
	"strings"
	"testing"
)

func TestTagOnlyLint1658(t *testing.T) {
	for _, role := range []string{"", "special", "specification", "other"} {
		n := specNode{Loc: "msg:010:02", Tags: []string{"spec"}, Role: &role, NodeType: "task"}
		fs := lintNode(n, lintMem)
		if len(fs) != 1 || fs[0].Rule != "not-governed" || fs[0].Severity != sevWarning || !strings.Contains(fs[0].Message, "hadron spec edit") {
			t.Fatalf("role=%q findings=%+v", role, fs)
		}
	}
}
