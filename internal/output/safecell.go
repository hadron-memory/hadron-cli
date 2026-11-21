package output

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
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

// Neutralised reports whether a rune is replaced by a space in terminal text:
// the control characters, the format characters (bidi overrides and isolates,
// directional marks, zero-width characters, the byte order mark, the soft
// hyphen) and the line and paragraph separators. It is the ONE classification:
// SafeLine applies it, and the typed wake line (internal/delivery) folds the same
// control and format characters, so a table and the agent's composer agree on
// which characters are neutralised (hadron-cli#2154). The two differ in what
// they do around that classification: the typed line also folds ordinary
// whitespace, collapses runs and trims the ends, while SafeLine keeps spacing
// as it is and may truncate to its rune bound.
func Neutralised(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

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
		if Neutralised(r) {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// SanitizeLine is SafeLine without a bound: it neutralises exactly the
// characters SafeLine does (one space each) and never truncates. A caller that
// must show a value in full, such as a task ref in a recovery message that a
// reader copies back into a command, says so by calling this instead of passing
// a rune bound that happens to be large enough (hadron-cli#2371). It delegates,
// so SafeLine, SafeCell and Neutralised stay the one classification.
func SanitizeLine(s string) string { return SafeLine(s, math.MaxInt) }

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

// Terminal escape sequences, in their 7-bit (ESC-introduced) and 8-bit (C1) forms. An OSC
// string ends at BEL, ST (ESC \) or the C1 ST; a CSI sequence ends at its final byte.
var (
	// Policy (cli#2846), not a terminal rule: an OSC string is bounded at the end of its line, so
	// one with no terminator cannot swallow the renderer's own lines after a server message. A
	// terminated OSC that spans a newline is therefore cut at that newline: its first line is
	// removed, the rest shows (with the stray terminator as a space).
	errOSC = regexp.MustCompile(`(?:\x1b\]|\x{9d})[^\x07\x1b\x{9c}\n]*(?:\x07|\x1b\\|\x{9c})?`)
	errCSI = regexp.MustCompile(`(?:\x1b\[|\x{9b})[0-?]*[ -/]*[@-~]?`)
	errESC = regexp.MustCompile(`\x1b.?`)
)

// SafeError makes server or provider error text safe to print on the terminal
// (cli#2739). It removes terminal escape sequences whole (a sequence's parameters are not
// left behind as visible junk), then turns every remaining control, format and line or
// paragraph separator character into a single space, so the text cannot act on the terminal
// or overwrite a line (a carriage return is a control character). A NEWLINE is kept: the
// renderer's own messages are legitimately multi-line (a recovery command alone on the last
// line), and a newline only starts another line, it does not move the cursor or recolour
// anything. Known limit, accepted: a server message can therefore still start an extra line.
// Ordinary text, including accents, CJK and emoji, passes byte for byte. It never truncates
// and is for TEXT only: an error code, a request id or an exit code is carried beside the
// message, not through this.
func SafeError(s string) string {
	s = errOSC.ReplaceAllString(s, "")
	s = errCSI.ReplaceAllString(s, "")
	s = errESC.ReplaceAllString(s, "")
	var b strings.Builder
	for _, r := range s {
		if r != '\n' && Neutralised(r) {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SafeJSONText is SafeError's counterpart for text that goes into a JSON document
// (cli#2739): the encoder already writes the C0 controls (an ESC, a BEL, a NUL) as \u
// escapes, so those stay as the server sent them and the value is unchanged for a program
// that decodes it. What the encoder leaves RAW, and a terminal that prints the document
// can still act on, becomes a space: the C1 controls (U+0080 to U+009F, which include the
// 8-bit CSI and OSC introducers) and the format characters (bidi overrides, zero-width
// characters). Ordinary text is unchanged.
func SafeJSONText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 0x80 && r <= 0x9f) || unicode.In(r, unicode.Cf) {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ExactJSONString is a string that marshals to a JSON string which DECODES to exactly the
// original, while its bytes carry no C1 control or format character raw: each is written as a
// \uXXXX escape, so a terminal showing the document cannot act on it (cli#2846). It is for the
// few values a caller must copy byte for byte (a staged secret change's request id and ceremony
// URL, cli#2738), where SafeJSONText's replacement with a space would corrupt the value.
type ExactJSONString string

func (s ExactJSONString) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(string(s))
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	for _, r := range string(b) {
		if (r >= 0x80 && r <= 0x9f) || unicode.In(r, unicode.Cf) {
			if r1, r2 := utf16.EncodeRune(r); r1 != unicode.ReplacementChar { // beyond the BMP: a surrogate pair
				fmt.Fprintf(&out, `\u%04x\u%04x`, r1, r2)
			} else {
				fmt.Fprintf(&out, `\u%04x`, r)
			}
			continue
		}
		out.WriteRune(r)
	}
	return []byte(out.String()), nil
}
