package billing

import (
	"strings"
	"testing"
)

// cli#1560: a confirmation prompt shows the whole operative value, never a cut
// one, while still neutralising control characters.
func TestPromptValueNeverTruncates(t *testing.T) {
	long := strings.Repeat("9", 1000)
	if got := promptValue(long); got != long {
		t.Errorf("a long value must be shown whole, got %d runes", len([]rune(got)))
	}
	if got := promptValue("a\x1b[31mb\nc"); strings.ContainsAny(got, "\x1b\n") {
		t.Errorf("control characters must be neutralised: %q", got)
	}
}

// cli#1562: Cf (bidi/zero-width) and line/paragraph separators are neutralised
// in a prompt too, so an RLO cannot visually reorder the amount an operator
// is confirming.
func TestPromptValueNeutralisesFormatCharacters(t *testing.T) {
	for name, in := range map[string]string{
		"rlo":  "12\u202e34",
		"zwsp": "12\u200b34",
		"lrm":  "12\u200e34",
		"zl":   "12\u202834",
		"zp":   "12\u202934",
	} {
		got := promptValue(in)
		if got != "12 34" {
			t.Errorf("%s: want the format rune replaced by a space, got %q", name, got)
		}
	}
}
