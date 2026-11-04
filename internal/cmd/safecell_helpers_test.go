package cmd

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// cli#1104 (#1093 slice 4): every server-supplied text-table cell goes through
// output.SafeCell, so a hostile value can neither forge a row nor recolor or
// reorder the terminal. These helpers are the shared shape of one red-first
// sanitize test per converted command; app_key_list_sanitize_test.go (#1065)
// is the original.

// hostileCellLabel is the gate's required hostile label: a newline (row
// forgery) plus an ESC color sequence (terminal recoloring).
const hostileCellLabel = "a\nb\x1b[31mc"

// longCellText exceeds SafeCell's 256-rune rendered bound (ellipsis included)
// in multibyte runes, so an uncut cell cannot slip through as ASCII-only.
var longCellText = strings.Repeat("日", 1000)

// cutCell is SafeCell's documented rendering of longCellText: 255 runes plus
// the ellipsis, 256 rendered runes in all.
var cutCellText = strings.Repeat("日", 255) + "…"

// assertSafeTableText checks the gate's three invariants for one command's
// text output: one logical line per table row (wantRows records plus the
// header), no hidden rune anywhere, and the long value cut, never uncut.
// wantRows < 0 skips the row-count check (key/value detail views); such
// callers MUST pin their line count with assertDetailLineCount, which is
// what catches a newline that stops being neutralized (cli#1110, Joshua
// P2-2: a detail test without it cannot see row forgery).
func assertSafeTableText(t *testing.T, text string, wantRows int, longCells int) {
	t.Helper()
	if wantRows >= 0 {
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		if len(lines) != wantRows+1 {
			t.Fatalf("want a header and one logical row per record (%d lines), got %d:\n%q", wantRows+1, len(lines), text)
		}
	}
	for _, r := range text {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)) {
			t.Fatalf("hidden rune %U survived in text output %q", r, text)
		}
	}
	if longCells > 0 {
		if strings.Contains(text, strings.Repeat("日", 257)) {
			t.Errorf("a long value was rendered uncut (257+ runes):\n%s", text)
		}
		if n := strings.Count(text, cutCellText); n != longCells {
			t.Errorf("want %d cell(s) cut to the documented 255+… form, got %d:\n%s", longCells, n, text)
		}
	}
	if !utf8.ValidString(text) {
		t.Errorf("text output must stay valid UTF-8")
	}
}

// assertDetailLineCount pins a key/value detail view's logical line count
// (cli#1110, Joshua P2-2): detail callers pass wantRows < 0, so no row-count
// discipline guards them, and a hostile newline that survived sanitization
// would silently forge an extra line. The pinned count is taken from the
// SANITIZED hostile output, so the pin fails exactly when a control rune
// stops being neutralized, while cosmetic output changes also fail loudly
// rather than silently reshaping the view.
func assertDetailLineCount(t *testing.T, text string, want int) {
	t.Helper()
	if got := len(strings.Split(strings.TrimRight(text, "\n"), "\n")); got != want {
		t.Fatalf("want exactly %d logical lines in the detail view (a surviving newline would forge more), got %d:\n%q", want, got, text)
	}
}
