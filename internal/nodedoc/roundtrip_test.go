package nodedoc

import "testing"

// TestRoundTrip pins the joint export/import invariant the two commands exist to
// guarantee: for every importer-consumed field, parse∘render is the identity in
// BOTH formats. Equality is asserted via the canonical JSON form, so harmless
// representation drift across YAML/JSON (int vs float, [] vs nil) can't cause a
// false failure while real field loss still does.
func TestRoundTrip(t *testing.T) {
	docs := map[string]*Document{
		"full": {
			ID: "n-1", MemoryURN: "acme.com:kb", Loc: "findings:flaky-ci",
			Name: "Flaky CI", Type: "task", Alias: "flaky",
			Description: "One liner", Abstract: "A paragraph summary.",
			AbstractOriginHash: "a1b2c3d4",
			Tags:               []string{"ci", "flaky"},
			Seq:                intptr(3),
			Data:               map[string]any{"k": "v", "count": 42, "nested": map[string]any{"a": "b"}},
			Properties:         map[string]any{"p": "q"},
			Content:            "The node body.\n\nSecond paragraph.",
			Edges: []Edge{
				{TargetID: "n-2", TargetLoc: "start", Label: "routes-to", Priority: 10, Condition: map[string]any{"flag": "x"}},
				{TargetID: "n-3", TargetLoc: "end", Label: "next"},
			},
		},
		"minimal-info": {
			ID: "n-9", MemoryURN: "acme.com:kb", Loc: "root",
			Name: "Root", Content: "Just a body.", Edges: []Edge{},
		},
	}
	for name, d := range docs {
		// contentHash is recomputed on render from content, so seed the
		// Document's field to that value to make it comparable post-round-trip.
		d.ContentHash = ContentHash(d.Content)
		canon, err := RenderJSON(d)
		if err != nil {
			t.Fatalf("%s: RenderJSON: %v", name, err)
		}

		md, err := RenderMarkdown(d, true)
		if err != nil {
			t.Fatalf("%s: RenderMarkdown: %v", name, err)
		}
		fromMd, err := ParseMarkdown([]byte(md))
		if err != nil {
			t.Fatalf("%s: ParseMarkdown: %v", name, err)
		}
		if got, _ := RenderJSON(fromMd); got != canon {
			t.Errorf("%s: markdown round-trip drift:\n--- want ---\n%s\n--- got ---\n%s", name, canon, got)
		}

		fromJSON, err := ParseJSON([]byte(canon))
		if err != nil {
			t.Fatalf("%s: ParseJSON: %v", name, err)
		}
		if got, _ := RenderJSON(fromJSON); got != canon {
			t.Errorf("%s: json round-trip drift:\n--- want ---\n%s\n--- got ---\n%s", name, canon, got)
		}
	}
}
