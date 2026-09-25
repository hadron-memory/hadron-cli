package nodedoc

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#714 — the governed signals (role, isRunnable) travel in a node file, keyed
// and placed as hadron-server's GitHub mirror writes them (#1218,
// buildNodeFrontmatter): after seq, before data, emitted when NON-NULL.

func ptr[T any](v T) *T { return &v }

func TestMarkdownCarriesRoleAndRunnableWhereTheMirrorPutsThem(t *testing.T) {
	doc := &Document{
		Name: "Spec", ID: "n1", Seq: ptr(2),
		Role: ptr("spec"), IsRunnable: ptr(true),
		Data: map[string]any{"k": "v"}, Edges: []Edge{},
	}
	out, err := RenderMarkdown(doc, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "seq: 2\nrole: spec\nrunnable: true\ndata:\n"
	if !strings.Contains(out, want) {
		t.Errorf("frontmatter must carry role and runnable between seq and data:\n%s", out)
	}
}

func TestMarkdownKeepsAnExplicitFalseAndOmitsAbsent(t *testing.T) {
	// A node's isRunnable has three states: false is carried, nil omitted. An
	// edge differs, which is why the edge codec's `runnable` omits false.
	explicit, err := RenderMarkdown(&Document{Name: "N", ID: "n1", IsRunnable: ptr(false), Edges: []Edge{}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(explicit, "\nrunnable: false\n") {
		t.Errorf("an explicit false must be written:\n%s", explicit)
	}
	absent, err := RenderMarkdown(&Document{Name: "N", ID: "n1", Edges: []Edge{}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(absent, "runnable:") || strings.Contains(absent, "role:") {
		t.Errorf("absent signals must not be written:\n%s", absent)
	}
}

func TestMarkdownParsesTheSignalsAndAnOldFileHasNone(t *testing.T) {
	doc, err := ParseMarkdown([]byte("---\nname: N\nid: n1\nrole: review\nrunnable: false\n---\n\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Role == nil || *doc.Role != "review" || doc.IsRunnable == nil || *doc.IsRunnable {
		t.Errorf("role %v runnable %v, want review / false", doc.Role, doc.IsRunnable)
	}
	// A file written before these keys existed has no opinion — nil, never a
	// zero value an importer could send as "clear".
	old, err := ParseMarkdown([]byte("---\nname: N\nid: n1\n---\n\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if old.Role != nil || old.IsRunnable != nil {
		t.Errorf("an old file must parse as no opinion, got role %v runnable %v", old.Role, old.IsRunnable)
	}
}

func TestJSONCarriesTheSignalsAndNullWhenAbsent(t *testing.T) {
	out, err := RenderJSON(&Document{Name: "N", ID: "n1", Role: ptr("spec"), IsRunnable: ptr(false), Edges: []Edge{}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	if m["role"] != "spec" || m["isRunnable"] != false {
		t.Errorf("role %v isRunnable %v, want spec / false", m["role"], m["isRunnable"])
	}
	back, err := ParseJSON([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if back.Role == nil || *back.Role != "spec" || back.IsRunnable == nil || *back.IsRunnable {
		t.Errorf("JSON round trip lost the signals: role %v runnable %v", back.Role, back.IsRunnable)
	}

	none, err := RenderJSON(&Document{Name: "N", ID: "n1", Edges: []Edge{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(none, `"role": null`) || !strings.Contains(none, `"isRunnable": null`) {
		t.Errorf("absent signals are null in the canonical JSON:\n%s", none)
	}
	old, err := ParseJSON([]byte(`{"name":"N","id":"n1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if old.Role != nil || old.IsRunnable != nil {
		t.Errorf("a JSON file without the keys must parse as no opinion")
	}
}

func TestMarkdownRoundTripsTheSignals(t *testing.T) {
	for _, tc := range []struct {
		role     *string
		runnable *bool
	}{
		{ptr("spec"), nil}, {nil, ptr(true)}, {nil, ptr(false)}, {ptr("weather-widget"), ptr(true)}, {nil, nil},
	} {
		in := &Document{Name: "N", ID: "n1", Role: tc.role, IsRunnable: tc.runnable, Edges: []Edge{}}
		md, err := RenderMarkdown(in, true)
		if err != nil {
			t.Fatal(err)
		}
		out, err := ParseMarkdown([]byte(md))
		if err != nil {
			t.Fatal(err)
		}
		if !samePtr(in.Role, out.Role) || !samePtr(in.IsRunnable, out.IsRunnable) {
			t.Errorf("round trip changed role %v→%v runnable %v→%v", in.Role, out.Role, in.IsRunnable, out.IsRunnable)
		}
	}
}

func samePtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
