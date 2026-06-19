package nodedoc

import (
	"strings"
	"testing"
)

func TestContentHashMatchesServer(t *testing.T) {
	// Known vector: sha256("abc") = ba7816bf...; first 8 hex = "ba7816bf".
	if got := ContentHash("abc"); got != "ba7816bf" {
		t.Errorf("ContentHash(abc) = %q, want ba7816bf", got)
	}
	if got := ContentHash(""); got != "" {
		t.Errorf("ContentHash(\"\") = %q, want empty", got)
	}
}

func TestDecodeJSON(t *testing.T) {
	if DecodeJSON(nil) != nil {
		t.Error("nil raw should decode to nil")
	}
	if DecodeJSON(raw("null")) != nil {
		t.Error("literal null should decode to nil")
	}
	if DecodeJSON(raw("  ")) != nil {
		t.Error("blank should decode to nil")
	}
	m, ok := DecodeJSON(raw(`{"a":1}`)).(map[string]any)
	if !ok || m["a"] == nil {
		t.Errorf("object decode failed: %#v", DecodeJSON(raw(`{"a":1}`)))
	}
}

func TestEncodeJSON(t *testing.T) {
	if rm, err := EncodeJSON(nil); err != nil || rm != nil {
		t.Errorf("EncodeJSON(nil) = (%v, %v), want (nil, nil)", rm, err)
	}
	rm, err := EncodeJSON(map[string]any{"a": "b"})
	if err != nil {
		t.Fatalf("EncodeJSON: %v", err)
	}
	if rm == nil || string(*rm) != `{"a":"b"}` {
		got := "<nil>"
		if rm != nil {
			got = string(*rm)
		}
		t.Errorf("EncodeJSON(map) = %s, want {\"a\":\"b\"}", got)
	}
}

// TestRenderJSONShapeAndStability checks the canonical object shape and that a
// parse∘render round-trip is byte-stable (the format is its own fixed point).
func TestRenderJSONShapeAndStability(t *testing.T) {
	doc := &Document{
		ID: "n-1", MemoryURN: "acme.com:kb", Loc: "findings:flaky-ci",
		Name: "Flaky CI", Type: "task", Description: "One liner",
		Abstract: "A summary.", AbstractOriginHash: "a1b2c3d4", ContentHash: "a1b2c3d4",
		Tags: []string{"ci"}, Content: "Body.",
		Edges: []Edge{{TargetID: "n-2", TargetLoc: "start", Label: "routes-to", Priority: 10}},
	}
	got, err := RenderJSON(doc)
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	for _, want := range []string{`"memory": "acme.com:kb"`, `"type": "task"`, `"targetLoc": "start"`, `"priority": 10`, `"edges": [`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON missing %q:\n%s", want, got)
		}
	}
	back, err := ParseJSON([]byte(got))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	again, _ := RenderJSON(back)
	if got != again {
		t.Errorf("JSON not a stable fixed point:\n--- first ---\n%s\n--- second ---\n%s", got, again)
	}
}

func TestParseJSONNormalizesEdges(t *testing.T) {
	doc, err := ParseJSON([]byte(`{"name":"X","id":"x","loc":"x"}`))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if doc.Edges == nil {
		t.Error("edges should normalize to [] (non-nil), not null")
	}
}
