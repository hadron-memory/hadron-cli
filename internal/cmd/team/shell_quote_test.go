package team

import "testing"

// The retry line printed by rescueHandoff advertises itself as ready-to-run, so
// it has to BE runnable (PR #528 review, @codex P2 + @copilot).
//
// os.CreateTemp returns whatever TMPDIR points at, and a temp directory
// containing a space is ordinary rather than exotic — the default shape on
// Windows, one mistake away anywhere. Unquoted, the path splits into extra
// arguments and recovery fails at the exact moment it is the only thing left.
func TestShellQuote(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// Left alone: the common case stays legible and copy-pasteable, which
		// is worth more than uniform quoting.
		{"/tmp/hadron-handoff-1.md", "/tmp/hadron-handoff-1.md"},
		{"ses_01a03a33", "ses_01a03a33"},
		// Quoted. The space is the one that actually happens.
		{"/Users/me/Application Support/x.md", "'/Users/me/Application Support/x.md'"},
		{"/tmp/a$b", "'/tmp/a$b'"},
		{"/tmp/a;rm -rf /", "'/tmp/a;rm -rf /'"},
		// A single quote is the one character single-quoting cannot carry
		// literally, so it takes the close-escape-reopen form.
		{"/tmp/it's.md", `'/tmp/it'\''s.md'`},
		// Empty quotes to '' rather than vanishing into nothing, which would
		// silently drop an argument.
		{"", "''"},
	} {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
