package output

import (
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
func SafeCell(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == cellMaxRunes {
			// Over the bound: drop the last kept rune for the ellipsis.
			out := []rune(b.String())
			return string(out[:cellMaxRunes-1]) + "…"
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
