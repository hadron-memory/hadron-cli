package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// exitCodeTableRE matches a row of the exit-code table: | 7    | text |
var exitCodeTableRE = regexp.MustCompile(`(?m)^\|\s*(\d+)\s*\|`)

// declaredExitCodes parses internal/exitcode for the constants it declares.
//
// PARSED, NOT LISTED. The first version of this guard compared the doc table
// against a hand-maintained slice in this file, which is the thing it was
// written to prevent wearing a different hat: a constant added without also
// editing that slice is never examined, so the check passes while the contract
// drifts (@codex and @copilot, PR #633, independently). A guard whose own
// inputs need hand-updating has the same failure mode as the doc it guards.
func declaredExitCodes(t *testing.T) map[int]string {
	t.Helper()
	const src = "../exitcode/exitcode.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}
	out := map[int]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.INT {
				continue
			}
			n, err := strconv.Atoi(lit.Value)
			if err != nil {
				continue
			}
			out[n] = vs.Names[0].Name
		}
	}
	return out
}

// exitCodeSection returns just the "Exit codes (stable contract)" section, so
// numeric first columns in OTHER tables cannot be mistaken for documented exit
// codes — which would let the reverse check pass on a coincidence.
func exitCodeSection(t *testing.T, doc string) string {
	t.Helper()
	const heading = "## Exit codes (stable contract)"
	i := strings.Index(doc, heading)
	if i < 0 {
		t.Fatalf("the %q heading is gone — this guard would otherwise pass vacuously", heading)
	}
	rest := doc[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// The exit-code table in agentic-usage.md is the contract AGENTS read, and
// nothing kept it honest against internal/exitcode until #619 added one.
//
// BIDIRECTIONAL, and both directions are real failures:
//   - a constant with no row is an exit code its audience cannot branch on;
//   - a row with no constant is a promise the binary does not keep.
//
// The first version checked only the first direction, against a hand-written
// list. Both bots caught that; the fix derives the constants by parsing the
// source and compares the two SETS.
func TestExitCodeTableMatchesTheConstants(t *testing.T) {
	raw, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatalf("read agentic-usage.md: %v", err)
	}
	section := exitCodeSection(t, string(raw))

	documented := map[int]bool{}
	for _, m := range exitCodeTableRE.FindAllStringSubmatch(section, -1) {
		n, err := strconv.Atoi(m[1])
		if err == nil {
			documented[n] = true
		}
	}
	declared := declaredExitCodes(t)

	// Refuse to pass vacuously: if either side comes back empty the shape
	// changed, and an empty-vs-empty comparison would agree about nothing.
	if len(documented) == 0 {
		t.Fatal("no exit-code rows parsed — the table's shape changed")
	}
	if len(declared) == 0 {
		t.Fatal("no constants parsed from internal/exitcode — the declaration's shape changed")
	}
	// Anchor on a value that must always exist, so a parser that silently
	// matched the wrong thing is caught rather than trusted.
	if declared[exitcode.Forbidden] != "Forbidden" {
		t.Fatalf("parsed constants do not contain Forbidden=%d; got %v", exitcode.Forbidden, declared)
	}

	for code, name := range declared {
		if !documented[code] {
			t.Errorf("exitcode.%s = %d is not in the documented table — "+
				"an undocumented exit code cannot be branched on by the agents it exists for", name, code)
		}
	}
	for code := range documented {
		if _, ok := declared[code]; !ok {
			t.Errorf("the table documents exit %d, which internal/exitcode does not declare — "+
				"a row with no constant is a promise the binary does not keep", code)
		}
	}
}

// #619 end-to-end: a server FORBIDDEN reaches the USER as exit 8.
//
// Asserted through a real command with exitCodeFor, not by calling the mapper:
// `renderError` post-processes what the mapper returns, so a test that reads
// exitcode.FromError measures a value that exists only inside the test
// (review:assert-the-value-the-user-gets).
func TestForbiddenReachesTheUserAsExitEight(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MergeUsers": `{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "merge", "dup", "--into", "alice", "--yes", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Forbidden {
		t.Errorf("exit = %d, want %d (Forbidden); err: %v", code, exitcode.Forbidden, err)
	}
	// And it must NOT tell an authenticated caller to sign in — the false
	// remedy exit 3 would have carried.
	if err != nil && strings.Contains(err.Error(), "auth login") {
		t.Errorf("a permission refusal must not prescribe signing in: %v", err)
	}
}
