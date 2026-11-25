package spec

import (
	"strings"
	"testing"
)

// #3173 F5: a lost create with no number reported still names the replacement, and never --to when it was not given.
func TestReplacementNameWithoutANumber3114(t *testing.T) {
	three := 3
	for name, tc := range map[string]struct {
		named bool
		seq   *int
		want  string
	}{
		"no number":         {false, nil, "the replacement (the citation given is not shown)"},
		"number":            {false, &three, "the replacement numbered 03 under its parent (the citation given is not shown)"},
		"named with --to":   {true, nil, "the replacement named with --to (not shown)"},
		"--to beats number": {true, &three, "the replacement named with --to (not shown)"},
	} {
		if got := replacementName(tc.named, tc.seq); got != tc.want || strings.Contains(got, "--to") != tc.named {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
