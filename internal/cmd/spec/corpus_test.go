package spec

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// A placeholder's detail carries the marker and ONE lint finding — the rules
// an empty body would trip are not run on an unwritten spec.
func TestSpecDetailOfAPlaceholder(t *testing.T) {
	n := &gen.GetNodeNode{Loc: "pas:010:04", Name: "pas:010:04", NodeType: "info"}
	d := specDetailFromNode(n, true, nil, "", draftInfo{Draft: true, Placeholders: map[string]bool{"pas:010:04": true}})
	if !d.Placeholder || len(d.Lint) != 1 || d.Lint[0].Rule != "placeholder" {
		t.Errorf("want the placeholder marker and exactly the placeholder finding, got %+v", d)
	}
	d = specDetailFromNode(n, true, nil, "", draftInfo{})
	if d.Placeholder {
		t.Errorf("outside a draft nothing is a placeholder: %+v", d)
	}
}

// In a DRAFT a split is available (spec renumber), so the index advice must
// not claim it is impossible; in a minted corpus it still says so.
func TestIndexAdviceIsStateAware(t *testing.T) {
	c, err := ParseCitation("msg:010")
	if err != nil {
		t.Fatal(err)
	}
	minted, draft := indexRemedy(c, false), indexRemedy(c, true)
	if !strings.Contains(minted, "never renumbered") || strings.Contains(minted, "spec renumber") {
		t.Errorf("minted advice: %q", minted)
	}
	if strings.Contains(draft, "never renumbered") || !strings.Contains(draft, "spec renumber") {
		t.Errorf("draft advice: %q", draft)
	}
	// The generic split remedy likewise drops "supersede-level" in a draft.
	rule, _ := ParseCitation("msg:010:02")
	long := abstractHardMax - 10
	if msg := nearCapMessage(long, "T", rule, true, true); !strings.Contains(msg, "nothing is superseded before minting") {
		t.Errorf("draft split advice: %q", msg)
	}
	if msg := nearCapMessage(long, "T", rule, true, false); !strings.Contains(msg, "supersede-level split") {
		t.Errorf("minted split advice: %q", msg)
	}
}
