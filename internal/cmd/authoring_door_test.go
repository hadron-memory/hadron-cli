package cmd

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// authoringPkgs are the command groups that own a protected corpus (#606).
// `hadron spec` writes the specs memory; `hadron coding` writes the `review:`
// and `tasks:` branches of a repo memory. Once a memory declares
// `Memory.protectedLocs`, the generic node write is REFUSED at those locs, so
// every write here must go through api.AuthorProtectedNode — the door that
// carries the bypass.
var authoringPkgs = []string{"spec", "coding"}

const (
	genPkgPath = "github.com/hadron-memory/hadron-cli/internal/api/gen"
	apiPkgPath = "github.com/hadron-memory/hadron-cli/internal/api"
)

// A structural guard, because the fixture-keyed command tests cannot provide
// one on their own: the fake GraphQL server is keyed by OPERATION NAME, so
// reverting a call site to gen.CreateNode and renaming the fixture back leaves
// the suite green. Nothing in it asserts which door the authoring commands use.
//
// It asserts both halves of the contract across ALL seven sites, rather than
// one per hand-written test — including any site added later, which is the case
// a per-command test structurally cannot cover, because nobody writes the test
// for the call site they forgot. That gap was real: the wire test below
// exercises `coding review create` alone, so flipping any `spec` site to
// upsert:true left it green (@copilot on PR #607).
func TestAuthoringCommandsUseTheDoorWithoutUpsert(t *testing.T) {
	for _, pkg := range authoringPkgs {
		for _, hit := range scanAuthoringWrites(t, filepath.Join("..", "cmd", pkg)) {
			t.Error(hit)
		}
	}
}

// scanAuthoringWrites reports every violation of #606's contract in a package's
// non-test files: a generic `createNode` write, or a door write that upserts.
//
// Packages are resolved by IMPORT PATH, not by the identifier spelling
// (@copilot on PR #607). Matching the literal `gen` would let a file importing
// the same package as `g` call `g.CreateNode` while the guard stayed green —
// protecting today's spelling rather than the rule.
//
// It parses rather than greps, so an identifier is matched only in CODE: a
// comment naming `gen.CreateNode` to explain why a file avoids it does not trip
// the guard. That is the #564 lesson from the exit-code guard next door.
//
// `UpdateNode` is deliberately NOT matched: the door is create/upsert only, and
// the update-side one is hadron-server#1192. When that lands, the four update
// sites listed on cli#606 move too, and it belongs in here.
func scanAuthoringWrites(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var hits []string
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("read %s: %v", path, rerr)
		}
		file, perr := parser.ParseFile(fset, path, src, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		pkgOf := importedPackages(file)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			pos := fset.Position(call.Pos()).String()
			switch {
			case pkgOf[x.Name] == genPkgPath && sel.Sel.Name == "CreateNode":
				hits = append(hits, pos+": writes through the generic createNode; an authoring "+
					"command must use api.AuthorProtectedNode (#606), or `protectedLocs` "+
					"will refuse the tool that owns the corpus")
			case pkgOf[x.Name] == apiPkgPath && sel.Sel.Name == "AuthorProtectedNode":
				// The upsert argument is last. `true` turns a documented
				// refusal into a silent overwrite of a permanent citation —
				// see the wrapper's doc comment for why no site wants it.
				if len(call.Args) == 0 || !isFalseLiteral(call.Args[len(call.Args)-1]) {
					hits = append(hits, pos+": must pass the literal false for upsert — "+
						"true would turn a documented refusal into a silent overwrite "+
						"of a permanent citation (#606)")
				}
			}
			return true
		})
	}
	return hits
}

// importedPackages maps each import's LOCAL name — its alias when it has one,
// otherwise the last path segment — to its import path.
//
// The last-segment fallback is right for this repo's imports and is what makes
// the unaliased `gen`/`api` spellings resolve; an import whose package name
// differs from its directory would need go/types, and none here does.
func importedPackages(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		local := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			local = path[i+1:]
		}
		if imp.Name != nil {
			local = imp.Name.Name
		}
		out[local] = path
	}
	return out
}

func isFalseLiteral(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "false"
}

// Two-directional: the scanner must actually be able to SEE a generic create,
// or it passes on an empty search and proves nothing. `node` legitimately still
// uses it, so finding one there shows the matcher fires — including through the
// import-path resolution, since that package imports `gen` unaliased.
func TestAuthoringGuardCanSeeAGenericCreate(t *testing.T) {
	if len(scanAuthoringWrites(t, filepath.Join("..", "cmd", "node"))) == 0 {
		t.Fatal("the guard's matcher found no generic createNode anywhere, so its silence on the " +
			"authoring packages means nothing — `hadron node add` still uses it")
	}
}

// The wire contract, and the decision worth pinning: the door is called with
// upsert FALSE.
//
// cli#606 suggested `upsert: true` for "the re-run case". No authoring command
// upserts today — all seven creates refuse a live loc, and two promise it in
// user-visible text (`spec new` exits Conflict with "<citation> already
// exists"; `coding review create`'s help says a check that already exists
// "fails rather than overwriting it"). Passing true would silently convert
// those refusals into overwrites of a permanent citation — the hole
// hadron-server #1182/#1184 had just closed on the generic surfaces.
//
// So this is not a style assertion. It pins a behaviour that a one-word edit
// would reverse, silently, with every other test still green.
func TestAuthoringDoorIsCalledWithoutUpsert(t *testing.T) {
	newNode := `{"id":"n_new","memoryId":"mem1","loc":"review:thin-resolver","name":"thin-resolver",
		"nodeType":"info","tags":["review","review-criteria"],"seq":null,"isRunnable":false,
		"updatedAt":"2026-08-04T00:00:00Z"}`
	confirmEdge := `{"id":"e_new","name":"Applies when a resolver changes","loc":"l","isRunnable":false,
		"priority":0,"target":{"id":"root","loc":"review","memoryId":"` + codingMem + `"}}`
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNode": {
			codingRootJSON("review", "", ""),
			codingNodeJSON("n_new", "review:thin-resolver", "", confirmEdge),
		},
		"AuthorProtectedNode": {`{"data":{"authorProtectedNode":` + newNode + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "create", "thin-resolver", "-m", codingMem,
		"--trigger", "a resolver changes", "--description", "Resolver fields stay thin.", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("create should succeed, got %v", err)
	}
	calls := captured["AuthorProtectedNode"]
	if len(calls) != 1 {
		t.Fatalf("expected exactly one call to the door, got %d", len(calls))
	}
	var vars struct {
		Upsert *bool `json:"upsert"`
	}
	if err := json.Unmarshal(calls[0], &vars); err != nil {
		t.Fatalf("decode variables: %v", err)
	}
	// Present, not merely absent: the mutation's own default is false, so an
	// omitted variable would also behave correctly today — but it would leave
	// the CLI's intent unstated and silently inherit whatever the server later
	// makes the default.
	if vars.Upsert == nil {
		t.Fatalf("upsert must be sent explicitly, not left to the server's default: %s", calls[0])
	}
	if *vars.Upsert {
		t.Errorf("the authoring door must be called with upsert=false — true would turn a " +
			"documented refusal into a silent overwrite of a permanent citation")
	}
}
