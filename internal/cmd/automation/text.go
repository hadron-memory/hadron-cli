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
//
// Two sets of bounds. Names, headlines and table cells use the small ones. The
// CONSENT projection (approval/approve) uses the large ones, and when anything
// in it would still be cut, approve refuses instead of letting a person confirm
// a hash over authority they were not shown (BLIND P2 on cli#1076).
const (
	maxName  = 120 // trigger and flow names, table cells
	maxLine  = 300 // a headline in a list or an ambiguity refusal
	maxDepth = 8000

	maxFact  = 1000 // one sentence of the consent projection
	maxFacts = 200  // entries of one list of the consent projection
)

var (
	// OSC (ESC ] … BEL|ST), CSI (ESC [ … final byte) and any other ESC + one
	// character. Removed whole, so no parameter bytes are left behind as text.
	reOSC = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?`)
	reCSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]?`)
	reESC = regexp.MustCompile(`\x1b.?`)
)

// hidden reports a character that must not reach a terminal as itself: a
// control (newline, tab, NUL, C1…), a Unicode format character (bidi overrides
// and isolates, zero-width), or a line/paragraph separator. Graph-authored text
// could otherwise reorder or hide what an approval display says.
func hidden(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

// safeText returns s with terminal escape sequences removed, every hidden
// character turned into a space, and the result cut to max runes with an
// ellipsis.
func safeText(s string, max int) string {
	out, _ := safeTextCut(s, max)
	return out
}

// safeTextCut is safeText that also reports whether it cut the text.
func safeTextCut(s string, max int) (string, bool) {
	s = reOSC.ReplaceAllString(s, "")
	s = reCSI.ReplaceAllString(s, "")
	s = reESC.ReplaceAllString(s, "")
	var b strings.Builder
	n := 0
	for _, r := range s {
		if hidden(r) {
			r = ' '
		}
		if n == max {
			b.WriteString("…")
			return b.String(), true
		}
		b.WriteRune(r)
		n++
	}
	return b.String(), false
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
		if r != '\n' && hidden(r) {
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
