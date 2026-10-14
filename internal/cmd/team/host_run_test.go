package team

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// The server bounds failureReason by JS string length (UTF-16 units), so a
// cut by runes would let an astral-plane reason through over the limit.
func TestTruncateUTF16CountsLikeTheServer(t *testing.T) {
	got := truncateUTF16(strings.Repeat("😀", 1500), 2000) // 3000 units
	if n := len(utf16.Encode([]rune(got))); n > 2000 {
		t.Errorf("%d UTF-16 units, want <= 2000", n)
	}
	if s := "short"; truncateUTF16(s, 2000) != s {
		t.Error("a short reason is untouched")
	}
}
