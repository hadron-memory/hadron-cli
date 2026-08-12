package coding

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestRouteTargetLoc(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"findings branch", "findings:flaky-otp-timer", "findings:flaky-otp-timer", false},
		{"deep loc", "ops:incidents:20260517-p3009", "ops:incidents:20260517-p3009", false},
		{"single segment", "architecture", "architecture", false},
		{"under the router's own prefix", "preflight:flavors", "preflight:flavors", false},
		{"trims space", "  findings:x  ", "findings:x", false},
		{"the router itself rejected", "preflight", "", true},
		{"empty rejected", "   ", "", true},
		{"bad segment rejected", "findings:Not A Slug", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := routeTargetLoc("preflight", tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("routeTargetLoc(%q) = %q, want an error", tc.in, got)
				}
				if code := exitcode.FromError(err); code != exitcode.Usage {
					t.Errorf("a bad loc is a usage error (2), got %d", code)
				}
				return
			}
			if err != nil {
				t.Fatalf("routeTargetLoc(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("routeTargetLoc(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeRoute(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"stem prepended", "fix a flaky test", "to fix a flaky test", false},
		{"stem kept", "to fix a flaky test", "to fix a flaky test", false},
		{"stem case-insensitive", "To fix a flaky test", "To fix a flaky test", false},
		{"whitespace collapsed", "  fix  a\nflaky test ", "to fix a flaky test", false},
		// "touch" starts with "to" but not with the stem "to ", so it is an
		// action word, not a stem — prepending is correct.
		{"to-prefixed word is not the stem", "touch the schema", "to touch the schema", false},
		{"bare stem rejected", "to", "", true},
		{"bare stem with space rejected", "to   ", "", true},
		{"empty rejected", "  ", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeRoute(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizeRoute(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeRoute(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("normalizeRoute(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRouteSymptom(t *testing.T) {
	if got := routeSymptom("", "to fix a flaky test"); got != "To fix a flaky test" {
		t.Errorf("the default symptom is the route, sentence-cased; got %q", got)
	}
	if got := routeSymptom("  I need to fix a flaky test  ", "to fix a flaky test"); got != "I need to fix a flaky test" {
		t.Errorf("--symptom must win verbatim, got %q", got)
	}
}

// The point of the command: what it writes must satisfy the linter that reads
// it. A route created here has to be clean against `preflight lint`'s own
// rules, or the tool contradicts the tool.
func TestCreatedRouteLintsClean(t *testing.T) {
	loc, err := routeTargetLoc("preflight", "findings:flaky-otp-timer")
	if err != nil {
		t.Fatalf("routeTargetLoc: %v", err)
	}
	label, err := normalizeRoute("fix a flaky OTP countdown test")
	if err != nil {
		t.Fatalf("normalizeRoute: %v", err)
	}
	in := preflightInput{
		Routes:     []graphEdge{{ID: "e1", Label: label, OtherID: "n1", Other: loc, MemoryID: "mem1"}},
		Targets:    map[string]checkNode{loc: {Loc: loc, Name: checkName(loc), Description: "d"}},
		HomeMemory: "mem1",
	}
	if findings := lintPreflight(in); len(findings) != 0 {
		t.Errorf("a freshly-created route must lint clean, got %+v", findings)
	}
}

// A route target's tags vary by branch (findings vs conventions vs ops), so
// unlike a review check it gets no defaults imposed on it.
func TestRouteTagsHaveNoDefaults(t *testing.T) {
	if got := mergeTags(nil, nil); len(got) != 0 {
		t.Errorf("a route target should start with no tags, got %v", got)
	}
	got := mergeTags(nil, []string{"findings", "graphql", "findings"})
	if strings.Join(got, ",") != "findings,graphql" {
		t.Errorf("mergeTags = %v, want the caller's tags sorted and deduped", got)
	}
}

func TestScaffoldRouteBodyLeadsWithTheRule(t *testing.T) {
	body := scaffoldRouteBody("flaky-otp-timer", "The resend countdown must start before the network await")
	if !strings.HasPrefix(body, "# flaky-otp-timer\n\nThe resend countdown must start before the network await\n") {
		t.Errorf("the scaffold must lead with the name and the rule, got:\n%s", body)
	}
	for _, want := range []string{"## What you see", "## Why", "## Do / don't"} {
		if !strings.Contains(body, want) {
			t.Errorf("the scaffold is missing %q:\n%s", want, body)
		}
	}
}
