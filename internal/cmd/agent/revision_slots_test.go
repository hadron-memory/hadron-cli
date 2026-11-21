package agent

import (
	"strings"
	"testing"
)

// The edge of "exact" (QA on #2800): the largest whole number a JSON number
// carries exactly (2^53-1) is shown, and the server's own constant sits well
// below it; the next whole number is refused, because a larger float is no
// longer exact.
func TestParseSlotCountExactEdge(t *testing.T) {
	if n, err := parseSlotCount("slotLimit", 9007199254740991); err != nil || n != 9007199254740991 {
		t.Errorf("2^53-1 is exact: %d %v", n, err)
	}
	if n, err := parseSlotCount("slotLimit", 999999999999999); err != nil || n != 999999999999999 {
		t.Errorf("the server's constant is shown: %d %v", n, err)
	}
	if _, err := parseSlotCount("slotLimit", 9007199254740992); err == nil || !strings.Contains(err.Error(), "larger than this CLI can show") {
		t.Errorf("2^53 is not provably exact: %v", err)
	}
}
