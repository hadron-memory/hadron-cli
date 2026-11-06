package api

import (
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"strings"
	"testing"
)

func TestSpecReferenceProjection1554(t *testing.T) {
	common := []string{"items {", "kind", "field", "sourceLoc", "sourceNodeId", "targetLoc", "text", "total", "hasMore"}
	for _, tt := range []struct {
		name, doc string
		reason    bool
	}{
		{"backlinks", gen.SpecBacklinksPage_Operation, false},
		{"unresolved", gen.SpecUnresolvedReferencesPage_Operation, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lines := map[string]bool{}
			for _, l := range strings.Split(tt.doc, "\n") {
				lines[strings.TrimSpace(l)] = true
			}
			for _, f := range common {
				if !lines[f] {
					t.Errorf("missing exact selected field %q in %s", f, tt.doc)
				}
			}
			if lines["reason"] != tt.reason {
				t.Errorf("reason selection=%v", lines["reason"])
			}
		})
	}
	// The compatibility operations keep their old documents; page capability
	// names must not leak into those static selections.
	for _, d := range []string{gen.SpecBacklinks_Operation, gen.SpecUnresolvedReferences_Operation} {
		if strings.Contains(d, "Page") {
			t.Fatalf("page leaked into legacy document: %s", d)
		}
	}
}
