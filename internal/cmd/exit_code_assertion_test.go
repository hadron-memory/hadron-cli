package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
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
const exitcodePkgPath = "github.com/hadron-memory/hadron-cli/internal/exitcode"

func rawExitCodeAssertionLines(filename string, src []byte) ([]int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	// Resolve the LOCAL name the exitcode package is bound to in THIS file,
	// rather than assuming "exitcode": a test could import it aliased
	// (`ec "…/internal/exitcode"`) and `ec.FromError(...)` would slip past a
	// name-literal match — a fail-open in the one guard whose job is not to
	// fail open (#564 review, @copilot). A blank (`_`) import can't be called,
	// so it needs no match; a dot import is out of scope (the call has no
	// selector) and would need a different shape entirely.
	local := ""
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) != exitcodePkgPath {
			continue
		}
		switch {
		case imp.Name == nil:
			local = "exitcode" // the package's own name
		case imp.Name.Name == "_" || imp.Name.Name == ".":
			local = "" // unreferenceable by selector; nothing to match
		default:
			local = imp.Name.Name
		}
		break
	}
	if local == "" {
		return nil, nil
	}
	var lines []int
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == local && sel.Sel.Name == "FromError" {
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
// string (the #564 fix), and it follows the exitcode import's LOCAL name —
// including an alias — rather than assuming "exitcode" (#575, @copilot).
func TestRawExitCodeAssertionDetectorIgnoresCommentsAndStrings(t *testing.T) {
	// Default import name: the call in code is flagged; the same text in a
	// comment and a string is not.
	def := []byte(`package x

import "` + exitcodePkgPath + `"

// This comment mentions exitcode.FromError( and must NOT be flagged.
func f() {
	// nor this: exitcode.FromError(err)
	s := "exitcode.FromError( in a string is not a call"
	_ = s
	_ = exitcode.FromError(nil) // line 10: the ONLY real call
}
`)
	if lines, err := rawExitCodeAssertionLines("d_test.go", def); err != nil || len(lines) != 1 || lines[0] != 10 {
		t.Fatalf("default import: want the code call at line 10, got %v (err %v)", lines, err)
	}

	// Aliased import: `ec.FromError(...)` must still be caught — the fail-open
	// the name-literal match had.
	aliased := []byte(`package x

import ec "` + exitcodePkgPath + `"

func g() { _ = ec.FromError(nil) } // line 5
`)
	if lines, err := rawExitCodeAssertionLines("a_test.go", aliased); err != nil || len(lines) != 1 || lines[0] != 5 {
		t.Fatalf("aliased import: want the aliased call at line 5, got %v (err %v)", lines, err)
	}

	// A file that does not import exitcode at all: a bare `exitcode.FromError`
	// (some OTHER local `exitcode`) is not this package's call — no match.
	unrelated := []byte(`package x

func h() { var exitcode struct{ FromError func(error) int }; _ = exitcode.FromError(nil) }
`)
	if lines, err := rawExitCodeAssertionLines("u_test.go", unrelated); err != nil || len(lines) != 0 {
		t.Fatalf("unrelated local: want no match, got %v (err %v)", lines, err)
	}
}
