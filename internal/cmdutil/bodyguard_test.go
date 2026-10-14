package cmdutil

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestGetHeaderPrefix(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"node get header", "Dispatch a CHECKED issue\n  loc: tasks:dispatch\n  urn: hrn:node:x:y:tasks:dispatch\n\nbody", true},
		{"node get header, CRLF", "Dispatch\r\n  loc: tasks:dispatch\r\n", true},
		{"spec get header", "Delivery window\nhrn:node:hadronmemory.com:specs:msg:010:02\nTags: a\n\nbody", true},
		{"spec get placeholder header", "Reserved\nPLACEHOLDER — reserved, not yet written\nhrn:node:hadronmemory.com:specs:msg:010:03\n", true},
		{"plain markdown", "# Title\n\nSome text.\n", false},
		{"heading then a URN line", "# Title\nhrn:node:a:b:c\n", false},
		{"one line", "just one line", false},
		{"empty", "", false},
		{"indented first line", "  loc: x\n  loc: y\n", false},
		{"blank title", "\n  loc: x\n", false},
		{"loc with no value", "Title\n  loc: \n", false},
		{"URN line with prose", "Title\nhrn:node:a:b:c is the node\n", false},
		{"three-space loc", "Title\n   loc: x\n", false},
	} {
		if got := GetHeaderPrefix(tc.body); got != tc.want {
			t.Errorf("%s: GetHeaderPrefix = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRefuseGetHeaderPrefix(t *testing.T) {
	if err := RefuseGetHeaderPrefix("# fine\n", "--force"); err != nil {
		t.Fatalf("a plain body must pass, got %v", err)
	}
	err := RefuseGetHeaderPrefix("Name\n  loc: a:b\n\nbody", "--force")
	if err == nil || exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("want a usage (exit 2) refusal, got %v", err)
	}
	for _, want := range []string{"--body-only", "--force", `"Name"`, "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %s: %v", want, err)
		}
	}
}

func TestDroppedTemplateTags(t *testing.T) {
	stored := "{{=<% %>=}}\nHi {{name}}, {{{raw}}} {{#s}}x{{/s}} {{name}}"
	for _, tc := range []struct {
		name     string
		proposed string
		want     []string
	}{
		{"unchanged", stored, []string{}},
		{"rendered: everything gone", "\nHi ,  x ", []string{"{{=<% %>=}}", "{{name}}", "{{{raw}}}", "{{#s}}", "{{/s}}"}},
		{"delimiter line dropped only", "Hi {{name}}, {{{raw}}} {{#s}}x{{/s}}", []string{"{{=<% %>=}}"}},
		{"one of two {{name}} removed is no drop", "{{=<% %>=}}\nHi {{name}}, {{{raw}}} {{#s}}x{{/s}}", []string{}},
		{"added tags are not drops", stored + " {{extra}}", []string{}},
	} {
		got := DroppedTemplateTags(stored, tc.proposed)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s: dropped = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTemplateDropWarning(t *testing.T) {
	if w := TemplateDropWarning("no tags", "still none"); w != "" {
		t.Errorf("no drop must give no warning, got %q", w)
	}
	w := TemplateDropWarning("{{=<% %>=}}\n{{name}}", "{{name}}")
	for _, want := range []string{"{{=<% %>=}}", "delimiter", "--body-only"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning must mention %s: %q", want, w)
		}
	}
	if strings.Contains(TemplateDropWarning("{{a}}", ""), "delimiter") {
		t.Error("the delimiter sentence is only for a dropped delimiter change")
	}
	var many strings.Builder
	for i := range 10 {
		many.WriteString("{{v" + string(rune('a'+i)) + "}}")
	}
	if w := TemplateDropWarning(many.String(), ""); !strings.Contains(w, "10 template tag(s)") || !strings.Contains(w, "and 2 more") {
		t.Errorf("a long list is capped and counted: %q", w)
	}
}
