package coding

import "testing"

func ptrBool(b bool) *bool { return &b }

// The five node shapes the predicate has to separate, taken from
// micromentor.org::mmdata. See docs/plans/coding-command-group.md, Decision 1.
func TestIsChecklistItem(t *testing.T) {
	cases := []struct {
		name string
		node checkNode
		want bool
	}{
		{"tagged review", checkNode{Loc: "review:thin-resolver-field", Tags: []string{"review", "graphql"}}, true},
		// 17 of mmdata's 48 checks carry no tags at all; a tag-only rule
		// silently ignored 35% of the checklist, including the misfiled
		// format-sources that motivated the command.
		{"untagged but under review:", checkNode{Loc: "review:format-sources"}, true},
		// review:backlog shares the review: prefix, so only the meta tag keeps
		// it out — the case that makes a prefix-only rule wrong.
		{"meta backlog", checkNode{Loc: "review:backlog", Tags: []string{"review", "meta"}}, false},
		{"runnable task", checkNode{Loc: "tasks:review-changes", IsRunnable: true}, false},
		{"pattern node", checkNode{Loc: "patterns:function-signatures", Tags: []string{"conventions"}}, false},
		{"router", checkNode{Loc: "preflight"}, false},
		{"runnable even under review:", checkNode{Loc: "review:something", IsRunnable: true}, false},
		// A findings node carries the `review` tag because it is
		// review-relevant, not because it is a check. Both mmdata and
		// hadron-portal have exactly one, and treating the tag as an
		// alternative to the prefix reported both as missing a parent edge.
		{"tagged finding is not a check", checkNode{Loc: "findings:some-race", Tags: []string{"review", "gotcha"}}, false},
	}
	for _, tc := range cases {
		if got := isChecklistItem("review", tc.node); got != tc.want {
			t.Errorf("%s: isChecklistItem(%q, tags=%v, runnable=%v) = %v, want %v",
				tc.name, tc.node.Loc, tc.node.Tags, tc.node.IsRunnable, got, tc.want)
		}
	}
}

// The listing projection must agree with the full-node predicate, or the
// pre-filter would drop nodes the engine would have kept.
func TestListingPredicateAgrees(t *testing.T) {
	cases := []checkNode{
		{Loc: "review:a", Tags: []string{"review"}},
		{Loc: "review:b"},
		{Loc: "review:backlog", Tags: []string{"review", "meta"}},
		{Loc: "tasks:t", IsRunnable: true},
		{Loc: "patterns:p", Tags: []string{"conventions"}},
	}
	for _, n := range cases {
		full := isChecklistItem("review", n)
		listing := isChecklistItemListing("review", n.Loc, n.Tags, ptrBool(n.IsRunnable))
		if full != listing {
			t.Errorf("%s: full=%v listing=%v — predicates disagree", n.Loc, full, listing)
		}
	}
	// A nil isRunnable means unset, which most nodes leave that way.
	if !isChecklistItemListing("review", "review:x", nil, nil) {
		t.Error("nil isRunnable should not disqualify")
	}
}
