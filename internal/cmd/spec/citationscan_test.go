package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree materializes files under a temp dir; keys are slash-separated
// relative paths.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

func citationsOf(refs []citationRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Citation)
	}
	return out
}

// The comment marker varies across the live pointers — `//`, ` * ` inside a
// block comment, a Go raw string — so the matcher anchors on the convention
// (`Spec:`), not on any comment syntax.
func TestScanLineAnchoredForms(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"line comment", `// Spec: cor:api:130 (surface contract).`, []string{"cor:api:130"}},
		{"block comment", ` * Spec: cor:int:030:02`, []string{"cor:int:030:02"}},
		{"em-dash prose", `    // Spec: cor:api:180 — cross-entity URN resolution`, []string{"cor:api:180"}},
		{"flat citation", `// Spec: msg:010:02:03`, []string{"msg:010:02:03"}},
		// A feature segment is required even after the anchor: ParseCitation
		// accepts a lone 3-letter code as a flat module citation, so allowing
		// it here made every three-letter word on the line a "citation".
		{"module level not matched", `// Spec: cor:api`, nil},
		{"prose is not a module citation", `// Spec: cor:api:130 (surface contract).`, []string{"cor:api:130"}},
		{"no anchor", `// cor:api:130 is the contract`, nil},
		{"anchor without citation", `spec:`, nil},
		{"k8s manifest key", `spec:`, nil},
		{"prose after anchor", `// Spec: see the design doc`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanLine(tc.line, false)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("scanLine(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

// The case a single-capture matcher silently fails: a real pointer often lists
// several citations, and checking only the first leaves the rest unverified —
// the under-coverage this command exists to remove. Both lines are copied from
// hadron-server.
func TestScanLineTakesEveryCitationOnTheLine(t *testing.T) {
	got := scanLine(`      // Spec: cor:api:080:01 (collide vs relocate), cor:api:080:02 (source`, false)
	want := []string{"cor:api:080:01", "cor:api:080:02"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}

	got = scanLine(` * Spec: cor:api:050 (feature) · cor:api:050:01 (field-sniff) · cor:api:050:02 (scope/access)`, false)
	want = []string{"cor:api:050", "cor:api:050:01", "cor:api:050:02"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}

	// The same citation twice on one line is one occurrence.
	if got := scanLine(`// Spec: cor:api:050 and again cor:api:050`, false); len(got) != 1 {
		t.Errorf("a repeated citation should collapse, got %v", got)
	}
}

// A matched citation must be the WHOLE token. The regex enforces only a
// leading boundary, so a typo used to truncate to a valid prefix — which then
// resolved, and the malformed pointer passed as healthy.
func TestScanLineRejectsPartialTokens(t *testing.T) {
	for _, line := range []string{
		`// Spec: msg:0102`,             // a digit too many
		`// Spec: cor:api:1300`,         // ditto, product-rooted
		`// Spec: cor:api:130:02:031`,   // trailing digit on the flow
		`// Spec: cor:api:130:02extra`,  // a longer identifier
		`// Spec: cor:api:130:02:03:04`, // one segment too deep
		`// Spec: cor:api:130.02`,       // dot-delimited: the typo the guide warns about
	} {
		if got := scanLine(line, false); got != nil {
			t.Errorf("%q must not match a valid PREFIX of a malformed token, got %v", line, got)
		}
	}

	// The terminators real pointers actually use still match.
	for _, tc := range []struct{ line, want string }{
		{`// Spec: cor:api:130`, "cor:api:130"},
		{`// Spec: cor:api:130.`, "cor:api:130"},
		{`// Spec: cor:api:130, and more`, "cor:api:130"},
		{`// Spec: cor:api:130 (surface contract)`, "cor:api:130"},
		{` * Spec: cor:api:130:02 — egress`, "cor:api:130:02"},
		{"// Spec: cor:api:130\t(tab)", "cor:api:130"},
	} {
		got := scanLine(tc.line, false)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("scanLine(%q) = %v, want [%s]", tc.line, got, tc.want)
		}
	}
}

func TestScanLineLoose(t *testing.T) {
	line := `see cor:api:130:02 for the egress policy`
	if got := scanLine(line, false); got != nil {
		t.Errorf("without --loose an unanchored token must be ignored, got %v", got)
	}
	if got := scanLine(line, true); len(got) != 1 || got[0] != "cor:api:130:02" {
		t.Errorf("--loose should find the bare token, got %v", got)
	}

	// Loose mode requires a feature segment: a bare three-letter token would
	// otherwise match most English prose.
	for _, noise := range []string{
		"the cli was fine",
		"see https://example.com/api:050:01/docs",
		"path/to/cor:api:050",
		"prefixcor:api:050",
	} {
		if got := scanLine(noise, true); got != nil {
			t.Errorf("loose match on %q should find nothing, got %v", noise, got)
		}
	}
}

func TestScanCitationsWalksAndSkips(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/a.ts":              "// Spec: cor:api:130\nconst x = 1;\n",
		"src/nested/b.go":       "// Spec: cor:dmo:020:01 (Node ordering).\n",
		"node_modules/dep/c.js": "// Spec: cor:api:999\n",
		"dist/bundle.js":        "// Spec: cor:api:998\n",
		".git/COMMIT_EDITMSG":   "// Spec: cor:api:997\n",
		"docs/notes.md":         "See `// Spec: cor:api:140` for the rule.\n",
		// Genuinely over maxFileBytes, so the oversize skip is actually
		// exercised: a 10-byte "big" file tested nothing (Copilot review on
		// #351). Its citation must NOT appear below.
		"src/big.txt":    "// Spec: cor:api:995\n" + strings.Repeat("x", maxFileBytes),
		"src/binary.bin": "abc\x00def // Spec: cor:api:996\n",
		// A generated single-line blob (a MkDocs search index in the live
		// case): the citation is prose swept into a bundle, not a pointer.
		"site/search_index.json": `{"text":"see Spec: cor:api:994 for details ` +
			strings.Repeat("padding ", maxLineBytes/8) + `"}`,
	})

	res, err := scanCitations(scanOptions{Roots: []string{root}})
	if err != nil {
		t.Fatalf("scanCitations: %v", err)
	}
	got := strings.Join(citationsOf(res.Refs), ",")
	want := "cor:api:140,cor:api:130,cor:dmo:020:01"
	if got != want {
		t.Errorf("got %q, want %q (skip-listed dirs and binaries excluded, sorted by file)", got, want)
	}
	if res.Files == 0 {
		t.Error("Files should count what was actually read")
	}
	for _, r := range res.Refs {
		if r.Line == 0 || r.File == "" || r.Text == "" {
			t.Errorf("every ref needs file/line/text for a CI annotation: %+v", r)
		}
	}
}

func TestScanCitationsExclude(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/a.ts":      "// Spec: cor:api:130\n",
		"src/a_test.ts": "// Spec: cor:api:131\n",
		"fixtures/x.ts": "// Spec: cor:api:132\n",
	})
	res, err := scanCitations(scanOptions{Roots: []string{root}, Exclude: []string{"*_test.ts", "fixtures"}})
	if err != nil {
		t.Fatalf("scanCitations: %v", err)
	}
	if got := strings.Join(citationsOf(res.Refs), ","); got != "cor:api:130" {
		t.Errorf("--exclude should prune the test file and the fixtures dir, got %q", got)
	}
}

// A root may be a single file, and an explicitly named one is scanned even
// where the walk would have skipped its directory.
func TestScanCitationsSingleFileRoot(t *testing.T) {
	root := writeTree(t, map[string]string{"vendor/dep.go": "// Spec: cor:api:130\n"})
	file := filepath.Join(root, "vendor", "dep.go")
	res, err := scanCitations(scanOptions{Roots: []string{file}})
	if err != nil {
		t.Fatalf("scanCitations: %v", err)
	}
	if len(res.Refs) != 1 || res.Refs[0].Citation != "cor:api:130" {
		t.Fatalf("an explicitly named file should be scanned, got %+v", res.Refs)
	}

	// Overlapping roots must not double-count the same file.
	res, err = scanCitations(scanOptions{Roots: []string{file, file}})
	if err != nil {
		t.Fatalf("scanCitations: %v", err)
	}
	if len(res.Refs) != 1 {
		t.Errorf("a file named twice should be scanned once, got %d refs", len(res.Refs))
	}
}

func TestScanCitationsMissingRoot(t *testing.T) {
	if _, err := scanCitations(scanOptions{Roots: []string{filepath.Join(t.TempDir(), "nope")}}); err == nil {
		t.Error("a nonexistent --src must be an error, not an empty clean scan")
	}
}
