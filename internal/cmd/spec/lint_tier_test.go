package spec

import (
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

// longAbstract builds an abstract inside the tight-headroom band, so the
// abstract-length ERROR fires and its tier-specific remedy can be read.
func longAbstract() *string {
	const target = abstractHardMax - 10
	var body string
	for len(body) < target {
		body += "More on-subject prose about this very spec. "
	}
	return ptr(body[:target])
}

// mustFinding fails the test if the rule did not fire. Every case here depends
// on abstract-length having fired, so a missing finding must stop the test
// rather than silently compare against a zero value.
func mustFinding(t *testing.T, fs []lintFindingDTO, rule string) lintFindingDTO {
	t.Helper()
	for _, f := range fs {
		if f.Rule == rule {
			return f
		}
	}
	t.Fatalf("%s did not fire; got %+v", rule, fs)
	return lintFindingDTO{}
}

func lintLoc(loc, title string) []lintFindingDTO {
	return lintNode(specNode{
		Loc: loc, Name: title, NodeType: "info",
		Tags: []string{"spec"}, Abstract: longAbstract(),
	}, "")
}

// #605: the abstract-length threshold is shared across tiers — the server's cap
// binds them all — and only the REMEDY differs. There are three, not two.
//
// At rule/flow tier a split is right. At index tier it is the most expensive
// operation the corpus supports, recommended automatically: a split cannot move
// children, because a citation is never renumbered, so @Vera's `cor:agt:020`
// would have cost twelve live citations. And a CONTRACT is neither — it indexes
// nothing, and its loc is a reserved atom with one per tier, so a split has
// nowhere to put a second one (@codex + @copilot on PR #609, independently).
func TestAbstractLengthRemedyIsTierAware(t *testing.T) {
	cases := []struct {
		name    string
		loc     string
		want    []string // fragments the remedy MUST contain
		wantNot []string // fragments it must NOT contain
	}{
		{"rule keeps the split remedy", "cor:agt:020:03",
			[]string{"supersede-level split", "granularity signal"},
			[]string{"INDEX", "CONTRACT"}},
		{"flow keeps the split remedy", "cor:agt:020:03:01",
			[]string{"supersede-level split"},
			[]string{"INDEX", "CONTRACT"}},

		{"feature routes, and its children are rules", "cor:agt:020",
			[]string{"INDEX", "ROUTING", "feature abstract", "cannot move rules"},
			[]string{"supersede-level split", "CONTRACT"}},

		// The level-1 ambiguity. ParseCitation reads a lone atom as a flat
		// module, so a bare PRODUCT root arrives indistinguishable from a
		// module — and their children differ (modules vs features). The message
		// must not name either, rather than name the wrong one.
		{"module does not claim to know its children", "cor:agt",
			[]string{"INDEX", "ROUTING", "product or module", "cannot move its children"},
			[]string{"supersede-level split", "cannot move features", "cannot move rules"}},
		{"bare product root gets the same honest wording", "cor",
			[]string{"product or module", "cannot move its children"},
			[]string{"cannot move features", "supersede-level split"}},

		// Contracts parse to an INDEX level but index nothing.
		{"product contract", "cor:gen",
			[]string{"CONTRACT", "reserved atom", "inherited by its siblings"},
			[]string{"supersede-level split", "INDEX of its children"}},
		{"module contract", "cor:api:000",
			[]string{"CONTRACT", "reserved atom"},
			[]string{"supersede-level split", "INDEX of its children"}},
		{"feature contract falls in the RULE band and still must not split", "cor:api:240:00",
			[]string{"CONTRACT", "reserved atom"},
			[]string{"supersede-level split"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := mustFinding(t, lintLoc(tc.loc, tc.loc+" — a spec"), "abstract-length")
			if f.Severity != sevError {
				t.Errorf("near the hard cap this is an error, got %q", f.Severity)
			}
			for _, want := range tc.want {
				if !strings.Contains(f.Message, want) {
					t.Errorf("remedy omits %q\nmessage: %s", want, f.Message)
				}
			}
			for _, not := range tc.wantNot {
				if strings.Contains(f.Message, not) {
					t.Errorf("remedy must not contain %q\nmessage: %s", not, f.Message)
				}
			}
		})
	}
}

// The conjunction hint is split-shaped, so it belongs only where a split is the
// remedy. A feature titled "workers and their names" is not telling you to
// split it — naming several subjects is what an index title does — and a
// contract's title names the provisions it carries.
func TestConjunctionHintIsRuleTierOnly(t *testing.T) {
	const title = "x — workers and their names"
	rule := mustFinding(t, lintLoc("cor:agt:020:03", title), "abstract-length")
	// Two-directional: the hint must actually fire at rule tier, or its absence
	// elsewhere proves nothing about the gate.
	if !strings.Contains(rule.Message, "already suggests") {
		t.Fatalf("the conjunction hint must still fire at rule tier, or this test measures nothing: %s", rule.Message)
	}
	for _, loc := range []string{"cor:agt:020", "cor:agt", "cor:gen", "cor:api:240:00"} {
		f := mustFinding(t, lintLoc(loc, title), "abstract-length")
		if strings.Contains(f.Message, "already suggests") {
			t.Errorf("%s: the conjunction hint is split-shaped and must not appear here: %s", loc, f.Message)
		}
	}
}
