package output

import "testing"

func TestSafeError2739(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"ordinary text is unchanged", "memory not found: acme.com:kb", "memory not found: acme.com:kb"},
		{"accents CJK and emoji are unchanged", "héllo 日本語 ✓ 🚀", "héllo 日本語 ✓ 🚀"},
		{"a code and a request id are unchanged", "GRANT_NOT_MANAGER req_01a1f0c2d3e4", "GRANT_NOT_MANAGER req_01a1f0c2d3e4"},
		{"a 7-bit CSI is removed whole", "a\x1b[31mb\x1b[0mc", "abc"},
		{"a CSI with intermediate bytes", "a\x1b[1;2 qb", "ab"},
		{"an OSC ended by BEL", "a\x1b]0;title\x07b", "ab"},
		{"an OSC ended by ST", "a\x1b]8;;http://x\x1b\\b", "ab"},
		{"an unterminated OSC", "a\x1b]0;title", "a"},
		{"an 8-bit CSI is removed whole", "a\u009b31mb", "ab"},
		{"an 8-bit OSC ended by C1 ST", "a\u009d0;t\u009cb", "ab"},
		{"a lone ESC and its next byte", "a\x1bcb", "ab"},
		{"a trailing ESC", "ab\x1b", "ab"},
		{"BEL NUL DEL become spaces", "a\x07b\x00c\x7fd", "a b c d"},
		{"a newline is kept (the renderer's own messages are multi-line); a carriage return is not", "a\nb\r\nc", "a\nb \nc"},
		{"a tab becomes a space", "a\tb", "a b"},
		{"bidi overrides and separators become spaces", "a‮b c d", "a b c d"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SafeError(tc.in); got != tc.want {
				t.Errorf("SafeError(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// SafeError never truncates: a long message arrives whole.
func TestSafeErrorDoesNotTruncate2739(t *testing.T) {
	long := ""
	for i := 0; i < 5000; i++ {
		long += "abcdefghij"
	}
	if got := SafeError(long); got != long {
		t.Errorf("a %d-rune message must pass whole, got %d runes", len(long), len([]rune(got)))
	}
}

func TestSafeJSONText2739(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"ordinary text is unchanged", "héllo 日本語 ✓ 🚀 GRANT_NOT_MANAGER", "héllo 日本語 ✓ 🚀 GRANT_NOT_MANAGER"},
		{"the C0 controls are left to the encoder", "a\x1b[31mb\x07c", "a\x1b[31mb\x07c"},
		{"an 8-bit CSI introducer becomes a space", "a\u009b31mb", "a 31mb"},
		{"an 8-bit OSC introducer becomes a space", "a\u009d0;tb", "a 0;tb"},
		{"bidi overrides and zero-width characters become spaces", "a\u202eb\u200bc\ufeffd", "a b c d"},
		{"the whole C1 range", "\u0080\u0085\u009f", "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SafeJSONText(tc.in); got != tc.want {
				t.Errorf("SafeJSONText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
