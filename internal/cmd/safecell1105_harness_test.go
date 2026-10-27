package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"
)

// cli#1105 (slice 5 of #1093): the integration and ops commands print
// server-supplied strings in text mode, so a newline, ESC or bidi override in
// one could forge a row or recolor the terminal. Each converted command gets one
// synthetic-server case run three ways: a hostile value (rows and hidden
// runes), a 1000-rune value (the SafeCell cut) and --json (originals kept).

// hostile1105 carries a newline, an ESC colour sequence, a bidi override and a
// zero-width space.
const hostile1105 = "a\nb\x1b[31mc\u202ed\u200be"

type safeCell1105Case struct {
	name string
	// args is the command line without --server and --json.
	args []string
	// resp builds the synthetic server's answers (operation name to response
	// JSON), putting cell in every free-text field the command prints. Enum,
	// boolean and numeric fields keep a valid value so the response decodes.
	resp func(cell string) map[string]string
	// lines is the text output's line count: a table is its header plus one
	// logical row per record; a detail view is its fixed line count.
	lines int
}

// q1105 is the JSON string literal for s, for building synthetic responses.
func q1105(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func hasHiddenRune1105(text string) (rune, bool) {
	for _, r := range text {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)) {
			return r, true
		}
	}
	return 0, false
}

func runSafeCell1105(t *testing.T, c safeCell1105Case, cell string, asJSON bool) string {
	t.Helper()
	gql := fakeGraphQL(t, c.resp(cell))
	f, out := testFactory(t)
	args := append(append([]string{}, c.args...), "--server", gql.URL)
	if asJSON {
		args = append(args, "--json")
	}
	root := NewRootCmd(f)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	return out.String()
}

func jsonCarries1105(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, want)
	case []any:
		for _, e := range x {
			if jsonCarries1105(e, want) {
				return true
			}
		}
	case map[string]any:
		for _, e := range x {
			if jsonCarries1105(e, want) {
				return true
			}
		}
	}
	return false
}

func assertSafeCell1105(t *testing.T, c safeCell1105Case) {
	t.Helper()
	t.Run(c.name+"/hostile", func(t *testing.T) {
		text := runSafeCell1105(t, c, hostile1105, false)
		if r, bad := hasHiddenRune1105(text); bad {
			t.Fatalf("hidden rune %U survived in text output %q", r, text)
		}
		if got := len(strings.Split(strings.TrimRight(text, "\n"), "\n")); got != c.lines {
			t.Fatalf("want %d lines, got %d:\n%q", c.lines, got, text)
		}
	})
	t.Run(c.name+"/long", func(t *testing.T) {
		long := strings.Repeat("日", 1000)
		text := runSafeCell1105(t, c, long, false)
		if strings.Contains(text, strings.Repeat("日", 256)) || !strings.Contains(text, strings.Repeat("日", 255)+"…") {
			t.Fatalf("a 1000-rune value must render in SafeCell's cut form (255 runes then …):\n%s", text)
		}
		if got := len(strings.Split(strings.TrimRight(text, "\n"), "\n")); got != c.lines {
			t.Fatalf("want %d lines, got %d:\n%q", c.lines, got, text)
		}
	})
	t.Run(c.name+"/json", func(t *testing.T) {
		for _, cell := range []string{hostile1105, strings.Repeat("日", 1000)} {
			var v any
			raw := runSafeCell1105(t, c, cell, true)
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				t.Fatalf("--json is not JSON: %v\n%s", err, raw)
			}
			if !jsonCarries1105(v, cell) {
				t.Fatalf("--json must keep the original value, got %s", raw)
			}
		}
	})
}
