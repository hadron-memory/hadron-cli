package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"
)

// SafeJSON renders a server-supplied JSON value for a terminal while keeping
// it valid JSON with the SAME value (hadron-cli#1524: `app properties get`
// prints the object so `> props.json` is a working file). It is indented two
// spaces, and inside strings every control character and every Unicode format
// or line/paragraph separator (Cf, Zl, Zp: bidi overrides, zero-width
// characters, U+2028/2029) is written as a \uXXXX escape (a surrogate pair
// above the BMP), so the text can neither recolor nor reorder the terminal.
// Structural whitespace from the indent is kept. Invalid JSON is an error.
func SafeJSON(raw []byte) (string, error) {
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return "", err
	}
	var b strings.Builder
	inString, escaped := false, false
	for _, r := range indented.String() {
		if !inString {
			if r == '"' {
				inString = true
			}
			b.WriteRune(r)
			continue
		}
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inString = false
		case unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp):
			if r > 0xFFFF {
				hi, lo := utf16.EncodeRune(r)
				fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
			} else {
				fmt.Fprintf(&b, `\u%04x`, r)
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}
