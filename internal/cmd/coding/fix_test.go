package coding

import "testing"

func TestTriggerFromDescription(t *testing.T) {
	cases := []struct{ desc, want string }{
		{"Applies when a resolver changes. Then check X.", "Applies when a resolver changes"},
		{"Verifies the thing. Applies when a schema changes.", "Applies when a schema changes"},
		{"applies when the model moves", "applies when the model moves"},
		{"Applies when a rule changes\nmore prose", "Applies when a rule changes"},
		// Nothing to promote — --fix must never invent a condition.
		{"Verifies a bg-code-gen Input modelDef sets graphqlType.", ""},
		{"", ""},
		{"   ", ""},
		{"Applies when", ""}, // bare stem is not a condition either
		{"Applies when .", ""},
	}
	for _, tc := range cases {
		if got := triggerFromDescription(tc.desc); got != tc.want {
			t.Errorf("triggerFromDescription(%q) = %q, want %q", tc.desc, got, tc.want)
		}
	}
}

func TestPlanReviewFix(t *testing.T) {
	in := reviewInput{
		Members: map[string]checkNode{
			// Fixable: broken label, description states the trigger.
			"review:a": {Loc: "review:a", Tags: []string{"review"}, Description: "Applies when a resolver changes. More."},
			// Not fixable: description has no condition, needs a human.
			"review:b": {Loc: "review:b", Tags: []string{"review"}, Description: "Verifies the codegen output."},
			// Healthy: must never be touched.
			"review:c": {Loc: "review:c", Tags: []string{"review"}, Description: "Applies when c changes."},
		},
		Edges: map[string]graphEdge{
			"review:a": edge("review:a", "child-of"),
			"review:b": edge("review:b", ""),
			"review:c": edge("review:c", "Applies when c changes"),
		},
		Toolchain: "-",
	}
	plan := planReviewFix(in, lintReview(in))
	if len(plan) != 1 {
		t.Fatalf("expected exactly one fixable edge, got %+v", plan)
	}
	if plan[0].Loc != "review:a" {
		t.Errorf("wrong node planned: %+v", plan[0])
	}
	if plan[0].NewLabel != "Applies when a resolver changes" {
		t.Errorf("unexpected new label %q", plan[0].NewLabel)
	}
	if plan[0].EdgeID == "" {
		t.Error("plan must carry the edge id — the fix is a single-edge updateEdge")
	}
}

// A check with no edge at all has nothing to relabel; --fix must not try to
// invent one (creating an edge is a different, non-mechanical decision).
func TestPlanReviewFixSkipsMissingEdge(t *testing.T) {
	in := reviewInput{
		Members:   map[string]checkNode{"review:orphan": {Loc: "review:orphan", Tags: []string{"review"}, Description: "Applies when x changes."}},
		Edges:     map[string]graphEdge{},
		Toolchain: "-",
	}
	if plan := planReviewFix(in, lintReview(in)); len(plan) != 0 {
		t.Errorf("expected no plan for a check with no edge, got %+v", plan)
	}
}

func TestPlanReviewFixIsDeterministic(t *testing.T) {
	mk := func(loc string) checkNode {
		return checkNode{Loc: loc, Tags: []string{"review"}, Description: "Applies when " + loc + " changes."}
	}
	in := reviewInput{
		Members: map[string]checkNode{"review:c": mk("review:c"), "review:a": mk("review:a"), "review:b": mk("review:b")},
		Edges: map[string]graphEdge{
			"review:a": edge("review:a", "child-of"),
			"review:b": edge("review:b", "child-of"),
			"review:c": edge("review:c", "child-of"),
		},
		Toolchain: "-",
	}
	for i := 0; i < 5; i++ {
		plan := planReviewFix(in, lintReview(in))
		if len(plan) != 3 {
			t.Fatalf("expected 3 fixes, got %d", len(plan))
		}
		if plan[0].Loc != "review:a" || plan[1].Loc != "review:b" || plan[2].Loc != "review:c" {
			t.Fatalf("plan order is unstable: %+v", plan)
		}
	}
}

// #381 — `--fix` WRITES the label it extracts, and it was cutting at the first
// period rather than the first sentence end. The three cases below are the
// real ones from the issue, and they are the vocabulary review checks are made
// of: a dotted identifier, a glob, a dotfile name.
//
// Two of the three came out as grammatically complete sentences, so nothing in
// the output said they had been truncated and the next lint run reported OK.
func TestTriggerFromDescriptionKeepsDottedTokens(t *testing.T) {
	cases := []struct{ name, desc, want string }{
		{
			"dotted identifier",
			"Own locale state in tests. Applies when a test reads AppLocale.current / asserts localized strings.",
			"Applies when a test reads AppLocale.current / asserts localized strings",
		},
		{
			"glob pattern",
			"Keep ARB keys in sync — applies when a diff touches any lib/l10n/*.arb file",
			"applies when a diff touches any lib/l10n/*.arb file",
		},
		{
			"dotfile name",
			"Applies when a PR implements ANY task whose feature has a spec in product-specs or a .specify spec — build from it.",
			"Applies when a PR implements ANY task whose feature has a spec in product-specs or a .specify spec — build from it",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := triggerFromDescription(c.desc); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// A real sentence end still ends the clause — the whole point of cutting at
// all. Pinned alongside the above so a fix for one cannot quietly disable the
// other.
func TestTriggerFromDescriptionStillStopsAtASentence(t *testing.T) {
	got := triggerFromDescription("Applies when a resolver changes. The rest is prose about why.")
	if got != "Applies when a resolver changes" {
		t.Errorf("got %q", got)
	}
	if n := triggerFromDescription("Applies when a resolver changes\nand a second line follows"); n != "Applies when a resolver changes" {
		t.Errorf("a newline must still end it, got %q", n)
	}
}

// The safety net (#381 suggestion 2): refuse rather than persist a clause that
// ends mid-thought. `--fix` is a WRITE, and a truncated label that reads as a
// sentence is invisible from the next lint run onward — so leaving it for a
// human is the same conservatism the function already applies to inventing a
// condition.
func TestTriggerFromDescriptionRefusesADanglingClause(t *testing.T) {
	for _, desc := range []string{
		"Applies when a PR implements a task whose feature has a spec or a",
		"Applies when a diff touches the",
		"Applies when a change is made by",
	} {
		if got := triggerFromDescription(desc); got != "" {
			t.Errorf("a clause ending on a function word must be refused, got %q", got)
		}
	}
	// And it must not over-refuse: a clause ending on a real word stands.
	if got := triggerFromDescription("Applies when a resolver changes"); got == "" {
		t.Error("a complete clause must survive the dangling-word guard")
	}
}

// PR #586 review, @codex: a period followed by whitespace is not necessarily a
// sentence boundary. An abbreviation is the case, and it silently truncated
// again — `Applies when e.g` even passes the dangling-word guard.
//
// Handled without a word list: a sentence end needs an UPPERCASE letter after
// the gap, which an abbreviation's continuation does not have.
func TestTriggerFromDescriptionKeepsAbbreviations(t *testing.T) {
	cases := []struct{ name, desc, want string }{
		{"e.g.", "Applies when e.g. generated clients change.", "Applies when e.g. generated clients change"},
		{"i.e.", "Applies when i.e. the schema snapshot moves.", "Applies when i.e. the schema snapshot moves"},
		{"etc.", "Applies when a diff touches schema, queries, etc. in one go.", "Applies when a diff touches schema, queries, etc. in one go"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := triggerFromDescription(c.desc); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// PR #586 review, @copilot: whitespace is decoded as RUNES. A period followed
// by NBSP or an em space would otherwise not end anything, and the prose after
// it would be written into the label.
func TestTriggerFromDescriptionHandlesUnicodeWhitespace(t *testing.T) {
	for _, space := range []string{" ", " ", " "} {
		desc := "Applies when a resolver changes." + space + "Prose that must not be captured."
		if got := triggerFromDescription(desc); got != "Applies when a resolver changes" {
			t.Errorf("space %q: got %q", space, got)
		}
	}
}
