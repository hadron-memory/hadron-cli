package output

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafeCellReplacesEveryHiddenClassWithOneSpace(t *testing.T) {
	for name, r := range map[string]rune{
		"C0 NUL": 0x00, "C0 BEL": 0x07, "TAB": '\t', "LF": '\n', "CR": '\r', "ESC": 0x1b,
		"DEL": 0x7f, "C1 NEL": 0x85, "C1 CSI": 0x9b,
		"Cf bidi override U+202E": 0x202e, "Cf bidi isolate U+2066": 0x2066,
		"Cf zero-width U+200B": 0x200b, "Cf ZWJ U+200D": 0x200d, "Cf BOM U+FEFF": 0xfeff,
		"Zl U+2028": 0x2028, "Zp U+2029": 0x2029,
	} {
		if got := SafeCell("a" + string(r) + "b"); got != "a b" {
			t.Errorf("%s: got %q, want %q", name, got, "a b")
		}
	}
}

func TestSafeCellKeepsOrdinaryUnicode(t *testing.T) {
	for _, s := range []string{"", "plain", "café", "日本語のラベル", "emoji 😀", "e\u0301 combining", "-"} {
		if got := SafeCell(s); got != s {
			t.Errorf("SafeCell(%q) = %q, want it unchanged", s, got)
		}
	}
}

// 256 is the RENDERED limit including the ellipsis: a cut cell is 255 kept
// runes plus "…". The cut is by rune, so it may split a combining sequence.
func TestSafeCellBoundIncludesTheEllipsis(t *testing.T) {
	exact := strings.Repeat("é", cellMaxRunes)
	if got := SafeCell(exact); got != exact {
		t.Errorf("a %d-rune cell must pass unchanged", cellMaxRunes)
	}
	got := SafeCell(strings.Repeat("日", 1000))
	if n := utf8.RuneCountInString(got); n != cellMaxRunes {
		t.Errorf("cut cell is %d runes, want %d", n, cellMaxRunes)
	}
	if !strings.HasSuffix(got, "…") || strings.TrimSuffix(got, "…") != strings.Repeat("日", cellMaxRunes-1) {
		t.Errorf("cut cell must be %d kept runes then …", cellMaxRunes-1)
	}
	if got := SafeCell(strings.Repeat("x", cellMaxRunes+1)); !strings.HasSuffix(got, "…") {
		t.Errorf("one rune over the bound must be cut")
	}
}

func TestSafeCellOutputIsValidUTF8(t *testing.T) {
	if got := SafeCell("a\xffb"); !utf8.ValidString(got) {
		t.Errorf("invalid input must render as valid UTF-8, got %q", got)
	}
}

// hiddenRunes is the rune set a sanitized value must never carry into a
// terminal: C0/C1 controls (incl. TAB, LF, CR, ESC), DEL, format characters
// (Cf: bidi overrides, zero-widths, BOM) and separator lines/paragraphs.
var hiddenRunes = map[string]rune{
	"C0 NUL": 0x00, "C0 BEL": 0x07, "TAB": '\t', "LF": '\n', "CR": '\r', "ESC": 0x1b,
	"DEL": 0x7f, "C1 NEL": 0x85, "C1 CSI": 0x9b,
	"Cf bidi override U+202E": 0x202e, "Cf bidi isolate U+2066": 0x2066,
	"Cf zero-width U+200B": 0x200b, "Cf ZWJ U+200D": 0x200d, "Cf BOM U+FEFF": 0xfeff,
	"Zl U+2028": 0x2028, "Zp U+2029": 0x2029,
}

func TestSafeTextNeutralizesEveryHiddenClassLikeSafeCell(t *testing.T) {
	for name, r := range hiddenRunes {
		if got := SafeText("a" + string(r) + "b"); got != "a b" {
			t.Errorf("%s: got %q, want %q", name, got, "a b")
		}
	}
}

func TestSafeTextKeepsOrdinaryUnicode(t *testing.T) {
	for _, s := range []string{"", "plain", "café", "日本語のラベル", "emoji 😀", "e\u0301 combining", "-"} {
		if got := SafeText(s); got != s {
			t.Errorf("SafeText(%q) = %q, want it unchanged", s, got)
		}
	}
}

// SafeText exists (cli#1110, Joshua P2-1) because detail views are the human
// way to read long server text — a system prompt or an error message — so
// unlike SafeCell it must not cut.
func TestSafeTextDoesNotCutLongDetailText(t *testing.T) {
	long := strings.Repeat("日", 5000)
	if got := SafeText(long); got != long {
		t.Errorf("SafeText must keep detail text whole, got %d runes, want %d", utf8.RuneCountInString(got), len([]rune(long)))
	}
	if strings.ContainsRune(SafeText(long), '…') {
		t.Errorf("SafeText must never append an ellipsis")
	}
}
