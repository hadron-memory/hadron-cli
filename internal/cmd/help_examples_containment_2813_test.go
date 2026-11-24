package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#2813: does the example runners' isolation already stop a symlink under the
// fixture from carrying an example outside it? DECISION: yes, by construction, so the
// lexical guard unsafeExamplePath1053 needs no resolver.
//
// What the runners execute: each example gets its own EMPTY t.TempDir(), the harness
// seeds only regular files into it (os.WriteFile of the @file and --*-file names, which
// the lexical guard has already confined), and the example runs in-process with
// t.Chdir into it. A path under that directory can only leave it through a symlink, and
// nothing puts one there: the harness never creates one, a fresh directory holds none,
// and no example runs against a directory another example touched. So the only way an
// example could plant a link is a hadron command that creates symlinks.
//
// This test pins that last premise, which is the one a future change could break:
// no non-test code in the module calls Symlink. If a command ever creates symlinks,
// this fails and the runners need a resolving guard (EvalSymlinks on the candidate and
// a prefix check against the resolved fixture directory; mind a `..` after a link,
// a dangling link, and a comma in a name) before the new command's examples are run.
// Hard links (os.Link, used for atomic same-directory file creation) cannot name a path
// outside the directory they are created in and are not covered by this premise.
func TestNoProductionCodeCreatesASymlink2813(t *testing.T) {
	root := filepath.Join("..", "..")
	var found []string
	scanned := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		f, perr := parser.ParseFile(token.NewFileSet(), p, src, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		scanned++
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Symlink" {
				found = append(found, p)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d non-test Go files; the walk is not reaching the module (root %s)", scanned, root)
	}
	if len(found) > 0 {
		t.Errorf("non-test code creates a symlink (%v): the example runners' containment premise (cli#2813) no longer holds; add a resolving guard to unsafeExamplePath1053 before running that command's examples", found)
	}
}
