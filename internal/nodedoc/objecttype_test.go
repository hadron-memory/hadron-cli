package nodedoc

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#720 — objectType (#725) travels in a node file, placed and emitted as
// hadron-server's git mirror writes it: right after type, only when set.

func TestMarkdownCarriesObjectTypeAfterType(t *testing.T) {
	out, err := RenderMarkdown(&Document{Name: "Acme", ID: "n1", Type: "record", ObjectType: "competitor", Description: "d", Edges: []Edge{}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\ntype: record\nobjectType: competitor\ndescription: d\n") {
		t.Errorf("objectType must sit right after type, as the mirror writes it:\n%s", out)
	}
	back, err := ParseMarkdown([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if back.ObjectType != "competitor" {
		t.Errorf("round trip lost objectType: %q", back.ObjectType)
	}
}

func TestMarkdownOmitsAnUnsetObjectTypeAndAnOldFileHasNone(t *testing.T) {
	out, err := RenderMarkdown(&Document{Name: "N", ID: "n1", Edges: []Edge{}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "objectType") {
		t.Errorf("an unset objectType must not be written:\n%s", out)
	}
	old, err := ParseMarkdown([]byte("---\nname: N\nid: n1\ntype: record\n---\n\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if old.ObjectType != "" {
		t.Errorf("a file without the key has no opinion, got %q", old.ObjectType)
	}
}

func TestJSONCarriesObjectType(t *testing.T) {
	out, err := RenderJSON(&Document{Name: "N", ID: "n1", Type: "record", ObjectType: "insight", Edges: []Edge{}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	if m["objectType"] != "insight" {
		t.Errorf("objectType = %v, want insight", m["objectType"])
	}
	back, err := ParseJSON([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if back.ObjectType != "insight" {
		t.Errorf("JSON round trip lost objectType: %q", back.ObjectType)
	}
	// Unset is "" and the key is still present: the canonical JSON carries
	// every field (the shape server nodeExport mirrors), so no omitempty.
	unset, err := RenderJSON(&Document{Name: "N", ID: "n1", Edges: []Edge{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unset, `"objectType": ""`) {
		t.Errorf("an unset objectType must render as \"\", key present:\n%s", unset)
	}
	old, err := ParseJSON([]byte(`{"name":"N","id":"n1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if old.ObjectType != "" {
		t.Errorf("a JSON file without the key has no opinion, got %q", old.ObjectType)
	}
}
