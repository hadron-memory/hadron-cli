package nodedoc

import (
	"fmt"
	"strings"
	"testing"
)

// TestRenderMarkdownGoldenTree locks the byte-for-byte file shape a TREE export
// writes (standalone=false): no loc/memory keys, server field order, framing,
// recomputed contentHash. This is the non-regression anchor for the memory
// export refactor — it reproduces the serializer's prior golden output.
func TestRenderMarkdownGoldenTree(t *testing.T) {
	content := "# Intro\n\nBody."
	doc := &Document{
		ID:                 "n-1",
		Loc:                "guide:intro",
		MemoryURN:          "acme.com:kb",
		Name:               "Intro",
		Type:               "task",
		Alias:              "intro",
		Description:        "One-liner",
		Abstract:           "A longer summary.",
		AbstractOriginHash: "deadbeef",
		Content:            content,
		Tags:               []string{"guide", "intro"},
		Seq:                intptr(0),
		Data:               map[string]any{"k": "v"},
		Edges:              []Edge{{TargetID: "n-2", TargetLoc: "guide:more", Label: "next", Priority: 5}},
	}

	want := fmt.Sprintf(`---
name: Intro
id: n-1
alias: intro
type: task
description: One-liner
abstract: A longer summary.
abstractOriginHash: deadbeef
contentHash: %s
tags:
  - guide
  - intro
seq: 0
data:
  k: v
nodes:
  - id: n-2
    loc: guide:more
    rel: next
    priority: 5
---

# Intro

Body.
`, ContentHash(content))

	got, err := RenderMarkdown(doc, false)
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	if got != want {
		t.Errorf("tree render mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// standalone=false must NOT leak the self-describing keys even though the
	// Document carries Loc and MemoryURN. (The edge entry's indented "    loc:"
	// is fine; a frontmatter-level key would start the line.)
	if strings.Contains(got, "\nloc: ") || strings.Contains(got, "\nmemory: ") {
		t.Error("tree render must omit frontmatter loc/memory keys")
	}
}

// TestRenderMarkdownStandaloneKeys checks a single-node file carries its own
// loc + memory keys, positioned right after id.
func TestRenderMarkdownStandaloneKeys(t *testing.T) {
	doc := &Document{
		ID: "n-1", Loc: "findings:flaky-ci", MemoryURN: "acme.com:kb",
		Name: "Flaky CI", Type: "task", Content: "Body.",
	}
	got, err := RenderMarkdown(doc, true)
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	want := "---\nname: Flaky CI\nid: n-1\nloc: findings:flaky-ci\nmemory: acme.com:kb\ntype: task\ncontentHash: " +
		ContentHash("Body.") + "\n---\n\nBody.\n"
	if got != want {
		t.Errorf("standalone render mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestRenderMarkdownMinimal checks omit-on-default for a bare node: only
// name + id, with type omitted for the default `info` and no contentHash for
// empty content.
func TestRenderMarkdownMinimal(t *testing.T) {
	doc := &Document{ID: "x", Loc: "x", Name: "X"} // Type "" == info default
	got, err := RenderMarkdown(doc, false)
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	want := "---\nname: X\nid: x\n---\n\n\n"
	if got != want {
		t.Errorf("minimal node mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestBuildEdgeEntries covers the per-edge omit rules and the target guard.
func TestBuildEdgeEntries(t *testing.T) {
	edges := []Edge{
		{Label: "a", Priority: 0, TargetID: "t1", TargetLoc: "loc1"},       // priority 0 omitted
		{Label: "", Priority: 3, TargetID: "t2", TargetLoc: ""},            // empty rel kept, loc omitted
		{Label: "c", Condition: map[string]any{"==": "x"}, TargetID: "t3"}, // condition kept
		{Label: "orphan"},                                                  // no target id → skipped
	}
	got := buildEdgeEntries(edges)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3 (orphan skipped)", len(got))
	}
	if got[0].Priority != 0 || got[0].Loc != "loc1" {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].Rel != "" || got[1].Priority != 3 || got[1].Loc != "" {
		t.Errorf("entry 1 = %+v", got[1])
	}
	if got[2].Condition == nil {
		t.Errorf("entry 2 condition dropped: %+v", got[2])
	}
	if buildEdgeEntries(nil) != nil {
		t.Error("no edges should yield nil (omit nodes: key)")
	}
}

// TestParseMarkdown is the inverse of render: header fields, the self-describing
// keys, edges, and a trimmed body.
func TestParseMarkdown(t *testing.T) {
	src := `---
name: Flaky CI
id: n-1
loc: findings:flaky-ci
memory: acme.com:kb
type: task
description: One liner
abstract: A summary.
tags:
  - ci
nodes:
  - id: n-2
    loc: start
    rel: routes-to
    priority: 10
---

The body.
`
	doc, err := ParseMarkdown([]byte(src))
	if err != nil {
		t.Fatalf("ParseMarkdown: %v", err)
	}
	if doc.Name != "Flaky CI" || doc.ID != "n-1" || doc.Loc != "findings:flaky-ci" || doc.MemoryURN != "acme.com:kb" {
		t.Errorf("header mismatch: %+v", doc)
	}
	if doc.Type != "task" || doc.Description != "One liner" || doc.Abstract != "A summary." {
		t.Errorf("fields mismatch: %+v", doc)
	}
	if doc.Content != "The body." {
		t.Errorf("body = %q, want %q", doc.Content, "The body.")
	}
	if len(doc.Edges) != 1 {
		t.Fatalf("edges = %+v, want 1", doc.Edges)
	}
	e := doc.Edges[0]
	if e.TargetID != "n-2" || e.TargetLoc != "start" || e.Label != "routes-to" || e.Priority != 10 {
		t.Errorf("edge = %+v", e)
	}
}

func TestParseMarkdownSummaryFallback(t *testing.T) {
	doc, err := ParseMarkdown([]byte("---\nname: X\nid: x\nsummary: legacy\n---\n\nBody.\n"))
	if err != nil {
		t.Fatalf("ParseMarkdown: %v", err)
	}
	if doc.Description != "legacy" {
		t.Errorf("description ?? summary fallback failed: %q", doc.Description)
	}
}

func TestParseMarkdownNoFrontmatter(t *testing.T) {
	if _, err := ParseMarkdown([]byte("just text, no frontmatter")); err == nil {
		t.Error("expected error for missing frontmatter")
	}
}
