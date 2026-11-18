package output

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// cli#2371 F2: SanitizeLine is the sanitize-only half of SafeLine. It
// neutralises exactly the characters SafeLine does and NEVER truncates, so a
// caller that must show a value in full (a task ref in a recovery message) says
// so by name instead of passing a rune bound that happens to be big enough.
func TestSanitizeLineNeutralisesLikeSafeLineAndNeverTruncates2371(t *testing.T) {
	t.Run("neutralises the same classes with one space each", func(t *testing.T) {
		for _, r := range []rune{0x00, 0x09, 0x0a, 0x0d, 0x1b, 0x7f, 0x85, 0x200b, 0x202e, 0x2028, 0x2029, 0xfeff} {
			if got := SanitizeLine("a" + string(r) + "b"); got != "a b" {
				t.Errorf("SanitizeLine(a%Ub) = %q, want %q", r, got, "a b")
			}
		}
	})
	t.Run("ordinary text, shell metacharacters and Unicode pass unchanged", func(t *testing.T) {
		for _, s := range []string{"", "AT1.2", "AT1.2;rm -rf $(x) 'q' \"z\" `y` | & < >", "héllo wörld 日本語 🙂"} {
			if got := SanitizeLine(s); got != s {
				t.Errorf("SanitizeLine(%q) = %q, want it unchanged", s, got)
			}
		}
	})
	t.Run("never truncates, however long", func(t *testing.T) {
		for _, n := range []int{1, 255, 256, 257, 1000, 100000} {
			s := strings.Repeat("é", n)
			got := SanitizeLine(s)
			if got != s || utf8.RuneCountInString(got) != n || strings.Contains(got, "…") {
				t.Fatalf("SanitizeLine of %d runes changed length or added an ellipsis (got %d runes)", n, utf8.RuneCountInString(got))
			}
		}
	})
	t.Run("agrees with SafeLine whenever SafeLine would not truncate", func(t *testing.T) {
		for _, s := range []string{"a\x1b[31mb\nc", "plain", "x" + string(rune(0x202e)) + "y" + string(rune(0x200b)) + "z", "日本語\t語"} {
			if a, b := SanitizeLine(s), SafeLine(s, utf8.RuneCountInString(s)); a != b {
				t.Errorf("SanitizeLine(%q) = %q but SafeLine = %q", s, a, b)
			}
		}
	})
}
