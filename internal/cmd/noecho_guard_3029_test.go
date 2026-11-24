package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// cli#3029 guard: a refusal built from a command's positional arguments prints back what the operator
// typed, which may be a pasted secret (#2898 policy; #2902, #3029). The guard reads every non-test Go
// file under internal/cmd as syntax, so a call that spans lines, uses any verb (%q, %s, %v), or wraps the
// value (output.SafeCell(args[0]), strings.TrimSpace(args[1])) is seen whole. It flags any
// exitcode.Newf / exitcode.New / fmt.Errorf / errors.New call with an argument that indexes or slices a
// variable named args.
//
// Its reach, said plainly: it sees `args[…]` written inside the call. A value copied to another variable
// first (ref := args[0]) or handed to a helper that formats it is not seen; those are reviewed by hand.

// echoesPositionalArgs returns the line of every refusal constructor call in src whose arguments index
// or slice `args`.
func echoesPositionalArgs(t *testing.T, fset *token.FileSet, name string, src any) []int {
	t.Helper()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var lines []int
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isRefusalConstructor(call.Fun) {
			return true
		}
		hit := false
		for _, a := range call.Args {
			ast.Inspect(a, func(m ast.Node) bool {
				var x ast.Expr
				switch e := m.(type) {
				case *ast.IndexExpr:
					x = e.X
				case *ast.SliceExpr:
					x = e.X
				}
				if id, ok := x.(*ast.Ident); ok && id.Name == "args" {
					hit = true
				}
				return !hit
			})
		}
		if hit {
			lines = append(lines, fset.Position(call.Pos()).Line)
		}
		return true
	})
	return lines
}

func isRefusalConstructor(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	switch pkg.Name + "." + sel.Sel.Name {
	case "exitcode.Newf", "exitcode.New", "fmt.Errorf", "errors.New":
		return true
	}
	return false
}

func TestNoRefusalEchoesAPositionalArg3029(t *testing.T) {
	fset := token.NewFileSet()
	var found []string
	files := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files++
		for _, line := range echoesPositionalArgs(t, fset, path, nil) {
			found = append(found, filepath.ToSlash(filepath.Join("internal/cmd", path))+":"+strconv.Itoa(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 100 {
		t.Fatalf("the guard read only %d files: it is not walking internal/cmd", files)
	}
	if len(found) > 0 {
		t.Errorf("%d refusal(s) build their text from a positional argument; name the argument and say the value is not shown instead (#2898 policy, cli#3029):\n  %s",
			len(found), strings.Join(found, "\n  "))
	}
}

// The guard itself is pinned: each shape below must be seen, and the safe ones must not.
func TestNoEchoGuardSeesEveryShape3029(t *testing.T) {
	src := `package x
func f(args []string, ref string) {
	_ = exitcode.Newf(exitcode.NotFound, "x %q not found", args[0])
	_ = exitcode.Newf(exitcode.Usage,
		"%q is the router itself", args[0])
	_ = exitcode.Newf(exitcode.Error, "x %s was not deleted", args[0])
	_ = exitcode.Newf(exitcode.NotFound, "x %s", output.SafeCell(args[1]))
	_ = exitcode.Newf(exitcode.Usage, "x %v", args[1:])
	_ = exitcode.New(exitcode.Usage, fmt.Errorf("x %q", args[0]))
	_ = fmt.Errorf("x %q", args[0])
	_ = errors.New("x " + args[0])
	_ = exitcode.Newf(exitcode.NotFound, "x not found (the ref given is not shown)")
	_ = exitcode.Newf(exitcode.NotFound, "x %q", ref)
	_ = exitcode.Newf(exitcode.Usage, "%d arguments", len(args))
	_ = gen.GetNode(ctx, client, args[0])
}
`
	got := echoesPositionalArgs(t, token.NewFileSet(), "shapes.go", src)
	want := []int{3, 4, 6, 7, 8, 9, 9, 10, 11}
	if len(got) != len(want) {
		t.Fatalf("flagged lines %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("flagged lines %v, want %v", got, want)
		}
	}
}
