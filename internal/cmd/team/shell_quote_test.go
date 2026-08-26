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
		if got := quoteForShell(tc.in, false); got != tc.want {
			t.Errorf("quoteForShell(%q, posix) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// WINDOWS needs a different rule, not the same one applied harder (PR #528
// review, @codex). goreleaser publishes a native Windows binary, and POSIX
// single-quoting is actively WRONG there: every ordinary Windows path contains
// backslashes, cmd.exe treats single quotes as LITERAL characters, and the
// advertised ready-to-run retry would pass a quoted, nonexistent filename to
// --handoff-file.
//
// A backslash is a metacharacter on one platform and the path separator on the
// other, which is why the character sets differ rather than the quoting style
// alone.
func TestQuoteForShellWindows(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// The ordinary case: backslashes must NOT trigger quoting.
		{`C:\Users\me\AppData\Local\Temp\hadron-handoff-1.md`,
			`C:\Users\me\AppData\Local\Temp\hadron-handoff-1.md`},
		{"ses_01a03a33", "ses_01a03a33"},
		// A space still does, and with DOUBLE quotes, which cmd.exe honours.
		{`C:\Program Files\x.md`, `"C:\Program Files\x.md"`},
		// cmd metacharacters.
		{`C:\a&b`, `"C:\a&b"`},
		{`C:\a%PATH%`, `"C:\a%PATH%"`},
		{"", `""`},
	} {
		if got := quoteForShell(tc.in, true); got != tc.want {
			t.Errorf("quoteForShell(%q, windows) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// The cross-check that names the bug: a real Windows path must come out
	// UNQUOTED on Windows and would have been POSIX-quoted into nonsense.
	const winPath = `C:\Users\me\AppData\Local\Temp\h.md`
	if posix := quoteForShell(winPath, false); posix == winPath {
		t.Errorf("fixture check: this path must be one POSIX quoting would mangle, got %q", posix)
	}
	if win := quoteForShell(winPath, true); win != winPath {
		t.Errorf("an ordinary Windows path must not be quoted on Windows: %q", win)
	}
}
