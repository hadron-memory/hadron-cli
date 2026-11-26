package spec

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// cli#1307: a NOT_APPROVED mint blocker must name the node to approve — the
// memory root node has an empty loc and is hidden from `spec list`.
func TestRenderMintNotApprovedNamesTarget(t *testing.T) {
	d := mintDTO{Memory: "acme.com::specs", Blocked: true, Blockers: []mintFindingDTO{
		{Kind: "NOT_APPROVED", Loc: "", Message: "Draft specs"},
		{Kind: "NOT_APPROVED", Loc: "msg:010:02", Message: "x"},
		{Kind: "LINT", Loc: "msg:020", Message: "y"},
	}}
	before, _ := json.Marshal(d)
	var b bytes.Buffer
	if err := renderMint(&b, d, d.DryRun); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"the memory's root node (no loc; Draft specs)",
		"hadron memory approve-all -m acme.com::specs",
		"hadron node approve hrn:node:acme.com:specs:msg:010:02 --revision <n>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "msg:020 —") {
		t.Errorf("non-NOT_APPROVED blocker got an approve line:\n%s", out)
	}
	if after, _ := json.Marshal(d); !bytes.Equal(before, after) {
		t.Error("render changed the DTO")
	}
}
