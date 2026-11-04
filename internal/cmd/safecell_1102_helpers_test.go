package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// hadron-cli#1102 (slice 2 of #1093): every server-supplied value cell of the
// memory, scope and channel text tables goes through output.SafeCell. These
// helpers are shared by the per-command tests in safecell_1102_*_test.go.
//
// evilJSON1102 is a label with a newline and an ESC, spelled as the JSON
// escapes a synthetic server sends; longJSON1102 is a 1000-rune value that
// SafeCell must cut to at most 256 runes ending in "…". Splice them into a
// response with the Go raw strings below, e.g. `"name":"` + evilJSON1102 + `"`.
const (
	evilJSON1102 = `a\nb\u001b[31mc`
	evilWant1102 = "a b [31mc" // what SafeCell renders for it
)

var longJSON1102 = strings.Repeat("x", 1000)

// runBoth1102 runs one command in text mode and again with --json and returns
// both outputs. args must not carry --json or --server.
func runBoth1102(t *testing.T, responses map[string]string, args ...string) (text, jsonOut string) {
	t.Helper()
	gql, _ := captureGraphQL(t, responses)
	for _, asJSON := range []bool{false, true} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		a := append(append([]string{}, args...), "--server", gql.URL)
		if asJSON {
			a = append(a, "--json")
		}
		root.SetArgs(a)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v (json=%v): %v\n%s", args, asJSON, err, out.String())
		}
		if asJSON {
			jsonOut = out.String()
		} else {
			text = out.String()
		}
	}
	return text, jsonOut
}

// assertSafeText1102 checks a text-mode output: no control byte but the
// newline that ends a row, exactly wantLines lines (so a newline in a value did
// not forge a row), the evil label rendered in its sanitized form, and the
// long value cut. wantEvil / wantLong say which of the two the command's
// fixture put on screen.
func assertSafeText1102(t *testing.T, text string, wantLines int, wantEvil, wantLong bool) {
	t.Helper()
	for i := 0; i < len(text); i++ {
		if c := text[i]; (c < 0x20 && c != '\n') || c == 0x7f {
			t.Fatalf("control byte %#x at %d in text output:\n%q", c, i, text)
		}
	}
	if got := len(strings.Split(strings.TrimRight(text, "\n"), "\n")); got != wantLines {
		t.Fatalf("%d lines, want %d (a value forged or hid a row):\n%s", got, wantLines, text)
	}
	if wantEvil && !strings.Contains(text, evilWant1102) {
		t.Fatalf("sanitized label %q missing:\n%s", evilWant1102, text)
	}
	if wantLong {
		if strings.Contains(text, strings.Repeat("x", 257)) {
			t.Fatalf("long value not cut:\n%s", text)
		}
		if !strings.Contains(text, strings.Repeat("x", 255)+"…") {
			t.Fatalf("long value not in SafeCell's cut form (255 runes + …):\n%s", text)
		}
	}
}

// assertJSONUnchanged1102 checks --json still carries the original value, not
// the sanitized or cut one: the evil label and all 1000 runes.
func assertJSONUnchanged1102(t *testing.T, jsonOut string, wantEvil, wantLong bool) {
	t.Helper()
	if !json.Valid([]byte(jsonOut)) {
		t.Fatalf("--json output is not JSON:\n%s", jsonOut)
	}
	if wantEvil && !strings.Contains(jsonOut, `a\nb\u001b[31mc`) {
		t.Fatalf("--json lost the original label:\n%s", jsonOut)
	}
	if wantLong && !strings.Contains(jsonOut, longJSON1102) {
		t.Fatalf("--json cut the long value:\n%s", jsonOut)
	}
}
