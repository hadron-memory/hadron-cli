package spec

import "testing"

// PR #587 review, @codex. `spec get` prints a per-node lint summary, and it
// reached lintNode with ContentIsRaw false on BOTH paths — so a stale spec
// printed "Lint: ✓ ok" while `spec lint` on the same node reported it.
//
// One command contradicting another about the same node is worse than either
// answer alone, so the parity is what is asserted: the same specNode must get
// the same findings whichever surface built it.
func TestSpecGetLintAgreesWithSpecLintOnStaleness(t *testing.T) {
	content := "# cor:api:060:01 — R\n\n## Definition\nx\n\n## Rule\nx\n\n## What invalidates this spec\nChanges.\n"
	abs := "An abstract written against an older body."
	stale := "deadbeef" // a fingerprint that cannot match the body

	raw := specNode{
		Loc: "cor:api:060:01", Name: "cor:api:060:01 — R", NodeType: "info",
		Tags: []string{"spec"}, Abstract: &abs, AbstractOriginHash: &stale,
		Content: &content, ContentIsRaw: true, DataVersion: "0.0.1",
	}
	compiled := raw
	compiled.ContentIsRaw = false

	if !hasRule(lintNode(raw, ""), "abstract-stale") {
		t.Fatal("precondition: the raw projection reports stale")
	}
	// The COMPILED projection must not claim clean — it must claim nothing,
	// which is what the uncheckable state is for. The bug was that it looked
	// identical to a clean node.
	if hasRule(lintNode(compiled, ""), "abstract-stale") {
		t.Error("a compiled body must not be compared")
	}
	if abstractVerification(compiled) != abstractUncheckable {
		t.Error("a compiled body is uncheckable, not verified — the two must stay distinguishable")
	}
}
