package question

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// wrapHelp keeps shared one-line sentences inside the 80-column help (hadron-cli#2995).
func TestWrapHelpWidthAndContent2995(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		width    int
		want     []string
	}{
		{"empty", "", 10, nil},
		{"fits", "one two", 10, []string{"one two"}},
		{"exactly the width", "aaaa bbbb", 9, []string{"aaaa bbbb"}},
		{"one over", "aaaa bbbb", 8, []string{"aaaa", "bbbb"}},
		{"a word longer than width stays whole", "tiny enormousword x", 5, []string{"tiny", "enormousword", "x"}},
		{"runes, not bytes", "✓✓✓ ✓✓✓", 7, []string{"✓✓✓ ✓✓✓"}},
		{"a backticked command is never split", "run `a b c` now", 8, []string{"run", "`a b c`", "now"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapHelp(tc.in, tc.width)
			var lines []string
			if got != "" {
				lines = strings.Split(got, "\n")
			}
			if strings.Join(lines, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("wrapHelp(%q, %d) = %q, want %q", tc.in, tc.width, lines, tc.want)
			}
			for _, l := range lines {
				if utf8.RuneCountInString(l) > tc.width && !strings.ContainsAny(l, "`") && !strings.Contains(l, "enormousword") {
					t.Errorf("line over width: %q", l)
				}
			}
		})
	}
}

// The recovery advice names askedBy and askedAt because `question list --json` carries them
// (the text table has no such columns).
func TestQuestionJSONCarriesTheFieldsTheRecoveryNames2995(t *testing.T) {
	b, err := json.Marshal(questionDTO{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"askedBy"`, `"askedAt"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("questionDTO JSON lacks %s: %s", key, b)
		}
	}
}
