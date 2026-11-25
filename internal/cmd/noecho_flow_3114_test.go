package cmd

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// cli#3114: the #3029 guard sees `args[…]` written inside a refusal. This one follows the value: into a
// copied or renamed variable (ref := args[0], rest := cmd.Flags().Args()), a struct field, a closure, a
// helper's parameter or result, in this package or another, until it reaches a refusal constructor.
// There is no allowlist.
//
// Its reach, said plainly: it starts from positional arguments (a RunE-shaped `[]string` parameter,
// FlagSet.Args/Arg). Flag values are pinned by behavior (noecho_3114_test.go), not by this guard. It is
// flow-insensitive, does not look inside error values, and does not follow a value through an interface
// method or a server answer.
//
// noEchoPendingDirs are the directory groups whose slice of #3114 has not landed. They are not exempt:
// each slice deletes its group, and the last one deletes this list.
var noEchoPendingDirs = []string{
	"internal/cmd/team/",
}

type echoFlow struct {
	objs    map[types.Object]bool // tainted variables, parameters, fields
	params  map[string]bool       // FullName#i, for callers in another package
	results map[string]bool       // FullName#i
	lits    map[types.Object]*ast.FuncLit
	paths   map[types.Object]bool // local file paths: handed straight to os.Open, Stat, ReadFile
	changed bool
}

func (e *echoFlow) markObj(o types.Object) {
	// A generated request or answer field is the wire, not this program's copy.
	if v, ok := o.(*types.Var); ok && v.IsField() && v.Pkg() != nil && strings.HasSuffix(v.Pkg().Path(), "/internal/api/gen") {
		return
	}
	if o != nil && !e.objs[o] && textual(o.Type()) {
		e.objs[o] = true
		e.changed = true
	}
}

func markKey(m map[string]bool, k string, e *echoFlow) {
	if !m[k] {
		m[k] = true
		e.changed = true
	}
}

// textual: only text, lists of text, and `any` (a formatter's variadic) carry what was typed.
func textual(t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		return u.Info()&types.IsString != 0
	case *types.Slice:
		return textual(u.Elem())
	case *types.Pointer:
		return textual(u.Elem())
	case *types.Interface:
		return u.Empty()
	}
	return false
}

func calleeOf(info *types.Info, call *ast.CallExpr) *types.Func {
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		fn, _ := info.Uses[f].(*types.Func)
		return fn
	case *ast.SelectorExpr:
		fn, _ := info.Uses[f.Sel].(*types.Func)
		return fn
	}
	return nil
}

// passThrough packages return their text argument, changed at most in form.
func passThrough(fn *types.Func) bool {
	if fn == nil || fn.Pkg() == nil {
		return false
	}
	switch fn.Pkg().Path() {
	case "strings", "strconv", "fmt":
		return true
	}
	// A text-to-text helper (ShellQuote, SafeCell, CanonicalMemoryRef) is judged by the value
	// it is given here, not by what some other caller gives it.
	sig := fn.Type().(*types.Signature)
	return strings.HasPrefix(fn.Pkg().Path(), "github.com/hadron-memory/hadron-cli/") && sig.Params().Len() == 1 && sig.Results().Len() == 1 &&
		textual(sig.Params().At(0).Type()) && textual(sig.Results().At(0).Type())
}

func (e *echoFlow) carries(info *types.Info, x ast.Expr, res int) bool {
	switch v := ast.Unparen(x).(type) {
	case *ast.Ident:
		o := info.Uses[v]
		return o != nil && e.objs[o] && !e.paths[o]
	case *ast.SelectorExpr:
		return e.objs[info.Uses[v.Sel]]
	case *ast.IndexExpr:
		return e.carries(info, v.X, 0)
	case *ast.SliceExpr:
		return e.carries(info, v.X, 0)
	case *ast.StarExpr:
		return e.carries(info, v.X, 0)
	case *ast.UnaryExpr:
		return e.carries(info, v.X, 0)
	case *ast.BinaryExpr:
		return v.Op == token.ADD && (e.carries(info, v.X, 0) || e.carries(info, v.Y, 0))
	case *ast.CompositeLit:
		for _, el := range v.Elts {
			if _, kv := el.(*ast.KeyValueExpr); !kv && e.carries(info, el, 0) {
				return true
			}
		}
	case *ast.CallExpr:
		if tv, ok := info.Types[v.Fun]; ok && tv.IsType() {
			return len(v.Args) == 1 && e.carries(info, v.Args[0], 0)
		}
		if id, ok := ast.Unparen(v.Fun).(*ast.Ident); ok && id.Name == "append" {
			for _, a := range v.Args {
				if e.carries(info, a, 0) {
					return true
				}
			}
			return false
		}
		fn := calleeOf(info, v)
		if fn == nil {
			return false
		}
		if fn.FullName() == "(*github.com/spf13/pflag.FlagSet).Args" || fn.FullName() == "(*github.com/spf13/pflag.FlagSet).Arg" {
			return true
		}
		if passThrough(fn) {
			if t := info.TypeOf(v); t != nil {
				if tup, ok := t.(*types.Tuple); ok {
					if res >= tup.Len() || !textual(tup.At(res).Type()) {
						return false
					}
				} else if !textual(t) {
					return false
				}
			}
			for _, a := range v.Args {
				if e.carries(info, a, 0) {
					return true
				}
			}
			return false
		}
		return e.results[fn.FullName()+"#"+strconv.Itoa(res)]
	}
	return false
}

func (e *echoFlow) assign(info *types.Info, lhs ast.Expr) {
	switch l := ast.Unparen(lhs).(type) {
	case *ast.Ident:
		if o := info.Defs[l]; o != nil {
			e.markObj(o)
		} else {
			e.markObj(info.Uses[l])
		}
	case *ast.SelectorExpr:
		e.markObj(info.Uses[l.Sel])
	case *ast.IndexExpr:
		e.assign(info, l.X)
	}
}

func isRunShaped(info *types.Info, ft *ast.FuncType) *ast.Ident {
	if ft.Params == nil {
		return nil
	}
	var idents []*ast.Ident
	for _, f := range ft.Params.List {
		idents = append(idents, f.Names...)
	}
	if len(idents) != 2 {
		return nil
	}
	a, b := info.Defs[idents[0]], info.Defs[idents[1]]
	if a == nil || b == nil || a.Type().String() != "*github.com/spf13/cobra.Command" || b.Type().String() != "[]string" {
		return nil
	}
	return idents[1]
}

// walk is one pass over a package: sources, assignments, calls into helpers, and returns.
func (e *echoFlow) walk(p *packages.Package) {
	info := p.TypesInfo
	for _, f := range p.Syntax {
		// returns are attributed to the innermost function.
		var visit func(n ast.Node, fn *types.Func)
		visit = func(root ast.Node, fn *types.Func) {
			ast.Inspect(root, func(n ast.Node) bool {
				switch s := n.(type) {
				case *ast.FuncDecl:
					if s.Body == nil {
						return false
					}
					decl, _ := info.Defs[s.Name].(*types.Func)
					if id := isRunShaped(info, s.Type); id != nil {
						e.markObj(info.Defs[id])
					}
					if decl != nil {
						sig := decl.Type().(*types.Signature)
						for i := 0; i < sig.Params().Len(); i++ {
							if e.params[decl.FullName()+"#"+strconv.Itoa(i)] {
								e.markObj(sig.Params().At(i))
							}
						}
					}
					visit(s.Body, decl)
					return false
				case *ast.FuncLit:
					if id := isRunShaped(info, s.Type); id != nil {
						e.markObj(info.Defs[id])
					}
					visit(s.Body, nil)
					return false
				case *ast.AssignStmt:
					for i, l := range s.Lhs {
						if len(s.Rhs) == len(s.Lhs) {
							if lit, ok := s.Rhs[i].(*ast.FuncLit); ok {
								if id, ok := l.(*ast.Ident); ok && info.Defs[id] != nil {
									e.lits[info.Defs[id]] = lit
								}
							}
							if e.carries(info, s.Rhs[i], 0) {
								e.assign(info, l)
							}
						} else if e.carries(info, s.Rhs[0], i) {
							e.assign(info, l)
						}
					}
				case *ast.ValueSpec:
					for i, id := range s.Names {
						if len(s.Values) == len(s.Names) && e.carries(info, s.Values[i], 0) ||
							len(s.Values) == 1 && len(s.Names) > 1 && e.carries(info, s.Values[0], i) {
							e.markObj(info.Defs[id])
						}
					}
				case *ast.RangeStmt:
					if s.Value != nil && e.carries(info, s.X, 0) {
						e.assign(info, s.Value)
					}
				case *ast.KeyValueExpr:
					if k, ok := s.Key.(*ast.Ident); ok && e.carries(info, s.Value, 0) {
						if fv, ok := info.Uses[k].(*types.Var); ok && fv.IsField() {
							e.markObj(fv)
						}
					}
				case *ast.ReturnStmt:
					if fn != nil {
						for i, r := range s.Results {
							sig := fn.Type().(*types.Signature)
							if len(s.Results) == sig.Results().Len() {
								if textual(sig.Results().At(i).Type()) && e.carries(info, r, 0) {
									markKey(e.results, fn.FullName()+"#"+strconv.Itoa(i), e)
								}
							} else {
								for j := 0; j < sig.Results().Len(); j++ {
									if textual(sig.Results().At(j).Type()) && e.carries(info, r, j) {
										markKey(e.results, fn.FullName()+"#"+strconv.Itoa(j), e)
									}
								}
							}
						}
					}
				case *ast.CallExpr:
					e.call(info, s)
				}
				return true
			})
		}
		visit(f, nil)
	}
}

func (e *echoFlow) call(info *types.Info, c *ast.CallExpr) {
	if id, ok := ast.Unparen(c.Fun).(*ast.Ident); ok {
		if lit := e.lits[info.Uses[id]]; lit != nil {
			var ps []*ast.Ident
			for _, f := range lit.Type.Params.List {
				ps = append(ps, f.Names...)
			}
			for i, a := range c.Args {
				if len(ps) > 0 && e.carries(info, a, 0) {
					e.markObj(info.Defs[ps[min(i, len(ps)-1)]])
				}
			}
			return
		}
	}
	fn := calleeOf(info, c)
	if fn != nil && len(c.Args) > 0 {
		switch fn.FullName() {
		case "os.Open", "os.Stat", "os.ReadFile":
			// A variable used AS a file name names a local file (the table's "path" class, OUT).
			if id, ok := ast.Unparen(c.Args[0]).(*ast.Ident); ok && info.Uses[id] != nil && !e.paths[info.Uses[id]] {
				e.paths[info.Uses[id]] = true
				e.changed = true
			}
		}
	}
	if fn == nil || fn.Pkg() == nil || !strings.HasPrefix(fn.Pkg().Path(), "github.com/hadron-memory/hadron-cli/internal/cmd") {
		return
	}
	sig := fn.Type().(*types.Signature)
	for i, a := range c.Args {
		j := min(i, sig.Params().Len()-1)
		if j >= 0 && e.carries(info, a, 0) {
			markKey(e.params, fn.FullName()+"#"+strconv.Itoa(j), e)
		}
	}
}

func refusalEchoes(pkgs []*packages.Package) []string {
	e := &echoFlow{objs: map[types.Object]bool{}, params: map[string]bool{}, results: map[string]bool{}, lits: map[types.Object]*ast.FuncLit{}, paths: map[types.Object]bool{}}
	for e.changed = true; e.changed; {
		e.changed = false
		for _, p := range pkgs {
			e.walk(p)
		}
	}
	seen := map[string]bool{}
	for _, p := range pkgs {
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok || !isRefusalConstructor(c.Fun) {
					return true
				}
				for _, a := range c.Args {
					if e.carries(p.TypesInfo, a, 0) {
						pos := p.Fset.Position(c.Pos())
						seen[fmt.Sprintf("%s:%d", filepath.ToSlash(pos.Filename), pos.Line)] = true
					}
				}
				return true
			})
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func loadForEcho(t *testing.T, dir string, overlay map[string][]byte, patterns ...string) []*packages.Package {
	t.Helper()
	pkgs, err := packages.Load(&packages.Config{
		Dir: dir, Overlay: overlay,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedExportFile,
	}, patterns...)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		for _, pe := range p.Errors {
			t.Fatalf("load %s: %v", p.PkgPath, pe)
		}
	}
	return pkgs
}

func TestNoRefusalEchoesATypedValueThroughACopyOrHelper3114(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	pkgs := loadForEcho(t, root, nil, "./internal/cmd/...", "./internal/cmdutil/...")
	if len(pkgs) < 40 {
		t.Fatalf("the guard loaded only %d packages", len(pkgs))
	}
	var found []string
	for _, hit := range refusalEchoes(pkgs) {
		rel, err := filepath.Rel(root, hit)
		if err != nil {
			t.Fatal(err)
		}
		rel = filepath.ToSlash(rel)
		pending := false
		for _, d := range noEchoPendingDirs {
			pending = pending || strings.HasPrefix(rel, d)
		}
		if !pending {
			found = append(found, rel)
		}
	}
	if len(found) > 0 {
		t.Errorf("%d refusal(s) echo a positional argument through a copy or a helper; name its kind and say the value is not shown (#2898 policy, cli#3114):\n  %s",
			len(found), strings.Join(found, "\n  "))
	}
}

// The guard itself is pinned: every shape the issue names is seen, and the safe ones are not.
func TestNoEchoFlowGuardSeesEveryShape3114(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	src := `package version

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type shape struct{ ref string }

func run(cmd *cobra.Command, args []string) error {
	ref := strings.TrimSpace(args[0])
	_ = exitcode.Newf(2, "x %q", ref) // HIT copy
	rest := cmd.Flags().Args()
	_ = exitcode.Newf(2, "x %q", rest[0]) // HIT renamed
	for _, p := range args {
		_ = fmt.Errorf("x %s", p) // HIT range
	}
	s := shape{ref: ref}
	_ = exitcode.Newf(2, "x %q", s.ref) // HIT field
	hint := fmt.Sprintf("run get %s", ref)
	_ = exitcode.Newf(2, "x %s", hint) // HIT suggestion text
	usage := func(format string, a ...any) error { return exitcode.Newf(2, format, a...) } // HIT closure
	_ = usage("x %q", ref)
	got, _ := parse(ref)
	_ = exitcode.Newf(2, "x %q", got) // HIT result
	_ = exitcode.Newf(2, "x %q", quote("constant"))
	_ = exitcode.Newf(2, "x %d arguments", len(args))
	file := args[1]
	if _, err := os.Stat(file); err != nil {
		return exitcode.Newf(2, "cannot read %s", file)
	}
	return helper(ref)
}

func helper(ref string) error { return exitcode.Newf(4, "no %q", ref) } // HIT parameter

func parse(ref string) (string, error) { return quote(ref), nil }

func quote(s string) string { return "'" + s + "'" }

func safe(name string) error { return exitcode.Newf(4, "no %q", name) }

var _ = []any{run, safe}
`
	pkgs := loadForEcho(t, root, map[string][]byte{filepath.Join(root, "internal/cmd/version/zz_shapes_3114.go"): []byte(src)}, "./internal/cmd/version")
	var got []int
	for _, h := range refusalEchoes(pkgs) {
		if i := strings.LastIndex(h, "zz_shapes_3114.go:"); i >= 0 {
			n, _ := strconv.Atoi(h[i+len("zz_shapes_3114.go:"):])
			got = append(got, n)
		}
	}
	sort.Ints(got)
	var want []int
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "// HIT") {
			want = append(want, i+1)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("flagged lines %v, want %v", got, want)
	}
}
