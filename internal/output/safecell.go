package output

import (
	"regexp"
	"strings"
	"unicode"
)

// cellMaxRunes is the RENDERED limit of one text-table cell, the ellipsis
// included: a longer value keeps cellMaxRunes-1 runes and ends in "…".
const cellMaxRunes = 256

// SafeCell makes a server-supplied string safe to print as one table cell
// (#1065). Every control character (C0, C1, DEL, ESC, tab, CR, LF) and every
// Unicode format or line/paragraph separator (Cf, Zl, Zp: bidi overrides,
// zero-width characters, BOM, U+2028/2029) becomes a single space, so a value
// can neither forge a row nor recolor or reorder the terminal. Ordinary
// Unicode passes unchanged. The text is cut by rune at cellMaxRunes, which can
// split a combining sequence. ZWJ (U+200D) is Cf, so a ZWJ emoji sequence
// renders as separate emoji; that is the accepted cost of the stricter rule.
//
// Text mode only: --json carries the original value. Other commands' tables are
// unchanged (roster, automation and member keep their own guards).
func SafeCell(s string) string { return SafeLine(s, cellMaxRunes) }

// SafeLine is SafeCell with the caller's rune bound, for one-line messages
// (an error refusal) that need more room than a table cell.
func SafeLine(s string, maxRunes int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == maxRunes {
			// Over the bound: drop the last kept rune for the ellipsis.
			out := []rune(b.String())
			return string(out[:maxRunes-1]) + "…"
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// SafeBlock bounds server-authored multiline text without flattening its lines.
// Escape sequences are removed before counting runes. CRLF becomes LF; every
// other control/format/separator becomes a space. Continuation indentation is
// CLI-owned. JSON callers must retain the original value. Like SafeCell, a cut
// may split a combining sequence; there is no separate byte bound.
func SafeBlock(s string, maxRunes int, indent string) string {
	s = blockOSC.ReplaceAllString(s, "")
	s = blockCSI.ReplaceAllString(s, "")
	s = blockESC.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= maxRunes {
			b.WriteString("…\n" + indent + "(truncated — --json carries the whole value)")
			return b.String()
		}
		if r == '\n' {
			b.WriteRune(r)
			b.WriteString(indent)
		} else {
			if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
				r = ' '
			}
			b.WriteRune(r)
		}
		n++
	}
	return b.String()
}

var (
	blockOSC = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?`)
	blockCSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]?`)
	blockESC = regexp.MustCompile(`\x1b.?`)
)
