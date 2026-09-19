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

// governedPkgs maps a command-group package to the doors its writes must use
// (#1201). The value is the api wrapper each write is allowed to call.
//
// THE RULE MOVED, and the guard has to move with it. #606's rule was about
// ADDRESSES: `Memory.protectedLocs` refused the generic write at declared locs,
// so every write in these packages needed the one door. #1203 deleted that
// column. Protection is now by KIND, read off the RESULTING state of the write,
// so the question is no longer "is this package privileged" but "what will this
// node BE".
//
// `spec` writes spec-kind nodes and is fully governed. `coding` is MIXED, which
// is the interesting case: `review create` writes review-kind nodes and must use
// the review door, while `preflight create` writes non-runnable orientation
// nodes carrying no role — of no governed kind, so the generic `createNode` is
// CORRECT there and this guard must not call it a violation.
//
// That mix is why the guard keys on the FILE rather than the package, and why
// the classification is an ALLOW-LIST of the ungoverned rather than a list of
// the governed: a new file is governed by default, so forgetting to classify it
// fails the guard instead of silently exempting it.
//
// ungovernedFiles write nodes of no governed kind, so the generic surface is
// right for them. Listed EXPLICITLY rather than inferred from the absence of a
// door call: an exemption nobody can enumerate is a hiding place, and "this
// file happens not to call a door" is exactly the silence a forgotten call site
// produces.
var ungovernedFiles = map[string][]string{
	"coding": {"preflight_add.go"},
}

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
func TestAuthoringCommandsUseTheirKindsDoor(t *testing.T) {
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
// `UpdateNode` IS matched now. It was excluded while the door was create-only
// and the update side was hadron-server#1192; #1201 shipped both halves, so a
// governed node must not be rewritable through the generic surface either —
// which is the point of the update doors, since the gate reads the resulting
// state and an edit to a spec node still produces a spec node.
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
		ungoverned := false
		for _, u := range ungovernedFiles[filepath.Base(dir)] {
			if u == name {
				ungoverned = true
			}
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
			generic := pkgOf[x.Name] == genPkgPath &&
				(sel.Sel.Name == "CreateNode" || sel.Sel.Name == "UpdateNode")
			if generic && !ungoverned {
				hits = append(hits, pos+": writes a node through the generic "+sel.Sel.Name+
					" in a governed file; a write that produces a spec- or review-kind node must "+
					"use its own door (api.CreateSpecNode / api.UpdateSpecNode / api.CreateReviewNode), "+
					"because the gate reads the RESULTING state and refuses the generic surface (#1201). "+
					"If this node really is of no governed kind, say so in ungovernedFiles rather "+
					"than leaving the guard to infer it")
			}
			// The DELETED doors. Naming them explicitly turns "the server removed
			// the field" from a runtime failure into a test failure — which is
			// the whole lesson of #1203 landing while five call sites still
			// pointed at authorProtectedNode.
			if pkgOf[x.Name] == apiPkgPath &&
				(sel.Sel.Name == "AuthorProtectedNode" || sel.Sel.Name == "UpdateProtectedNode") {
				hits = append(hits, pos+": calls "+sel.Sel.Name+", which hadron-server #1203 DELETED — "+
					"use the per-kind door for what this node will be (#1201)")
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

// THE WIRE CONTRACT: `coding review create` writes through the REVIEW door and
// marks the node with the governed role.
//
// Both halves matter and neither implies the other. The door is how the write
// gets THROUGH the gate; the role is what the gate READS. A check written
// through the review door without `role: "review"` is ungoverned — the generic
// `updateNode` will happily rewrite it afterwards, which is precisely the
// removal a safety check must not permit.
//
// The upsert assertion this replaced is GONE because the argument is: the
// per-kind doors do not offer one, so "every call site passes false" is now the
// server's decision rather than a contract this repo has to keep. The refusal
// it protected survives — a re-run against a live loc is a NodeLocConflictError,
// which cli#610 maps to exit 5.
func TestReviewCreateUsesTheReviewDoorAndMarksTheRole(t *testing.T) {
	newNode := `{"id":"n_new","memoryId":"mem1","loc":"review:thin-resolver","name":"thin-resolver",
		"nodeType":"info","tags":["review","review-criteria"],"seq":null,"isRunnable":false,
		"role":"review","updatedAt":"2026-08-04T00:00:00Z"}`
	confirmEdge := `{"id":"e_new","name":"Applies when a resolver changes","loc":"l","isRunnable":false,
		"priority":0,"target":{"id":"root","loc":"review","memoryId":"` + codingMem + `"}}`
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNode": {
			codingRootJSON("review", "", ""),
			codingNodeJSON("n_new", "review:thin-resolver", "", confirmEdge),
		},
		"CreateReviewNode": {`{"data":{"createReviewNode":` + newNode + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "create", "thin-resolver", "-m", codingMem,
		"--trigger", "a resolver changes", "--description", "Resolver fields stay thin.", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("create should succeed, got %v", err)
	}
	calls := captured["CreateReviewNode"]
	if len(calls) != 1 {
		t.Fatalf("expected exactly one call to the review door, got %d", len(calls))
	}
	var vars struct {
		Input struct {
			Role *string `json:"role"`
		} `json:"input"`
	}
	if err := json.Unmarshal(calls[0], &vars); err != nil {
		t.Fatalf("decode variables: %v", err)
	}
	// Present, not merely absent. An unmarked node passes the generic surface,
	// so omitting the role would leave the check ungoverned while every other
	// test stayed green.
	if vars.Input.Role == nil {
		t.Fatalf("the governed role must be sent — without it the check is ungoverned: %s", calls[0])
	}
	if *vars.Input.Role != "review" {
		t.Errorf("role = %q, want \"review\" — the gate reads this, not the door's name", *vars.Input.Role)
	}
}

// `coding preflight create` writes through the GENERIC surface, and that is the
// re-route rather than a gap in it.
//
// Its route targets are non-runnable orientation nodes carrying no role, so they
// are of no governed kind — `Memory.protectedLocs`, which is what made
// `preflight` a protected ADDRESS, was deleted by #1203. Pinned because the
// tempting mistake is symmetry: sending it through `createTaskNode` would claim
// a kind it does not have, and would start failing the day a door asserts one.
func TestPreflightCreateUsesTheGenericSurface(t *testing.T) {
	newNode := `{"id":"n_route","memoryId":"mem1","loc":"findings:flaky-timer","name":"flaky-timer",
		"nodeType":"info","tags":[],"seq":null,"isRunnable":false,
		"updatedAt":"2026-08-04T00:00:00Z"}`
	confirmEdge := `{"id":"e_r","name":"to fix a flaky timer","loc":"l","isRunnable":false,
		"priority":0,"target":{"id":"root","loc":"preflight","memoryId":"` + codingMem + `"}}`
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNode": {
			codingRootJSON("preflight", "", ""),
			codingNodeJSON("n_route", "findings:flaky-timer", "", confirmEdge),
		},
		"CreateNode": {`{"data":{"createNode":` + newNode + `}}`},
		"CreateEdge": {`{"data":{"createEdge":` + newRouteEdgeJSON + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "create", "findings:flaky-timer", "-m", codingMem,
		"--route", "fix a flaky timer", "--description", "The countdown starts before the await.",
		"--no-body-line", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("create should succeed, got %v", err)
	}
	if len(captured["CreateNode"]) != 1 {
		t.Fatalf("expected one generic createNode, got %d", len(captured["CreateNode"]))
	}
	for _, door := range []string{"CreateTaskNode", "CreateSpecNode", "CreateReviewNode"} {
		if len(captured[door]) != 0 {
			t.Errorf("a route target is of no governed kind; it must not use %s", door)
		}
	}
	var vars struct {
		Input struct {
			Role       *string `json:"role"`
			IsRunnable *bool   `json:"isRunnable"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"][0], &vars)
	if vars.Input.Role != nil {
		t.Errorf("a route target must carry no governed role, got %q", *vars.Input.Role)
	}
	if vars.Input.IsRunnable != nil && *vars.Input.IsRunnable {
		t.Error("a runnable route target would be task-kind and would need the task door")
	}
}
