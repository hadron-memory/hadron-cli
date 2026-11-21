package output

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// cli#2439: SanitizeLine is the named no-bound sanitizer. The nine closed call
// sites below used to spell the same thing as SafeLine with a bound that can
// never truncate (math.MaxInt, or len(x)+1 / RuneCountInString(x)+1). This
// guard fails when one of them goes back to that spelling. Fixed-bound
// SafeLine calls elsewhere mean to truncate and are not in the list.
var noBoundSites2439 = map[string]int{ // file (repo-relative from internal/) -> calls that must use SanitizeLine
	"api/batch_edges.go":                 2,
	"cmd/root.go":                        2,
	"cmd/team/wakes.go":                  1,
	"cmd/auth/passkey.go":                1,
	"cmd/auth/passkey_revoke_page.go":    1,
	"cmd/billing/billing_write.go":       1,
	"cmd/memory/config_template_bulk.go": 1,
	"cmd/team/worker_title.go":           1,
}

// neverTruncatingBound reports whether e is a bound that cannot truncate:
// math.MaxInt, len(x)+1, or utf8.RuneCountInString(x)+1.
func neverTruncatingBound2439(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.SelectorExpr:
		x, ok := v.X.(*ast.Ident)
		return ok && x.Name == "math" && v.Sel.Name == "MaxInt"
	case *ast.BinaryExpr:
		lit, ok := v.Y.(*ast.BasicLit)
		if v.Op != token.ADD || !ok || lit.Value != "1" {
			return false
		}
		call, ok := v.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			return f.Name == "len"
		case *ast.SelectorExpr:
			return f.Sel.Name == "RuneCountInString"
		}
	}
	return false
}

func TestNoBoundSafeLineSitesUseSanitizeLine2439(t *testing.T) {
	for rel, want := range noBoundSites2439 {
		path := filepath.Join("..", filepath.FromSlash(rel))
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		sanitize := 0
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "output" {
				return true
			}
			switch sel.Sel.Name {
			case "SafeLine":
				if len(call.Args) == 2 && neverTruncatingBound2439(call.Args[1]) {
					t.Errorf("%s: output.SafeLine with a never-truncating bound; use output.SanitizeLine", rel)
				}
			case "SanitizeLine":
				sanitize++
			}
			return true
		})
		if sanitize != want {
			t.Errorf("%s: %d output.SanitizeLine call(s), want %d", rel, sanitize, want)
		}
	}
}
