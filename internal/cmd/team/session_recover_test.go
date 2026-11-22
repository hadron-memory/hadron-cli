package team

import (
	"strings"
	"testing"
)

func TestStandingDirectionsSection(t *testing.T) {
	cases := []struct {
		name, note, want string
		ok               bool
	}{
		{"between headings", "# S\nx\n\n## Standing directions\nA\nB\n\n## Other\nC\n", "A\nB", true},
		{"to the end", "## Standing Directions\nonly\n", "only", true},
		{"deeper heading stays inside", "## Standing directions\nA\n### sub\nB\n## Next\nC", "A\n### sub\nB", true},
		{"higher heading ends it", "### Standing directions\nA\n## Up\nB", "A", true},
		{"absent", "# S\nnothing here\n", "", false},
		{"not a heading", "#Standing directions\nA", "", false},
	}
	for _, c := range cases {
		got, ok := standingDirections(c.note)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got (%q,%v), want (%q,%v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestRenderRecoverSaysTheTaskPageDidNotEnd2734 pins the recover text's
// TasksMore line (BLIND round 2, cli#2734): TasksMore is the server's hasMore,
// not a full-page guess, so the line says the page did not end — "a full
// page" was wrong for a short page — and is said only when true.
func TestRenderRecoverSaysTheTaskPageDidNotEnd2734(t *testing.T) {
	for _, tc := range []struct {
		name string
		more bool
	}{
		{"hasMore true", true},
		{"hasMore false", false},
	} {
		var b strings.Builder
		if err := renderRecover(&b, recoverDTO{TasksMore: tc.more}); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := b.String()
		if strings.Contains(got, "full page: more tasks") {
			t.Errorf("%s: the old full-page guess is gone from the task line: %q", tc.name, got)
		}
		if !tc.more && strings.Contains(got, "more tasks may exist") {
			t.Errorf("%s: hasMore false says nothing about more tasks: %q", tc.name, got)
		}
		if tc.more && !strings.Contains(got, "(the task page did not end: more tasks may exist)") {
			t.Errorf("%s: hasMore true says the page did not end: %q", tc.name, got)
		}
	}
}
