// Package shippedtext guards the text the CLI ships to its users and to the
// AI agents that shell out to it.
//
// hadron-server slice S1 of the MCP team cut (hadron-server#2937) disables 71
// agent-team MCP tools. The CLI's own text must name only CLI commands, so a
// worker is never told to call a tool that no longer exists (hadron-cli#1384,
// slice M5). This test is the guard: it fails when a disabled tool name
// reaches any shipped text.
package shippedtext

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// wantDisabledCount pins the list size (ruling M5-R1) so that a hand edit of
// the testdata file cannot shrink the guard silently.
const wantDisabledCount = 71

// exemptions are files allowed to name a disabled tool, each with the reason.
// An exemption that no longer matches anything fails the test, so the entry is
// removed in the same change that cleans the file.
var exemptions = map[string]string{
	"internal/cmd/spec/mcp-tools.txt": "the tools manifest embedded by checktools.go; slice S2 regenerates it at S1's deployed SHA (plan R5)",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func loadDisabled(t *testing.T) *regexp.Regexp {
	t.Helper()
	f, err := os.Open("testdata/disabled_mcp_tools.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var names []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if seen[line] {
			t.Fatalf("duplicate name %q in testdata", line)
		}
		seen[line] = true
		names = append(names, regexp.QuoteMeta(line))
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	if len(names) != wantDisabledCount {
		t.Fatalf("testdata lists %d tool names, want exactly %d (plan mcp-team-cut, ruling M5-R1)", len(names), wantDisabledCount)
	}
	return regexp.MustCompile(`\b(?:` + strings.Join(names, "|") + `)\b`)
}

// unscannedPaths are the only paths the walk skips outright, each exact and
// with its reason. Nothing is skipped by directory NAME: a `testdata`, `bin` or
// `dist` directory under plugins/ or docs/ is shipped text like any other.
var unscannedPaths = map[string]string{
	"internal/shippedtext/testdata": "this guard's own list of the disabled names",
	"internal/api/queries":          "GraphQL build inputs, not embedded text; generated Go literals are checked separately",
	"internal/api/gen":              "copies of the server SDL; follow S1 via S2's snapshot refresh",
	"docs/plans":                    "design-as-built records, not instructions (plan D5)",
	".git":                          "VCS metadata",
	"bin":                           "build output at the repo root",
	"dist":                          "release output at the repo root",
}

func isUnscannedPath(rel string) bool {
	for p := range unscannedPaths {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// scanned reports whether a repo-relative, slash-separated path is shipped text.
func scanned(rel string) bool {
	switch {
	case isUnscannedPath(rel):
		return false
	case rel == "internal/cmd/agentic/agentic-usage.md", rel == "README.md":
		return true
	case strings.HasPrefix(rel, "plugins/"), strings.HasPrefix(rel, "docs/"):
		return true
	case strings.HasPrefix(rel, "internal/") && !strings.HasSuffix(rel, ".go"):
		return true // conservative superset of embedded assets, regardless of extension
	}
	return false
}

// foldStringLiterals evaluates an expression built only from string literals,
// parentheses and `+`, so `"hadron_team_" + "chat_read"` is checked as the one
// string it is.
//
// LIMIT, pinned by TestScanRootDetection: an operand that is not a literal is
// not evaluated, so a name assembled from a named constant or variable, or by
// fmt, strings.Join and the like, is NOT detected (only the literal parts are
// scanned on their own). The guard targets accidental reuse of a disabled
// name in shipped text, not a deliberately obfuscated one; full constant
// folding would need go/types and every package loaded.
func foldStringLiterals(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return foldStringLiterals(e.X)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		l, ok := foldStringLiterals(e.X)
		if !ok {
			return "", false
		}
		r, ok := foldStringLiterals(e.Y)
		return l + r, ok
	}
	return "", false
}

// scanRoot walks root and returns every "path: name" hit plus the exempt paths
// that actually matched.
func scanRoot(t *testing.T, root string, disabled *regexp.Regexp, exempt map[string]string) (hits []string, matched map[string]bool) {
	t.Helper()
	matched = map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if isUnscannedPath(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		isGo := strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go")
		if !isGo && !scanned(rel) {
			return nil
		}
		var texts []string
		if isGo {
			// String literals only: comments are history (ruling M5-R2).
			fset := token.NewFileSet()
			file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if perr != nil {
				t.Errorf("parse %s: %v", rel, perr)
				return nil
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BinaryExpr:
					// A name split across a concatenation of literals is one
					// string at run time (cli#1422).
					if s, ok := foldStringLiterals(n); ok {
						texts = append(texts, s)
						return false
					}
				case *ast.BasicLit:
					if n.Kind == token.STRING {
						if s, uerr := strconv.Unquote(n.Value); uerr == nil {
							texts = append(texts, s)
						}
					}
				}
				return true
			})
		} else {
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			texts = []string{string(b)}
		}
		for _, text := range texts {
			for _, name := range disabled.FindAllString(text, -1) {
				if _, ok := exempt[rel]; ok {
					matched[rel] = true
					continue
				}
				hits = append(hits, rel+": "+name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hits, matched
}

func TestNoDisabledMCPToolNamedInShippedText(t *testing.T) {
	hits, matched := scanRoot(t, repoRoot(t), loadDisabled(t), exemptions)
	for _, h := range hits {
		t.Errorf("shipped text names a disabled MCP team tool (use the CLI command): %s", h)
	}
	for rel, why := range exemptions {
		if !matched[rel] {
			t.Errorf("stale exemption %s (%s): it names no disabled tool any more; remove it", rel, why)
		}
	}
}

// TestScanRootDetection pins what the walk covers, on a throwaway tree: a name
// planted at each shipped surface is found, including under directories named
// testdata, bin and dist inside plugins/ and docs/; and the exact skips are
// honoured.
func TestScanRootDetection(t *testing.T) {
	const bad = "call hadron_team_chat_read now\n"
	write := func(root, rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	re := loadDisabled(t)
	for _, rel := range []string{
		"plugins/x/SKILL.md",
		"plugins/x/testdata/SKILL.md",
		"plugins/x/dist/SKILL.md",
		"plugins/x/bin/SKILL.md",
		"plugins/x/node_modules/SKILL.md",
		"docs/how-to/a.md",
		"docs/how-to/testdata/a.md",
		"README.md",
		"internal/cmd/agentic/agentic-usage.md",
		"internal/cmd/other/embedded.txt",
		"internal/cmd/other/embedded.md",
		"internal/cmd/other/embedded.tmpl",
		"internal/cmd/other/embedded.json",
		"internal/cmd/other/embedded.custom",
	} {
		root := t.TempDir()
		write(root, rel, bad)
		if hits, _ := scanRoot(t, root, re, nil); len(hits) != 1 {
			t.Errorf("%s: want 1 hit, got %v", rel, hits)
		}
	}
	root := t.TempDir()
	write(root, "internal/lib/lib.go", "package lib\nvar S = \"hadron_team_chat_read\"\n")
	if hits, _ := scanRoot(t, root, re, nil); len(hits) != 1 {
		t.Errorf("a Go string literal must be found, got %v", hits)
	}
	// Concatenated literals are one string (cli#1422): detected, however split.
	for name, src := range map[string]string{
		"two parts":     `"hadron_team_" + "chat_read"`,
		"three parts":   `"hadron_" + "team_" + "chat_read"`,
		"parenthesised": `("hadron_team_" + ("chat_" + "read"))`,
		"inside a call": `fmt.Sprint("x", "hadron_team_" + "chat_read")`,
	} {
		root := t.TempDir()
		write(root, "internal/lib/lib.go", "package lib\nvar S = "+src+"\n")
		if hits, _ := scanRoot(t, root, re, nil); len(hits) != 1 {
			t.Errorf("concatenation (%s) must be detected once, got %v", name, hits)
		}
	}
	// The documented limit, pinned: an operand that is not a literal is not
	// evaluated. If this starts to fail the guard got stronger: update the
	// foldStringLiterals comment and move the case above.
	root = t.TempDir()
	write(root, "internal/lib/lib.go", "package lib\nconst a = \"hadron_team_\"\nvar S = a + \"chat_read\"\nvar T = Prefix() + \"chat_read\"\n")
	if hits, _ := scanRoot(t, root, re, nil); len(hits) != 0 {
		t.Errorf("a non-literal operand is the documented limit, got %v", hits)
	}
	// Skipped on purpose: comments, history, and the exact skip paths.
	root = t.TempDir()
	write(root, "internal/lib/lib.go", "package lib\n// hadron_team_chat_read\n")
	write(root, "docs/plans/p.md", bad)
	write(root, "internal/api/queries/query.graphql", bad)
	write(root, "internal/api/gen/generated.go", "package gen\nvar S = \"hadron_team_chat_read\"\n")
	write(root, "internal/shippedtext/testdata/list.txt", bad)
	write(root, "bin/x.md", bad)
	if hits, _ := scanRoot(t, root, re, nil); len(hits) != 0 {
		t.Errorf("exact skips and comments must pass, got %v", hits)
	}
	// An exemption is honoured and reported as matched; one that matches nothing is not.
	root = t.TempDir()
	write(root, "internal/cmd/spec/mcp-tools.txt", bad)
	hits, matched := scanRoot(t, root, re, map[string]string{"internal/cmd/spec/mcp-tools.txt": "r", "gone.txt": "r"})
	if len(hits) != 0 || !matched["internal/cmd/spec/mcp-tools.txt"] || matched["gone.txt"] {
		t.Errorf("exemption handling wrong: hits=%v matched=%v", hits, matched)
	}
}

func TestGuardCatchesADisabledName(t *testing.T) {
	re := loadDisabled(t)
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"call hadron_team_chat_read now", true},
		{"`hadron_task_create`", true},
		{"hadron_get_node", false},
		{"hadron_create_task", false},
		{"hadron_team_chat_read_extra", false},
	} {
		if got := re.MatchString(tc.in); got != tc.want {
			t.Errorf("MatchString(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
