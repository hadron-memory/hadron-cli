package edge

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// A node's edges as `edge list` builds them: two out, two in, plus one whose
// far endpoint the server redacted (#781).
func sampleEdges() []edgeListDTO {
	return []edgeListDTO{
		{ID: "e1", Direction: "outgoing", Name: "routes-to", OtherID: "n1", OtherLoc: "findings:race"},
		{ID: "e2", Direction: "outgoing", Name: "to diagnose a slow query", OtherID: "n2", OtherLoc: "findings:slow"},
		{ID: "e3", Direction: "incoming", Name: "Applies when a resolver changes", OtherID: "n3", OtherLoc: "review:thin-resolver"},
		{ID: "e4", Direction: "incoming", Name: "child-of", OtherID: "n4", OtherLoc: "review:posthog"},
		{ID: "e5", Direction: "outgoing", Name: "routes-to"}, // redacted endpoint: no loc, no id
	}
}

func ids(es []edgeListDTO) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFilterEdges(t *testing.T) {
	cases := []struct {
		name string
		fl   edgeFilter
		want []string
	}{
		{"zero value matches everything", edgeFilter{}, []string{"e1", "e2", "e3", "e4", "e5"}},
		{"direction outgoing", edgeFilter{Direction: "outgoing"}, []string{"e1", "e2", "e5"}},
		{"direction incoming", edgeFilter{Direction: "incoming"}, []string{"e3", "e4"}},
		{"name substring", edgeFilter{Name: "routes-to"}, []string{"e1", "e5"}},
		{"name is case-insensitive", edgeFilter{Name: "APPLIES WHEN"}, []string{"e3"}},
		{"name is a substring, not a prefix", edgeFilter{Name: "resolver"}, []string{"e3"}},
		{"to, by loc", edgeFilter{To: "findings:slow"}, []string{"e2"}},
		{"to, by id", edgeFilter{To: "n2"}, []string{"e2"}},
		{"from, by loc", edgeFilter{From: "review:posthog"}, []string{"e4"}},
		{"from, by id", edgeFilter{From: "n3"}, []string{"e3"}},
		// --to is outgoing-only: an incoming edge from that node must not match.
		{"to does not match an incoming endpoint", edgeFilter{To: "review:posthog"}, []string{}},
		{"from does not match an outgoing endpoint", edgeFilter{From: "findings:slow"}, []string{}},
		{"combined", edgeFilter{Direction: "outgoing", Name: "routes-to"}, []string{"e1", "e5"}},
		{"no match", edgeFilter{Name: "nothing-like-this"}, []string{}},
	}
	for _, tc := range cases {
		got := ids(filterEdges(sampleEdges(), tc.fl))
		if !eq(got, tc.want) {
			t.Errorf("%s:\n  got  %v\n  want %v", tc.name, got, tc.want)
		}
	}
}

// A redacted endpoint carries neither loc nor id, so it must never satisfy a
// request for a specific node — matching on "" would make every such edge look
// like the one asked for.
func TestRedactedEndpointNeverMatches(t *testing.T) {
	redacted := edgeListDTO{ID: "e5", Direction: "outgoing", Name: "routes-to"}
	if endpointIs(redacted, "") {
		t.Error(`an empty ref must not match a redacted endpoint`)
	}
	if got := filterEdges([]edgeListDTO{redacted}, edgeFilter{To: "anything"}); len(got) != 0 {
		t.Errorf("redacted endpoint matched --to: %v", ids(got))
	}
	// It still shows up unfiltered, and under a name filter.
	if got := filterEdges([]edgeListDTO{redacted}, edgeFilter{Name: "routes-to"}); len(got) != 1 {
		t.Error("a redacted endpoint should still be listable by label")
	}
}

// filterEdges must return a non-nil slice so --json renders [] rather than null.
func TestFilterEdgesNeverReturnsNil(t *testing.T) {
	if got := filterEdges(nil, edgeFilter{}); got == nil {
		t.Error("nil input produced a nil slice")
	}
	if got := filterEdges(sampleEdges(), edgeFilter{Name: "no-match"}); got == nil {
		t.Error("an empty result produced a nil slice")
	}
}

// Combinations that can only ever match nothing are rejected up front, rather
// than returning an empty list that reads like "no such edges".
func TestEdgeFilterValidate(t *testing.T) {
	ok := []edgeFilter{
		{},
		{Direction: "incoming"},
		{Direction: "outgoing"},
		{To: "x"},
		{From: "x"},
		{Direction: "outgoing", To: "x"},
		{Direction: "incoming", From: "x"},
		{Name: "x", Direction: "incoming"},
	}
	for _, fl := range ok {
		if err := fl.validate(); err != nil {
			t.Errorf("%+v should be valid, got %v", fl, err)
		}
	}

	bad := []struct {
		fl   edgeFilter
		want string
	}{
		{edgeFilter{Direction: "both"}, "--direction"},
		{edgeFilter{Direction: "Incoming"}, "--direction"}, // case-sensitive by design
		{edgeFilter{To: "x", From: "y"}, "mutually exclusive"},
		{edgeFilter{To: "x", Direction: "incoming"}, "--to selects outgoing"},
		{edgeFilter{From: "x", Direction: "outgoing"}, "--from selects incoming"},
	}
	for _, tc := range bad {
		err := tc.fl.validate()
		if err == nil {
			t.Errorf("%+v should be rejected", tc.fl)
			continue
		}
		if got := exitcode.FromError(err); got != exitcode.Usage {
			t.Errorf("%+v should be a usage error, got %d", tc.fl, got)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: error should mention %q, got %q", tc.fl, tc.want, err.Error())
		}
	}
}
