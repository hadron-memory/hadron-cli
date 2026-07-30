package coding

import "testing"

func route(target, label string) graphEdge {
	return graphEdge{ID: "edg_" + target, Label: label, Other: target, MemoryID: "org::mem"}
}

func TestRouteLabelPhrasing(t *testing.T) {
	cases := []struct{ label, want string }{
		{"to review your code before opening a PR", ""},
		{"To debug a failing run", ""}, // case-insensitive
		{"routes-to", "route-label-phrasing"},
		{"", "route-label-phrasing"},
		{"related", "route-label-phrasing"},
	}
	for _, tc := range cases {
		in := preflightInput{
			Routes:     []graphEdge{route("findings:x", tc.label)},
			Targets:    map[string]checkNode{"findings:x": {Loc: "findings:x"}},
			HomeMemory: "org::mem",
		}
		rules := rulesFor(lintPreflight(in), "findings:x")
		switch {
		case tc.want == "" && len(rules) > 0:
			t.Errorf("label %q: expected clean, got %v", tc.label, rules)
		case tc.want != "" && !has(rules, tc.want):
			t.Errorf("label %q: expected %s, got %v", tc.label, tc.want, rules)
		}
	}
}

// Stale routing is worse than missing routing, so an unresolvable target is the
// one error on this side — unlike the review tree, where an unreadable endpoint
// only warns.
func TestRouteTargetUnresolvableIsError(t *testing.T) {
	in := preflightInput{
		Routes:      []graphEdge{route("findings:gone", "to do the thing")},
		Targets:     map[string]checkNode{},
		Unavailable: []string{"findings:gone"},
		HomeMemory:  "org::mem",
	}
	fs := lintPreflight(in)
	if len(fs) != 1 || fs[0].Rule != "route-target-resolves" {
		t.Fatalf("expected route-target-resolves, got %v", fs)
	}
	if fs[0].Severity != sevError {
		t.Errorf("a dead route should be an error, got %q", fs[0].Severity)
	}
}

// Once a target is known unreadable, the convention checks have nothing to say
// about it — piling on would triple-report one broken route.
func TestUnresolvableTargetSuppressesOtherRules(t *testing.T) {
	in := preflightInput{
		Routes:      []graphEdge{route("findings:gone", "routes-to")},
		Unavailable: []string{"findings:gone"},
		HomeMemory:  "org::mem",
	}
	fs := lintPreflight(in)
	if len(fs) != 1 {
		t.Fatalf("expected exactly one finding for a dead route, got %v", fs)
	}
}

func TestRouteTargetRetired(t *testing.T) {
	for _, tag := range retiredTags {
		in := preflightInput{
			Routes:     []graphEdge{route("findings:old", "to do the thing")},
			Targets:    map[string]checkNode{"findings:old": {Loc: "findings:old", Tags: []string{tag}}},
			HomeMemory: "org::mem",
		}
		if !has(rulesFor(lintPreflight(in), "findings:old"), "route-target-retired") {
			t.Errorf("tag %q should trigger route-target-retired", tag)
		}
	}
}

func TestRouteTargetMovedMemory(t *testing.T) {
	r := route("findings:moved", "to do the thing")
	r.MemoryID = "other.org::elsewhere"
	in := preflightInput{
		Routes:     []graphEdge{r},
		Targets:    map[string]checkNode{"findings:moved": {Loc: "findings:moved"}},
		HomeMemory: "org::mem",
	}
	if !has(rulesFor(lintPreflight(in), "findings:moved"), "route-target-moved-memory") {
		t.Error("expected route-target-moved-memory")
	}

	// An opaque PK can't be compared against a URN-shaped scope; don't guess.
	r.MemoryID = "019f76f283c27bc39c7f906c798e4268"
	in.Routes = []graphEdge{r}
	if has(rulesFor(lintPreflight(in), "findings:moved"), "route-target-moved-memory") {
		t.Error("a PK-vs-URN comparison must not produce a finding")
	}
}

func TestPreflightCleanRouter(t *testing.T) {
	in := preflightInput{
		Routes: []graphEdge{
			route("findings:a", "to diagnose a slow query"),
			route("findings:b", "to trace an auth failure"),
		},
		Targets: map[string]checkNode{
			"findings:a": {Loc: "findings:a"},
			"findings:b": {Loc: "findings:b"},
		},
		HomeMemory: "org::mem",
	}
	if fs := lintPreflight(in); len(fs) != 0 {
		t.Errorf("a clean router should yield no findings, got %v", fs)
	}
}
