package coding

import (
	"strings"
	"testing"
)

func member(loc string) checkNode {
	return checkNode{Loc: loc, Tags: []string{"review"}, Description: "Applies when something changes. Then do the thing."}
}

func edge(loc, label string) graphEdge {
	return graphEdge{ID: "edg_" + loc, Label: label, Loc: loc + ":" + label + ":review", Other: loc}
}

// rulesFor returns the rules fired against one node, for compact assertions.
func rulesFor(fs []findingDTO, loc string) []string {
	var out []string
	for _, f := range fs {
		if f.Node == loc {
			out = append(out, f.Rule)
		}
	}
	return out
}

func has(rules []string, want string) bool {
	for _, r := range rules {
		if r == want {
			return true
		}
	}
	return false
}

func TestLabelRules(t *testing.T) {
	cases := []struct {
		label string
		want  string // "" = no label finding
	}{
		{"Applies when a resolver changes", ""},
		{"applies when a resolver changes", ""}, // stem is case-insensitive
		{"", "label-present"},
		{"   ", "label-present"},
		{"child-of", "label-is-condition"},
		{"applies-when", "label-is-condition"},
		{"related", "label-is-condition"},
		{"Applies when", "label-is-condition"},  // bare stem
		{"Applies when ", "label-is-condition"}, // stem + whitespace only
	}
	for _, tc := range cases {
		in := reviewInput{
			Members:   map[string]checkNode{"review:x": member("review:x")},
			Edges:     map[string]graphEdge{"review:x": edge("review:x", tc.label)},
			Toolchain: "-",
		}
		rules := rulesFor(lintReview(in), "review:x")
		switch {
		case tc.want == "" && len(rules) > 0:
			t.Errorf("label %q: expected clean, got %v", tc.label, rules)
		case tc.want != "" && !has(rules, tc.want):
			t.Errorf("label %q: expected %s, got %v", tc.label, tc.want, rules)
		}
	}
}

// A check with no edge to the parent is invisible to tasks:review-changes —
// the highest-severity finding, and one only the memory sweep can see.
func TestParentEdgeMissing(t *testing.T) {
	in := reviewInput{
		Members:   map[string]checkNode{"review:orphan": member("review:orphan")},
		Edges:     map[string]graphEdge{},
		Toolchain: "-",
	}
	fs := lintReview(in)
	if !has(rulesFor(fs, "review:orphan"), "parent-edge-exists") {
		t.Fatalf("expected parent-edge-exists, got %v", fs)
	}
	if fs[0].Severity != sevError {
		t.Errorf("parent-edge-exists should be an error, got %q", fs[0].Severity)
	}
}

// The :rel: loc is the server's derived fallback for a nameless edge, so it
// corroborates the empty label rather than being its own rule.
func TestEmptyLabelMentionsRelLoc(t *testing.T) {
	e := graphEdge{ID: "e1", Label: "", Loc: "review:x:rel:review", Other: "review:x"}
	in := reviewInput{
		Members:   map[string]checkNode{"review:x": member("review:x")},
		Edges:     map[string]graphEdge{"review:x": e},
		Toolchain: "-",
	}
	fs := lintReview(in)
	var msg string
	for _, f := range fs {
		if f.Rule == "label-present" {
			msg = f.Message
		}
	}
	if msg == "" {
		t.Fatalf("expected label-present, got %v", fs)
	}
	if !strings.Contains(msg, "review:x:rel:review") {
		t.Errorf("empty-label message should cite the derived loc, got %q", msg)
	}
	for _, f := range fs {
		if f.Rule == "label-is-loc" {
			t.Error("the :rel: loc must not be its own rule")
		}
	}
}

// A pair of `child-of` labels is one defect reported once per node by
// label-is-condition — duplicate-trigger must not double-report it.
func TestDuplicateTriggerOnlyCountsValidConditions(t *testing.T) {
	in := reviewInput{
		Members: map[string]checkNode{"review:a": member("review:a"), "review:b": member("review:b")},
		Edges: map[string]graphEdge{
			"review:a": edge("review:a", "child-of"),
			"review:b": edge("review:b", "child-of"),
		},
		Toolchain: "-",
	}
	for _, f := range lintReview(in) {
		if f.Rule == "duplicate-trigger" {
			t.Fatalf("broken labels must not also be reported as duplicate triggers: %+v", f)
		}
	}

	// Two genuinely valid, identical triggers do get flagged.
	in.Edges = map[string]graphEdge{
		"review:a": edge("review:a", "Applies when a rule subclass changes"),
		"review:b": edge("review:b", "Applies when a rule subclass changes"),
	}
	n := 0
	for _, f := range lintReview(in) {
		if f.Rule == "duplicate-trigger" {
			n++
			if f.Severity != sevWarning {
				t.Errorf("duplicate-trigger should be a warning, got %q", f.Severity)
			}
		}
	}
	if n != 2 {
		t.Errorf("expected both siblings flagged, got %d", n)
	}
}

func TestSeqUniqueness(t *testing.T) {
	s34, other := 34, 35
	a, b, c := member("review:a"), member("review:b"), member("review:c")
	a.Seq, b.Seq, c.Seq = &s34, &s34, &other
	in := reviewInput{
		Members: map[string]checkNode{"review:a": a, "review:b": b, "review:c": c},
		Edges: map[string]graphEdge{
			"review:a": edge("review:a", "Applies when a changes"),
			"review:b": edge("review:b", "Applies when b changes"),
			"review:c": edge("review:c", "Applies when c changes"),
		},
		Toolchain: "-",
	}
	fs := lintReview(in)
	if !has(rulesFor(fs, "review:a"), "seq-unique") || !has(rulesFor(fs, "review:b"), "seq-unique") {
		t.Errorf("expected both seq-34 siblings flagged, got %v", fs)
	}
	if has(rulesFor(fs, "review:c"), "seq-unique") {
		t.Error("a unique seq must not be flagged")
	}

	// An unset seq is the norm, not a finding.
	for loc := range in.Members {
		m := in.Members[loc]
		m.Seq = nil
		in.Members[loc] = m
	}
	for _, f := range lintReview(in) {
		if f.Rule == "seq-unique" {
			t.Errorf("unset seq must not be flagged: %+v", f)
		}
	}
}

func TestDescriptionPresent(t *testing.T) {
	n := member("review:x")
	n.Description = ""
	in := reviewInput{
		Members:   map[string]checkNode{"review:x": n},
		Edges:     map[string]graphEdge{"review:x": edge("review:x", "Applies when x changes")},
		Toolchain: "-",
	}
	fs := lintReview(in)
	if !has(rulesFor(fs, "review:x"), "description-present") {
		t.Errorf("expected description-present, got %v", fs)
	}
	if fs[0].Severity != sevWarning {
		t.Errorf("description-present should be a warning, got %q", fs[0].Severity)
	}
}

func TestForeignToolchain(t *testing.T) {
	// A corpus that clearly reads as TypeScript, plus one Dart trigger.
	mk := func(loc, desc string) checkNode {
		return checkNode{Loc: loc, Tags: []string{"review"}, Description: desc}
	}
	in := reviewInput{
		Members: map[string]checkNode{
			"review:a": mk("review:a", "Applies when a TypeScript file changes"),
			"review:b": mk("review:b", "Guards npm dependency drift in TypeScript"),
			"review:c": mk("review:c", "TypeScript resolver conventions for .ts modules"),
			"review:d": mk("review:d", "Runs the formatter"),
		},
		Edges: map[string]graphEdge{
			"review:a": edge("review:a", "Applies when any TypeScript file changes"),
			"review:b": edge("review:b", "Applies when package.json changes"),
			"review:c": edge("review:c", "Applies when a .ts resolver changes"),
			"review:d": edge("review:d", "Applies when Dart sources change"),
		},
	}
	fs := lintReview(in)
	if !has(rulesFor(fs, "review:d"), "foreign-toolchain") {
		t.Errorf("expected the Dart trigger flagged in a TS corpus, got %v", fs)
	}
	for _, loc := range []string{"review:a", "review:b", "review:c"} {
		if has(rulesFor(fs, loc), "foreign-toolchain") {
			t.Errorf("%s is native to the corpus and must not be flagged", loc)
		}
	}

	// Explicit --toolchain overrides inference.
	in.Toolchain = "dart"
	if has(rulesFor(lintReview(in), "review:d"), "foreign-toolchain") {
		t.Error("--toolchain dart should make the Dart trigger native")
	}
	// And "-" disables the heuristic outright.
	in.Toolchain = "-"
	for _, f := range lintReview(in) {
		if f.Rule == "foreign-toolchain" {
			t.Errorf("--toolchain - must disable the rule: %+v", f)
		}
	}
}

// The heuristic must stay silent when it cannot pick a winner, rather than
// guessing — an even split is exactly the mmdata trigger corpus.
func TestForeignToolchainSilentWhenAmbiguous(t *testing.T) {
	in := reviewInput{
		Members: map[string]checkNode{
			"review:a": {Loc: "review:a", Tags: []string{"review"}, Description: "d"},
			"review:b": {Loc: "review:b", Tags: []string{"review"}, Description: "d"},
		},
		Edges: map[string]graphEdge{
			"review:a": edge("review:a", "Applies when Dart sources change"),
			"review:b": edge("review:b", "Applies when a TypeScript file changes"),
		},
	}
	for _, f := range lintReview(in) {
		if f.Rule == "foreign-toolchain" {
			t.Errorf("a 1-1 split must yield no opinion, got %+v", f)
		}
	}
}

// An unreadable edge source can't be tested against the membership predicate,
// so it is reported as indeterminate rather than dropped (CLAUDE.md) — and as a
// warning, since it may well not be a checklist item at all.
func TestUnavailableSurfaced(t *testing.T) {
	in := reviewInput{
		Members:     map[string]checkNode{},
		Edges:       map[string]graphEdge{},
		Unavailable: []string{"tasks:build-review-coverage"},
		Toolchain:   "-",
	}
	fs := lintReview(in)
	if len(fs) != 1 || fs[0].Rule != "check-node-resolves" {
		t.Fatalf("expected check-node-resolves, got %v", fs)
	}
	if fs[0].Node != "tasks:build-review-coverage" {
		t.Errorf("finding should name the unreadable loc, got %q", fs[0].Node)
	}
	if fs[0].Severity != sevWarning {
		t.Errorf("indeterminate membership should warn, not error; got %q", fs[0].Severity)
	}
}

func TestFindingsAreDeterministic(t *testing.T) {
	in := reviewInput{
		Members: map[string]checkNode{
			"review:b": {Loc: "review:b", Tags: []string{"review"}},
			"review:a": {Loc: "review:a", Tags: []string{"review"}},
		},
		Edges: map[string]graphEdge{
			"review:a": edge("review:a", "child-of"),
			"review:b": edge("review:b", "child-of"),
		},
		Toolchain: "-",
	}
	first := lintReview(in)
	for i := 0; i < 5; i++ {
		got := lintReview(in)
		if len(got) != len(first) {
			t.Fatalf("unstable finding count: %d vs %d", len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("unstable order at %d: %+v vs %+v", j, got[j], first[j])
			}
		}
	}
}
