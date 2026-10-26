package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// cli#1065: `app key list` text cells carry server-supplied strings, so a
// control or invisible character in one could forge a row or recolor the
// terminal. Text replaces them; --json keeps the originals.

const hostileKeyField = "k\nrow\x1b[31mred\u202etxt\u200b\u2028\t\x7f\u0085z"

func hostileKeyRow(t *testing.T, id string, cell string) string {
	t.Helper()
	m := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	return `{"id":` + m(id+cell) + `,"keyPreview":` + m(cell) + `,"label":` + m(cell) +
		`,"createdAt":` + m(cell) + `,"lastUsedAt":` + m(cell) + `,"revokedAt":` + m(cell) + `}`
}

func TestAppKeyListSanitizesEverySixCellInText(t *testing.T) {
	long := strings.Repeat("日", 1000)
	for name, cell := range map[string]string{"hostile": hostileKeyField, "long multibyte": long} {
		t.Run(name, func(t *testing.T) {
			gql := fakeGraphQL(t, map[string]string{"AppKeysMetadata": `{"data":{"appKeys":[` +
				hostileKeyRow(t, "a", cell) + `,` + hostileKeyRow(t, "b", cell) + `]}}`})
			defer gql.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"app", "key", "list", appRead1057, "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
			if len(lines) != 3 {
				t.Fatalf("want a header and one logical row per key (3 lines), got %d:\n%q", len(lines), text)
			}
			for _, r := range text {
				if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)) {
					t.Fatalf("hidden rune %U survived in text output %q", r, text)
				}
			}
			if name == "long multibyte" {
				if strings.Contains(text, strings.Repeat("日", 256)) || strings.Count(text, "…") != 12 {
					t.Errorf("each of the six cells of both rows must be cut with …, got %d:\n%s", strings.Count(text, "…"), text)
				}
				if utf8.RuneCountInString(strings.Repeat("日", 255)+"…") != 256 {
					t.Fatal("bound arithmetic")
				}
			}
		})
	}
}

func TestAppKeyListJSONKeepsTheOriginals(t *testing.T) {
	long := strings.Repeat("日", 1000)
	for _, cell := range []string{hostileKeyField, long} {
		gql := fakeGraphQL(t, map[string]string{"AppKeysMetadata": `{"data":{"appKeys":[` + hostileKeyRow(t, "a", cell) + `]}}`})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "key", "list", appRead1057, "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		gql.Close()
		var rows []struct {
			ID, KeyPreview                          string
			Label, CreatedAt, LastUsedAt, RevokedAt *string
		}
		if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Label == nil || *rows[0].Label != cell || rows[0].KeyPreview != cell || rows[0].ID != "a"+cell {
			t.Errorf("--json must round-trip the original values, got %+v", rows)
		}
	}
}

func TestAppKeyListTextKeepsNullAndOrdinaryValues(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"AppKeysMetadata": `{"data":{"appKeys":[{"id":"key-id","keyPreview":"hdr_...abcd","label":"café 😀","createdAt":"2026-01-01T00:00:00Z","lastUsedAt":null,"revokedAt":null}]}}`})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "key", "list", appRead1057, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"key-id", "hdr_...abcd", "café 😀", "2026-01-01T00:00:00Z"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("ordinary value %q must pass unchanged:\n%s", want, out)
		}
	}
	if !strings.Contains(out.String(), "-") {
		t.Errorf("a null cell must still render -")
	}
}
