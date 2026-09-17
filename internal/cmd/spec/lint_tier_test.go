package spec

import (
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

// longAbstract builds an abstract inside the tight-headroom band so the
// abstract-length ERROR fires, optionally containing the given citations so the
// index-completeness check can be exercised on the same node.
func longAbstract(mentions ...string) *string {
	const target = abstractHardMax - 10 // inside the tight-headroom band
	var body string
	for _, m := range mentions {
		body += m + " does a thing. "
	}
	for len(body) < target {
		body += "More on-subject prose about this very spec. "
	}
	return ptr(body[:target])
}

func findingFor(fs []lintFindingDTO, rule string) (lintFindingDTO, bool) {
	for _, f := range fs {
		if f.Rule == rule {
			return f, true
		}
	}
	return lintFindingDTO{}, false
}

// #605: the abstract-length threshold is shared across tiers; the REMEDY is
// not. At rule tier, "supersede-level split" is correct advice. At feature
// tier, following it literally means superseding every child into a tombstone,
// because a split cannot move children — a citation is never renumbered. @Vera
// hit this on cor:agt:020, which has twelve live rules.
func TestAbstractLengthRemedyIsTierAware(t *testing.T) {
	cases := []struct {
		name          string
		loc           string
		wantSplit     bool
		wantRouteWord string
	}{
		{"rule keeps the split remedy", "cor:agt:020:03", true, ""},
		{"flow keeps the split remedy", "cor:agt:020:03:01", true, ""},
		{"feature must NOT be told to split", "cor:agt:020", false, "feature"},
		{"module must NOT be told to split", "cor:agt", false, "module"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := lintNode(specNode{
				Loc: tc.loc, Name: tc.loc + " — a spec", NodeType: "info",
				Tags: []string{"spec"}, Abstract: longAbstract(),
			})
			f, ok := findingFor(fs, "abstract-length")
			if !ok {
				t.Fatalf("abstract-length must fire at every tier — the hard cap binds all of them; got %+v", fs)
			}
			if f.Severity != sevError {
				t.Errorf("near the hard cap this is an error, got %q", f.Severity)
			}
			mentionsSplit := strings.Contains(f.Message, "supersede-level split")
			if mentionsSplit != tc.wantSplit {
				t.Errorf("split remedy present=%v, want %v\nmessage: %s", mentionsSplit, tc.wantSplit, f.Message)
			}
			if tc.wantRouteWord != "" {
				// The index-tier remedy has to say what to do INSTEAD, not just
				// withhold the split — otherwise the reader is left with a
				// reported error and no available action.
				for _, must := range []string{"INDEX", "ROUTING", tc.wantRouteWord} {
					if !strings.Contains(f.Message, must) {
						t.Errorf("index-tier remedy omits %q\nmessage: %s", must, f.Message)
					}
				}
			}
		})
	}
}

// The conjunction hint is split-shaped, so it must not survive at index tier
// either — a feature titled "workers and their names" is not telling you to
// split it, and appending the hint would reinstate the advice the tier-specific
// remedy exists to withhold.
func TestConjunctionHintIsRuleTierOnly(t *testing.T) {
	const title = "cor:agt:020 — workers and their names"
	rule := lintNode(specNode{Loc: "cor:agt:020:03", Name: title, NodeType: "info",
		Tags: []string{"spec"}, Abstract: longAbstract()})
	feature := lintNode(specNode{Loc: "cor:agt:020", Name: title, NodeType: "info",
		Tags: []string{"spec"}, Abstract: longAbstract()})

	rf, _ := findingFor(rule, "abstract-length")
	ff, _ := findingFor(feature, "abstract-length")
	// Two-directional: the hint must actually appear at rule tier, or its
	// absence at feature tier proves nothing about the gate.
	if !strings.Contains(rf.Message, "already suggests") {
		t.Fatalf("the conjunction hint must still fire at rule tier, or this test measures nothing: %s", rf.Message)
	}
	if strings.Contains(ff.Message, "already suggests") {
		t.Errorf("the conjunction hint is split-shaped and must not appear at index tier: %s", ff.Message)
	}
}
