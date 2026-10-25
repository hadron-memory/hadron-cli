package automation

import (
	"regexp"
	"strings"
	"unicode"
)

// Server text — summary sentences, trigger and flow names, headlines — is
// authored by other people (graph content), so it is untrusted: a terminal
// escape in it must not run, and a huge value must not flood the screen.
// Text mode strips and bounds every server string it prints; --json passes the
// server's value through unchanged.
const (
	maxName  = 120 // trigger and flow names
	maxLine  = 300 // one sentence or headline
	maxItems = 25  // entries printed from one list before "and N more"
	maxDepth = 8000
)

var (
	// OSC (ESC ] … BEL|ST), CSI (ESC [ … final byte) and any other ESC + one
	// character. Removed whole, so no parameter bytes are left behind as text.
	reOSC = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?`)
	reCSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]?`)
	reESC = regexp.MustCompile(`\x1b.?`)
)

// safeText returns s with terminal escape sequences removed, every remaining
// control character (newline, tab, NUL, C1…) turned into a space, and the result
// cut to max runes with an ellipsis.
func safeText(s string, max int) string {
	s = reOSC.ReplaceAllString(s, "")
	s = reCSI.ReplaceAllString(s, "")
	s = reESC.ReplaceAllString(s, "")
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			r = ' '
		}
		if n == max {
			b.WriteString("…")
			return b.String()
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// safeBlock is safeText for a multi-line value (the pretty-printed graph):
// newlines survive, every other control character and escape does not.
func safeBlock(s string, max int) string {
	s = reOSC.ReplaceAllString(s, "")
	s = reCSI.ReplaceAllString(s, "")
	s = reESC.ReplaceAllString(s, "")
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r != '\n' && unicode.IsControl(r) {
			r = ' '
		}
		if n == max {
			b.WriteString("…\n(truncated — `--json` carries the whole graph)")
			return b.String()
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// shellQuote quotes s for a copy-pasteable command in an error hint.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789:._/@-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
