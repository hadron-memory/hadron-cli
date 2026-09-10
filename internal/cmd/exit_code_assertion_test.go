package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// #537: a command-level test must assert the exit code a USER gets.
//
// `renderError` maps an error in two steps — `exitcode.FromError`, then
// `isUsageError` to upgrade cobra's OWN refusals from the generic 1 to Usage 2.
// Cobra's refusals (`required flag(s) … not set`, `unknown flag`, `unknown
// command`, `accepts N args`, the flag-group failures) are plain errors carrying
// no exit code, so step one reads every one of them as 1.
//
// A test calling `exitcode.FromError` on a command result therefore measures a
// value that exists only inside the test. Both functions return `int`, both are
// in scope, and neither NAME says which one the process exits with — so the
// wrong one is exactly as easy to reach for as the right one. That is why this
// is a check rather than a convention. The correct call is `exitCodeFor`, which
// is what `renderError` runs — and now that this guard parses the AST rather
// than scanning raw lines (#564), a test may name `exitcode.FromError(` in a
// comment to explain why it avoids it, without tripping the check.
//
// The failure directions are asymmetric, which is why the whole 177-site sweep
// was worth doing even though the audit found no live defect: a test expecting
// Usage on a cobra refusal FAILS loudly (that is how #533 surfaced), while one
// expecting the generic 1 PASSES while pinning the opposite of a published
// contract.

// rawExitCodeAssertionLines returns the 1-based line numbers where the source
// CALLS or references the selector `exitcode.FromError`. It parses the file, so
// the identifier is matched only in code — never inside a comment or a string
// literal, which is the #564 fix: the old line-based regex could not tell a
// real call from a test explaining in prose why it uses `exitCodeFor` instead.
func rawExitCodeAssertionLines(filename string, src []byte) ([]int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var lines []int
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == "exitcode" && sel.Sel.Name == "FromError" {
			lines = append(lines, fset.Position(sel.Pos()).Line)
		}
		return true
	})
	return lines, nil
}

func TestTestsAssertTheUserVisibleExitCode(t *testing.T) {
	entries, err := filepath.Glob(filepath.Join("*_test.go"))
	if err != nil {
		t.Fatalf("globbing test files: %v", err)
	}
	checked := 0
	for _, path := range entries {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		// This file names the banned call in its own documentation and in its
		// own detector — both in code (the detector) and prose.
		if filepath.Base(path) == "exit_code_assertion_test.go" {
			continue
		}
		checked++
		lines, err := rawExitCodeAssertionLines(path, src)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, ln := range lines {
			t.Errorf("%s:%d asserts the PRE-classification exit code; use exitCodeFor, which is what renderError runs", path, ln)
		}
	}
	// FLOOR the count: a glob that matches nothing passes vacuously, which is
	// the green-and-empty failure this repo keeps finding in its own guards.
	if checked < 30 {
		t.Errorf("only %d test files scanned; the glob is not covering the package", checked)
	}
}

// The detector matches a real call but NOT the same text in a comment or a
// string — the #564 fix, verified directly rather than through the package glob.
func TestRawExitCodeAssertionDetectorIgnoresCommentsAndStrings(t *testing.T) {
	src := []byte(`package x

import "fmt"

func code() int { return 0 }

// This comment mentions exitcode.FromError( and must NOT be flagged.
func f() {
	// nor this one: exitcode.FromError(err)
	s := "exitcode.FromError( in a string is not a call"
	_ = s
	_ = code()
	_ = exitcode.FromError(nil) // line 13: the ONLY real call
	fmt.Println("done")
}
`)
	lines, err := rawExitCodeAssertionLines("fixture_test.go", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(lines) != 1 || lines[0] != 13 {
		t.Fatalf("want exactly the code call at line 13, got %v", lines)
	}
}
