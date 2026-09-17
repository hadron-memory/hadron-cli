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

// A structural guard, because the fixture-keyed command tests cannot provide
// one on their own: the fake GraphQL server is keyed by OPERATION NAME, so
// reverting a call site to gen.CreateNode and renaming the fixture back leaves
// the suite green. Nothing in it asserts which door the authoring commands use.
//
// This asserts it directly, and covers all seven sites uniformly rather than
// one per hand-written test — including any site added later, which is the case
// a per-command test cannot cover because nobody writes the test for the call
// site they forgot.
func TestAuthoringCommandsDoNotUseTheGenericCreate(t *testing.T) {
	for _, pkg := range authoringPkgs {
		for _, hit := range genCreateNodeCalls(t, filepath.Join("..", "cmd", pkg)) {
			t.Errorf("%s: writes through the generic gen.CreateNode; an authoring "+
				"command must use api.AuthorProtectedNode (#606), or `protectedLocs` "+
				"will refuse the tool that owns the corpus", hit)
		}
	}
}

// genCreateNodeCalls returns the source positions of every `gen.CreateNode(...)`
// call in the package's non-test files.
//
// It parses rather than greps, so the identifier is matched only in CODE — a
// comment or doc string naming `gen.CreateNode` to explain why a file avoids it
// does not trip the guard. That is the #564 lesson from the exit-code guard
// next door, which had the same defect as a line-based regex.
//
// updateNode is deliberately NOT matched: the door is create/upsert only, and
// the update-side one is hadron-server#1192. When that lands, the four update
// sites listed on cli#606 move too, and `UpdateNode` belongs in here.
func genCreateNodeCalls(t *testing.T, dir string) []string {
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
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "gen" && sel.Sel.Name == "CreateNode" {
				hits = append(hits, fset.Position(call.Pos()).String())
			}
			return true
		})
	}
	return hits
}

// Two-directional: the guard above must actually be able to SEE a gen.CreateNode
// call, or it passes on an empty search and proves nothing. `node` is a package
// that legitimately still uses the generic create, so finding one there shows
// the detector fires.
func TestAuthoringGuardCanSeeAGenericCreate(t *testing.T) {
	if len(genCreateNodeCalls(t, filepath.Join("..", "cmd", "node"))) == 0 {
		t.Fatal("the guard's matcher found no gen.CreateNode anywhere, so its silence on the " +
			"authoring packages means nothing — `hadron node add` still uses the generic create")
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
