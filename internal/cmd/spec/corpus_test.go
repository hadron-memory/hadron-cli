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

func TestDraftLintKeepsUnreadablePlaceholderAsError(t *testing.T) {
	loc := "pas:010:04"
	nodes, placeholders, inScope := partitionDraftLintNodes(
		[]specNode{{Loc: loc, Unavailable: true}},
		draftInfo{Draft: true, Placeholders: map[string]bool{loc: true}},
	)
	if !inScope[loc] || len(placeholders) != 0 || len(nodes) != 1 {
		t.Fatalf("unreadable placeholder was dropped: nodes=%v, placeholders=%v, inScope=%v", nodes, placeholders, inScope)
	}
	findings := lintCorpus(nodes, "")
	if len(findings) != 1 || findings[0].Rule != "unavailable" || findings[0].Severity != sevError {
		t.Fatalf("want the unavailable error, got %v", findings)
	}

	// A readable placeholder still gets its single warning instead of the
	// content rules for an unwritten spec.
	nodes, placeholders, _ = partitionDraftLintNodes(
		[]specNode{{Loc: loc}},
		draftInfo{Draft: true, Placeholders: map[string]bool{loc: true}},
	)
	if len(nodes) != 0 || len(placeholders) != 1 || placeholders[0].Rule != "placeholder" {
		t.Fatalf("readable placeholder classification changed: nodes=%v, placeholders=%v", nodes, placeholders)
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
