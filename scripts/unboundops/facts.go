package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"reflect"
	"strings"

	gql "github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"go.yaml.in/yaml/v3"
	"golang.org/x/tools/go/packages"
)

const (
	genPkgPath   = "github.com/hadron-memory/hadron-cli/internal/api/gen"
	genqlientCfg = "genqlient.yaml"
)

// productionPackages are the packages whose code counts as reaching the
// server. Tests never count, and the generated package does not reach itself.
var productionPackages = []string{"./cmd/...", "./internal/..."}

func parseSchema(src string) (map[string]*gql.Definition, error) {
	doc, err := parser.ParseSchema(&gql.Source{Name: schemaPath, Input: src})
	if err != nil {
		return nil, err
	}
	out := map[string]*gql.Definition{}
	for _, d := range doc.Definitions {
		out[d.Name] = d
	}
	for _, e := range doc.Extensions {
		if d := out[e.Name]; d != nil {
			d.Fields = append(d.Fields, e.Fields...)
		}
	}
	return out, nil
}

// docOp is one operation in internal/api/queries: its name (which is also the
// generated function's name) and, per root field it selects, the argument
// paths it passes. A path is "input" for an argument and "input.memoryId" for
// a field of an object literal written in the document; it maps to true when
// its value is opaque to the document (a variable, so what reaches the server
// is whatever Go puts in it) and false when the document spells it out.
type docOp struct {
	name string
	root string // Query | Mutation
	args map[string]map[string]bool
}

// passedPaths records the paths a value passes under prefix.
func passedPaths(into map[string]bool, prefix string, v *gql.Value) {
	if v.Kind != gql.ObjectValue {
		into[prefix] = into[prefix] || v.Kind == gql.Variable
		return
	}
	if _, ok := into[prefix]; !ok {
		into[prefix] = false
	}
	for _, c := range v.Children {
		passedPaths(into, prefix+"."+c.Name, c.Value)
	}
}

func parseDocuments(paths []string) ([]docOp, error) {
	var out []docOp
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		doc, err := parser.ParseQuery(&gql.Source{Name: p, Input: string(src)})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		for _, op := range doc.Operations {
			o := docOp{name: op.Name, root: "Query", args: map[string]map[string]bool{}}
			if op.Operation == gql.Mutation {
				o.root = "Mutation"
			}
			for _, sel := range op.SelectionSet {
				f, ok := sel.(*gql.Field)
				if !ok {
					continue
				}
				// An alias selects the same server field again: what is reached
				// is the field's own name, with the union of the arguments.
				passed := o.args[f.Name]
				if passed == nil {
					passed = map[string]bool{}
				}
				for _, a := range f.Arguments {
					passedPaths(passed, a.Name, a.Value)
				}
				o.args[f.Name] = passed
			}
			out = append(out, o)
		}
	}
	return out, nil
}

// goUses is what production code references: the generated operation
// functions it calls, the input-object fields it names ("AgentFilter.ownedByMe"),
// and the input objects it decodes JSON straight into ("CreateNodeInput"),
// which reaches every field of that input.
type goUses struct {
	funcs   map[string]bool
	fields  map[string]bool
	decoded map[string]bool
}

// loadGoUses type-checks the production packages under dir and collects
// their uses.
// inputs is the schema's input-object names; bindings maps a GraphQL type to
// the hand-authored Go type genqlient.yaml binds it to.
func loadGoUses(dir string, patterns []string, inputs map[string]bool, bindings map[string]string) (*goUses, error) {
	cfg := &packages.Config{Dir: dir, Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax | packages.NeedImports | packages.NeedDeps}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	var loadErrs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
	})
	if len(loadErrs) > 0 {
		return nil, fmt.Errorf("load packages: %s", strings.Join(loadErrs, "; "))
	}

	// Which Go types stand for which GraphQL inputs: genqlient names a
	// generated input after the GraphQL type; a bound one is named in the
	// config by its full Go path.
	bound := map[string]string{}
	for g, goType := range bindings {
		bound[goType] = g
	}
	inputOf := map[*types.TypeName]string{}
	fieldOf := map[*types.Var]string{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.Types == nil {
			return
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			g := bound[p.PkgPath+"."+name]
			if g == "" && p.PkgPath == genPkgPath {
				g = name
			}
			if g == "" || !inputs[g] {
				continue
			}
			st, ok := tn.Type().Underlying().(*types.Struct)
			if !ok {
				continue
			}
			inputOf[tn] = g
			for i := 0; i < st.NumFields(); i++ {
				tag, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ",")
				if tag != "" && tag != "-" {
					fieldOf[st.Field(i)] = g + "." + tag
				}
			}
		}
	})

	u := &goUses{funcs: map[string]bool{}, fields: map[string]bool{}, decoded: map[string]bool{}}
	for _, p := range pkgs {
		if p.PkgPath == genPkgPath || p.TypesInfo == nil {
			continue
		}
		for _, obj := range p.TypesInfo.Uses {
			switch o := obj.(type) {
			case *types.Func:
				if o.Pkg() != nil && o.Pkg().Path() == genPkgPath && o.Parent() == o.Pkg().Scope() {
					u.funcs[o.Name()] = true
				}
			case *types.Var:
				if k, ok := fieldOf[o]; ok {
					u.fields[k] = true
				}
			}
		}
		for _, file := range p.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if tn := decodeTarget(p.TypesInfo, call); tn != nil && inputOf[tn] != "" {
						u.decoded[inputOf[tn]] = true
					}
				}
				return true
			})
		}
	}
	return u, nil
}

// decodeTarget is the named type a json.Unmarshal or Decoder.Decode call
// decodes into, when its target is a pointer to a named type.
func decodeTarget(info *types.Info, call *ast.CallExpr) *types.TypeName {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) == 0 {
		return nil
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "encoding/json" || (fn.Name() != "Unmarshal" && fn.Name() != "Decode") {
		return nil
	}
	ptr, ok := info.TypeOf(call.Args[len(call.Args)-1]).(*types.Pointer)
	if !ok {
		return nil
	}
	if named, ok := ptr.Elem().(*types.Named); ok {
		return named.Obj()
	}
	return nil
}

// genqlientBindings maps a GraphQL type name to the Go type genqlient.yaml
// binds it to.
func genqlientBindings(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Bindings map[string]struct {
			Type string `yaml:"type"`
		} `yaml:"bindings"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for g, b := range cfg.Bindings {
		out[g] = b.Type
	}
	return out, nil
}

// facts is what the CLI demonstrably reaches, derived from the code rather
// than claimed in the matrix.
//
// A root field is REACHED when an operation document selects it AND
// production code calls that operation's generated function: a document nobody
// calls is bound but unreachable, and counts as not reached. An argument path
// is passed when a reached operation passes it. Below a variable, an
// input-object field is reached when production code names the Go struct
// field genqlient maps it to (a keyed literal, an assignment or a read), or
// decodes JSON straight into its struct.
type facts struct {
	types map[string]*gql.Definition
	roots map[string]bool            // "Query.agents"
	args  map[string]map[string]bool // "Query.agents" → passed paths (see docOp)
	uses  *goUses
}

func deriveFacts(types map[string]*gql.Definition, ops []docOp, uses *goUses) *facts {
	f := &facts{types: types, roots: map[string]bool{}, args: map[string]map[string]bool{}, uses: uses}
	for _, op := range ops {
		if !uses.funcs[op.name] {
			continue
		}
		for field, passed := range op.args {
			key := op.root + "." + field
			f.roots[key] = true
			if f.args[key] == nil {
				f.args[key] = map[string]bool{}
			}
			for p, opaque := range passed {
				f.args[key][p] = f.args[key][p] || opaque
			}
		}
	}
	return f
}

// item is one argument-level matrix key the facts produce, with what the
// generator needs to decide or check its disposition.
type item struct {
	key        string // "Query.agents(filter.ownedByMe)"
	reached    bool
	deprecated string // the server's @deprecated reason, "" when not deprecated
}

// argumentItems walks every argument of every reached root field. Filters is
// every direct field of a reached list query's filter input, reached or not.
// Arguments is every other argument or input-object field the CLI cannot
// reach, at the shallowest unreached path: a field below one that is not
// passed is not listed again. The walk descends through an input object once
// per type per path, so a recursive input ends.
func (f *facts) argumentItems() (arguments, filters []item) {
	for _, root := range []string{"Query", "Mutation"} {
		def := f.types[root]
		if def == nil {
			continue
		}
		for _, field := range def.Fields {
			key := root + "." + field.Name
			if !f.roots[key] {
				continue
			}
			for _, a := range field.Arguments {
				w := walk{f: f, root: key}
				if root == "Query" && isFilter(f.types[a.Type.Name()]) {
					w.filterArg = a.Name
				}
				w.visit(a.Name, a.Type.Name(), deprecation(a.Directives), false, map[string]bool{})
				arguments = append(arguments, w.arguments...)
				filters = append(filters, w.filters...)
			}
		}
	}
	return arguments, filters
}

func isFilter(def *gql.Definition) bool {
	return def != nil && def.Kind == gql.InputObject && strings.HasSuffix(def.Name, "Filter")
}

func deprecation(d gql.DirectiveList) string {
	dep := d.ForName("deprecated")
	if dep == nil {
		return ""
	}
	if r := dep.Arguments.ForName("reason"); r != nil && r.Value != nil && r.Value.Raw != "" {
		return r.Value.Raw
	}
	return "deprecated by the server (no reason given)"
}

type walk struct {
	f         *facts
	root      string // "Query.agents"
	filterArg string // the argument whose direct fields are Filters items
	arguments []item
	filters   []item
}

func (w *walk) emit(path string, reached bool, deprecated string) {
	it := item{key: w.root + "(" + path + ")", reached: reached, deprecated: deprecated}
	if w.filterArg != "" && strings.Count(path, ".") == 1 && strings.HasPrefix(path, w.filterArg+".") {
		w.filters = append(w.filters, it)
	} else if !reached {
		w.arguments = append(w.arguments, it)
	}
}

// visit decides path (of type typeName) and descends into it. viaGo is true
// once the walk is below a variable, where the Go code decides what is set.
func (w *walk) visit(path, typeName, deprecated string, viaGo bool, onPath map[string]bool) {
	def := w.f.types[typeName]
	if !viaGo {
		opaque, passed := w.f.args[w.root][path]
		if !passed {
			// An unpassed filter argument still lists each of its fields.
			if path == w.filterArg && def != nil {
				for _, fld := range def.Fields {
					w.emit(path+"."+fld.Name, false, deprecation(fld.Directives))
				}
				return
			}
			w.emit(path, false, deprecated)
			return
		}
		viaGo = opaque
	}
	if strings.Contains(path, ".") {
		w.emit(path, true, deprecated)
	}
	if def == nil || def.Kind != gql.InputObject || onPath[typeName] {
		return
	}
	onPath[typeName] = true
	defer delete(onPath, typeName)
	for _, fld := range def.Fields {
		sub := path + "." + fld.Name
		// Below a variable the Go code decides, unless another operation
		// spells the same field out in its document.
		_, literal := w.f.args[w.root][sub]
		if viaGo && !literal && !w.f.uses.decoded[typeName] && !w.f.uses.fields[typeName+"."+fld.Name] {
			w.emit(sub, false, deprecation(fld.Directives))
			continue
		}
		w.visit(sub, fld.Type.Name(), deprecation(fld.Directives), viaGo, onPath)
	}
}
