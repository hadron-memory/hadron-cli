package team

import "testing"

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
