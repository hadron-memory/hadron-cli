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
	if len(names) != wantDisabledCount {
		t.Fatalf("testdata lists %d tool names, want exactly %d (plan mcp-team-cut, ruling M5-R1)", len(names), wantDisabledCount)
	}
	return regexp.MustCompile(`\b(?:` + strings.Join(names, "|") + `)\b`)
}

// scanned reports whether a repo-relative, slash-separated path is shipped text.
func scanned(rel string) bool {
	switch {
	case strings.HasPrefix(rel, "docs/plans/"):
		return false // design-as-built records, not instructions (plan D5)
	case strings.HasPrefix(rel, "internal/api/gen/"):
		return false // copies of the server SDL; follow S1 via S2's snapshot refresh
	case strings.Contains(rel, "/testdata/"):
		return false
	case rel == "internal/cmd/agentic/agentic-usage.md", rel == "README.md":
		return true
	case strings.HasPrefix(rel, "plugins/"), strings.HasPrefix(rel, "docs/"):
		return true
	case strings.HasPrefix(rel, "internal/") && strings.HasSuffix(rel, ".txt"):
		return true // embedded text (e.g. the tools manifest)
	}
	return false
}

func TestNoDisabledMCPToolNamedInShippedText(t *testing.T) {
	root := repoRoot(t)
	disabled := loadDisabled(t)
	matched := map[string]bool{}
	var hits []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "node_modules" || name == "bin" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		isGo := strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") && !strings.HasPrefix(rel, "internal/api/gen/")
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
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, uerr := strconv.Unquote(lit.Value); uerr == nil {
						texts = append(texts, s)
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
				if _, ok := exemptions[rel]; ok {
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
	for _, h := range hits {
		t.Errorf("shipped text names a disabled MCP team tool (use the CLI command): %s", h)
	}
	for rel, why := range exemptions {
		if !matched[rel] {
			t.Errorf("stale exemption %s (%s): it names no disabled tool any more; remove it", rel, why)
		}
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
