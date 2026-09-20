package spec

import (
	"strings"
	"testing"
)

// The scanner keys on URN SHAPE, never on "it parsed" — which is the trap that
// would have made this rule useless. `SplitNodeUrn("cor:urn:010:04")` returns
// memory "cor:urn", loc "010:04" and a NIL ERROR, so a parse-first scanner
// would report every citation in the corpus as a URN example, on a corpus made
// of citations.
func TestScanURNExamplesKeysOnShapeNotOnParsability(t *testing.T) {
	notURNs := []string{
		"cor:urn:010:04",         // a bare citation — parses cleanly, is not a URN
		"cor:acl:030:01",         // ditto, and the corpus is full of them
		"see the mm-app memory",  // prose
		"hadronmemory.com:specs", // a MEMORY ref, not a node URN
		"a:b",                    // too short to be anything
	}
	for _, s := range notURNs {
		if got := scanURNExamples(s); len(got) != 0 {
			t.Errorf("%q must not be read as a URN example, got %+v", s, got)
		}
	}

	areURNs := map[string]bool{
		"hrn:node:acme.com:mmdata:review:sort":   true,
		"urn:node:acme.com:mmdata:review:sort":   true,
		"hrn:node:acme.com::mmdata::review:sort": true,
		"acme.com::mmdata::review:sort":          true, // scheme-less v1 chain
	}
	for s := range areURNs {
		if got := scanURNExamples(s); len(got) != 1 {
			t.Errorf("%q must be read as one URN example, got %d", s, len(got))
		}
	}
}

// The heart of #527: a deep v1 chain is REPORTED AS UNDECIDABLE rather than
// decomposed, because urn-lib and the server disagree about it.
//
// Measured 2026-09-20 against urn-lib-go v0.0.13 and the running server:
//
//	hrn:node:…::experiments::services::db-helpers::query
//	  server    → hadronmemory.com:experiments:services:db-helpers   (last-segment)
//	  SplitNodeUrn → hadronmemory.com:experiments                    (first-two)
//
// Decomposing here would print a confident wrong answer about exactly the node
// this rule was filed for — the failure mode it exists to catch, one level up.
func TestScanURNExamplesRefusesToGuessOnADeepChain(t *testing.T) {
	// The literal from cor:urn:010:04, which is why #527 exists.
	const lit = "micromentor.org::coding-app::coding-agent::app-mem::a:b"
	got := scanURNExamples(lit)
	if len(got) != 1 {
		t.Fatalf("want one finding, got %d", len(got))
	}
	f := got[0]
	if f.decomposed {
		t.Fatalf("a deep chain must NOT be decomposed — urn-lib is wrong about it; got memory %q", f.memory)
	}
	if f.memory != "" || f.loc != "" {
		t.Errorf("an undecidable literal must carry no decomposition, got memory=%q loc=%q", f.memory, f.loc)
	}
	// The remedy must name the oracle, because "cannot say" without "ask this"
	// is a dead end for the author standing in front of it.
	for _, want := range []string{"hadron_get_node", "5 segments"} {
		if !strings.Contains(f.why, want) {
			t.Errorf("the message must contain %q, got: %s", want, f.why)
		}
	}
}

// Four is where the two readings part — measured, not a safe-looking margin.
// Three segments is the arity at which they coincide, and it is also the
// shortest legal example, which is why every conformance fixture uses it and
// the drift went unseen.
func TestV1ChainSegmentsBoundary(t *testing.T) {
	cases := []struct {
		lit        string
		segs       int
		decomposed bool
	}{
		{"hrn:node:acme.com:mmdata:review:sort", 0, true},        // flat v2: fixed arity, library correct
		{"hrn:node:acme.com::mmdata::review:sort", 3, true},      // rules coincide
		{"hrn:node:acme.com::mmdata::services::query", 4, false}, // they diverge HERE
		{"hrn:node:a.com::b::c::d::e", 5, false},                 // and stay diverged
	}
	for _, c := range cases {
		t.Run(c.lit, func(t *testing.T) {
			if got := v1ChainSegments(c.lit); got != c.segs {
				t.Errorf("v1ChainSegments = %d, want %d", got, c.segs)
			}
			f := classifyURNLiteral(c.lit)
			if f.decomposed != c.decomposed {
				t.Errorf("decomposed = %v, want %v (why: %s)", f.decomposed, c.decomposed, f.why)
			}
		})
	}
}

// Advisory, never a gate: the lint cannot know which reading the author meant,
// so failing a corpus run on one would assert a classification it has not
// computed. Every finding this rule emits is sevInfo.
func TestLintURNExamplesIsAlwaysAdvisory(t *testing.T) {
	body := "hrn:node:acme.com:mmdata:review:sort and " +
		"micromentor.org::coding-app::coding-agent::app-mem::a:b and " +
		"hrn:node:not-a-urn-at-all::x"
	got := lintURNExamples(body, "hadronmemory.com:specs")
	if len(got) == 0 {
		t.Fatal("want findings")
	}
	for _, f := range got {
		if f.Severity != sevInfo {
			t.Errorf("severity = %q, want %q — this rule must never gate: %s", f.Severity, sevInfo, f.Message)
		}
		if f.Rule != "urn-example" {
			t.Errorf("rule = %q", f.Rule)
		}
	}
}

// One URN shown five times is one claim. Five identical advisory lines would
// train a reader to skim the rule, which is the opposite of putting the
// disagreement where they cannot miss it.
func TestScanURNExamplesDeduplicates(t *testing.T) {
	const lit = "hrn:node:acme.com:mmdata:review:sort"
	body := lit + "\n\n```\n" + lit + "\n```\nand again " + lit + "\n"
	if got := scanURNExamples(body); len(got) != 1 {
		t.Errorf("want 1 deduplicated finding, got %d", len(got))
	}
}
