package team

import (
	"strings"
	"testing"
)

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

// Windows gets NO quoting from this function, and that is the point.
//
// Three attempts failed here (PR #528 review, @codex, three times): POSIX
// single quotes are literal characters to cmd.exe and backslashes are its path
// separator; double quotes still let cmd.exe expand %VAR% and PowerShell expand
// $var, so a summary containing either is silently altered by the command that
// promised to reproduce the invocation; and a Go process cannot tell which of
// the two shells the text will be pasted into.
//
// There is no rule that serves both, so the fix is not better escaping — it is
// not claiming to have escaped. retryLine prints the arguments as DATA on
// Windows rather than as a command line, and this function stays honestly
// POSIX-only. Its doc comment carries the reasoning so the next person does not
// re-derive the double-quote idea and ship it.
func TestShellQuoteIsPOSIXOnlyByDesign(t *testing.T) {
	// A value that BOTH Windows shells would mangle inside double quotes. If
	// someone reintroduces a Windows branch here, this is the case to think
	// about first.
	const hostile = `fixed %PATH% and $env:HOME handling`
	got := shellQuote(hostile)
	if got != `'`+hostile+`'` {
		t.Errorf("POSIX single-quoting must make it literal: %q", got)
	}
	// Single quotes are what make that true, and nothing else would.
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("the POSIX guarantee rests on single quotes: %q", got)
	}
}
