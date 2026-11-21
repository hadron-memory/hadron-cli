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
		{"bidi overrides and separators become spaces", "a\u202eb\u2028c\u2029d", "a b c d"},
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

// cli#2846 item 1: an OSC with no terminator ends at the end of ITS LINE, not the end of the
// string, so the renderer's own lines after a server message (a recovery command on its own
// last line) survive. A terminated OSC is still removed whole.
func TestSafeErrorBoundsAnUnterminatedOSCAtTheEndOfItsLine2846(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"a recovery line after an unterminated 7-bit OSC", "server said \x1b]0;pwned\nrun: hadron auth login", "server said \nrun: hadron auth login"},
		{"a recovery line after an unterminated 8-bit OSC", "server said \u009d0;pwned\nrun: hadron auth login", "server said \nrun: hadron auth login"},
		{"several lines after it", "a\x1b]8;;http://x\nl2\nl3", "a\nl2\nl3"},
		{"a terminated OSC (BEL) is still removed whole", "a\x1b]0;title\x07b\nnext", "ab\nnext"},
		{"a terminated OSC (ST) is still removed whole", "a\x1b]8;;http://x\x1b\\b\nnext", "ab\nnext"},
		{"an unterminated OSC on the last line still ends the string", "ok\nbad\x1b]0;title", "ok\nbad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SafeError(tc.in); got != tc.want {
				t.Errorf("SafeError(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// cli#2846 item 2 (decided): a zero-width joiner or non-joiner is a format character and
// becomes a space, so a ZWJ emoji sequence and ZWNJ text split. That is the documented cost
// (as in SafeCell); ordinary emoji are unchanged. This pins the behaviour the agentic-usage
// text now states.
func TestSafeErrorSplitsZeroWidthJoinerSequences2846(t *testing.T) {
	if got, want := SafeError("family \U0001F468\u200d\U0001F469\u200d\U0001F467 here"), "family \U0001F468 \U0001F469 \U0001F467 here"; got != want {
		t.Errorf("a ZWJ emoji sequence splits: got %q want %q", got, want)
	}
	if got, want := SafeError("a\u200cb"), "a b"; got != want {
		t.Errorf("a ZWNJ becomes a space: got %q want %q", got, want)
	}
	if got := SafeError("\U0001F680 \u2713 \U0001F468"); got != "\U0001F680 \u2713 \U0001F468" {
		t.Errorf("ordinary emoji pass unchanged: %q", got)
	}
}
