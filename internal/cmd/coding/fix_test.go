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
