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
