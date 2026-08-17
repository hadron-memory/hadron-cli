package coding

import (
	"strings"
	"testing"
)

// A cross-memory route cannot use a bare wikilink: it would resolve against the
// ROUTER's memory and find nothing (cor:urn:020:01). The line must name the
// target's memory instead.
//
// The memory label is looked up from the server rather than parsed out of the
// caller's ref — see memoryLabel for why the parsing version was wrong — so the
// only thing to pin here is the rendered shape.
func TestCrossMemoryLinkForm(t *testing.T) {
	if got := wikiLink("findings:x"); got != "[[findings:x]]" {
		t.Errorf("wikiLink = %q", got)
	}
	got := crossMemoryLink("tasks:create-platform-spec", "hrn:mem:acme.com:specs")
	want := "`tasks:create-platform-spec` in `hrn:mem:acme.com:specs`"
	if got != want {
		t.Errorf("crossMemoryLink = %q, want %q", got, want)
	}
	line := routingLineTo("To author a spec", got, "How to place a citation")
	if !strings.Contains(line, want) || !strings.Contains(line, `- **"To author a spec"** →`) {
		t.Errorf("routingLineTo = %q", line)
	}
}

// The two link forms must be distinguishable as already-linked keys, or
// planRoutingLine's duplicate check would match the wrong one.
func TestLinkFormsAreDistinctKeys(t *testing.T) {
	body := "# R\n\n- **\"A\"** → `findings:x` in `hrn:mem:other.org:kb` — elsewhere.\n"
	// The same loc in the ROUTER's memory is a different target, so the
	// cross-memory bullet must not suppress it.
	plan, err := planRoutingLine(body, "", routingLine("B", "findings:x", "here"), wikiLink("findings:x"))
	if err != nil {
		t.Fatalf("planRoutingLine: %v", err)
	}
	if plan.Skipped {
		t.Error("a cross-memory bullet for the same loc must not count as already-linked for the local one")
	}
}
