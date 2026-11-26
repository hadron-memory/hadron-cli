package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// cli#3195: both Slack tokens are read through cmdutil.ReadConnectionToken.
//
// A source pin, because no run of the command can tell the two readers apart:
// "slack connect" refuses "-" on a terminal for either flag before it opens any
// source, and on every other input ReadConnectionToken is ReadSecretInput. The
// call sites keep the helper so that its refusal, with "Nothing was sent", is
// what answers if that earlier check changes. It reads call syntax only: it does not follow a reader stored
// in a variable or wrapped in another function, so such a change fails here too
// (a flag goes missing) and is reviewed by hand.
func TestSlackTokensAreReadThroughTheConnectionReader3195(t *testing.T) {
	fset := token.NewFileSet()
	var flags []string
	err := filepath.WalkDir("slack", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch e := n.(type) {
			case *ast.SelectorExpr:
				if e.Sel.Name == "ReadSecretInput" {
					t.Errorf("%s reads a secret with ReadSecretInput; use cmdutil.ReadConnectionToken", fset.Position(e.Pos()))
				}
			case *ast.CallExpr:
				sel, ok := e.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "ReadConnectionToken" || len(e.Args) < 3 {
					return true
				}
				lit, ok := e.Args[2].(*ast.BasicLit)
				if !ok {
					t.Errorf("%s: the flag name must be a string literal", fset.Position(e.Pos()))
					return true
				}
				name, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Errorf("%s: %v", fset.Position(e.Pos()), err)
				}
				flags = append(flags, name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(flags)
	if want := []string{"app-token-file", "bot-token-file"}; !slices.Equal(flags, want) {
		t.Errorf("ReadConnectionToken reads %v, want %v", flags, want)
	}
}
