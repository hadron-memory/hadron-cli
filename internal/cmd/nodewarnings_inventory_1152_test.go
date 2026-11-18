package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// #1152 (follow-ups to #1143, items 1, 2 and 4): every mutation root that
// carries plain-pin response advice is pinned BY NAME, independently of the
// production list, so deleting one name from installNodeWriteWarnings fails
// here; and every node-named mutation in the typed queries is either in that set
// or explicitly classified, so a future node writer cannot silently miss advice.

// nodeWriteAdviceRoots1152 is the eligible set, written out independently of
// nodewarnings.go on purpose.
var nodeWriteAdviceRoots1152 = []string{
	"createNode", "createSpecNode", "createTaskNode", "createReviewNode",
	"updateNode", "updateSpecNode", "updateTaskNode", "updateReviewNode",
	"importNode", "mergeNodes", "cloneNode", "moveNode", "updateNodeData", "restoreNode", "restoreNodeRevision",
	"searchReplaceInNodes", "searchReplaceInSpecNodes",
}

// Each root is exercised through the real installNodeWriteWarnings path: a stub
// command sends `mutation { <root> { id } }` and the fake server answers with
// advice for that root, advice for a foreign root, and (for the failure control)
// an error. Stdout stays untouched; advice is stderr only.
func TestNodeWriteAdviceRootsAreEachPinned1152(t *testing.T) {
	for _, root := range nodeWriteAdviceRoots1152 {
		t.Run(root, func(t *testing.T) {
			run := func(query, body string) (stderr string, err error) {
				srv := fakeGraphQL(t, map[string]string{"M1152": body})
				f, out := testFactory(t)
				errOut := &strings.Builder{}
				f.IOStreams.ErrOut = errOut
				stub := &cobra.Command{Use: "stub1152", RunE: func(cmd *cobra.Command, _ []string) error {
					client, err := f.GraphQLClient()
					if err != nil {
						return err
					}
					var data map[string]any
					return client.MakeRequest(cmd.Context(), &graphql.Request{Query: query, OpName: "M1152"}, &graphql.Response{Data: &data})
				}}
				cmdRoot := NewRootCmd(f)
				cmdRoot.AddCommand(stub)
				installNodeWriteWarnings(stub, f)
				cmdRoot.SetArgs([]string{"stub1152", "--server", srv.URL})
				err = cmdRoot.Execute()
				if out.Len() != 0 {
					t.Fatalf("stdout changed: %q", out.String())
				}
				return errOut.String(), err
			}
			warnings := `[{"path":"` + root + `","code":"include.pin.unkept","text":` + quote1142(pinWarningText) + `},{"path":"wrongRoot1152","code":"wrong.root","text":"must stay silent"}]`
			stderr, err := run(`mutation M1152 { `+root+` { id } }`, `{"data":{"`+root+`":{"id":"n"}},"extensions":{"warnings":`+warnings+`}}`)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(stderr, "warning: [include.pin.unkept] "+pinWarningText) != 1 || strings.Contains(stderr, "wrong.root") {
				t.Fatalf("%s: advice for its own root must show once, a foreign root's never: %q", root, stderr)
			}
			// Response-key ownership: an alias is keyed by the alias, not the field.
			stderr, err = run(`mutation M1152 { saved: `+root+` { id } }`, `{"data":{"saved":{"id":"n"}},"extensions":{"warnings":[{"path":"saved","code":"include.pin.unkept","text":"aliased advice"},{"path":"`+root+`","code":"wrong.root","text":"keyed by the field, not the alias"}]}}`)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stderr, "aliased advice") || strings.Contains(stderr, "wrong.root") {
				t.Fatalf("%s: alias ownership: %q", root, stderr)
			}
			// A failed request prints no advice (the failed-command contract).
			stderr, err = run(`mutation M1152 { `+root+` { id } }`, `{"errors":[{"message":"refused"}],"extensions":{"warnings":`+warnings+`}}`)
			if err == nil || strings.Contains(stderr, "warning:") {
				t.Fatalf("%s: failed request: err=%v stderr=%q", root, err, stderr)
			}
		})
	}
}

// Roots whose names mention a node but which carry no plain-pin advice, or which
// have not been evaluated. A node-named mutation that is in neither this table nor
// nodeWriteAdviceRoots1152 fails the inventory test below.
var nodeMutationsWithoutAdvice1152 = map[string]string{
	// Verified against hadron-server (writeNodeCore, resolvers.mutation.node.ts and
	// createAssetReferenceNode.ts at origin/main when #1152 was built):
	"createAssetReferenceNode": "writes a reference node through the shared node write, but its content is server-authored (file name, type, size, asset URN) and holds no include, so no plain-pin advice is expected",
	"updateNodeRevision":       "edits a revision's label only; no content is written",
	// They do not write node content:
	"approveMemoryNodes":                "records approvals; no content write",
	"approveNode":                       "records an approval; no content write",
	"clearNodeHistory":                  "deletes history; no content write",
	"deleteNode":                        "deletes a node; no content write",
	"deleteNodeRevision":                "deletes a revision; no content write",
	"planNodeMove":                      "a read-only plan; no write",
	"recordNodeContentValidationReport": "records a validation report; no content write",
	"requestNodeSignature":              "requests a signature; no content write",
	"createNodeRoleRule":                "writes a memory's role rule, not node content",
	"updateNodeRoleRule":                "writes a memory's role rule, not node content",
	"deleteNodeRoleRule":                "deletes a memory's role rule, not node content",
	"unlockNodeRoleRule":                "unlocks a role rule; no content write",
	// Not evaluated in #1152 (its scope is the set above): whether the provider
	// carries plain-pin advice for these three is its own check.
	"extractParentNodeToMemory": "not evaluated for advice",
	"mintMemoryNodes":           "not evaluated for advice",
	"updateNodeProperties":      "not evaluated for advice (properties, not content)",
}

func TestNodeMutationInventoryIsClassified1152(t *testing.T) {
	files, err := filepath.Glob("../api/queries/*.graphql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no typed queries found: %v", err)
	}
	roots := map[string]bool{}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := parser.ParseQuery(&ast.Source{Name: file, Input: string(b)})
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, op := range doc.Operations {
			if op.Operation != ast.Mutation {
				continue
			}
			for _, sel := range op.SelectionSet {
				if f, ok := sel.(*ast.Field); ok && strings.Contains(strings.ToLower(f.Name), "node") {
					roots[f.Name] = true
				}
			}
		}
	}
	advised := map[string]bool{}
	for _, r := range nodeWriteAdviceRoots1152 {
		advised[r] = true
	}
	var unclassified, stale []string
	for r := range roots {
		if !advised[r] && nodeMutationsWithoutAdvice1152[r] == "" {
			unclassified = append(unclassified, r)
		}
	}
	for r := range advised {
		if !roots[r] {
			stale = append(stale, r)
		}
	}
	for r := range nodeMutationsWithoutAdvice1152 {
		if !roots[r] {
			stale = append(stale, r)
		}
		if advised[r] {
			t.Errorf("%s is both advised and excluded", r)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)
	if len(unclassified) > 0 {
		t.Errorf("node-named mutations with no advice classification (add to installNodeWriteWarnings and nodeWriteAdviceRoots1152, or to nodeMutationsWithoutAdvice1152 with a reason): %v", unclassified)
	}
	if len(stale) > 0 {
		t.Errorf("classified names no longer in the typed queries: %v", stale)
	}
}
