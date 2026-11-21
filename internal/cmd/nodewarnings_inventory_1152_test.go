package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
	"createNode", "createSpecNode", "createTaskNode", "createReviewNode", "createAssetReferenceNode",
	"updateNode", "updateSpecNode", "updateTaskNode", "updateReviewNode",
	"importNode", "mergeNodes", "cloneNode", "moveNode", "updateNodeData", "restoreNode", "restoreNodeRevision",
	"searchReplaceInNodes", "searchReplaceInSpecNodes",
}

// stubRun1152 runs a stub command through the real installNodeWriteWarnings path.
// It sends each (opName, query) in order against a fake server that answers
// responses[opName], and returns stderr, stdout and the command's error.
func stubRun1152(t *testing.T, responses map[string]string, requests [][2]string) (stderr, stdout string, err error) {
	t.Helper()
	srv := fakeGraphQL(t, responses)
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	stub := &cobra.Command{Use: "stub1152", RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		for _, r := range requests {
			var data map[string]any
			if err := client.MakeRequest(cmd.Context(), &graphql.Request{Query: r[1], OpName: r[0]}, &graphql.Response{Data: &data}); err != nil {
				return err
			}
		}
		return nil
	}}
	cmdRoot := NewRootCmd(f)
	cmdRoot.AddCommand(stub)
	installNodeWriteWarnings(stub, f)
	cmdRoot.SetArgs([]string{"stub1152", "--server", srv.URL})
	err = cmdRoot.Execute()
	return errOut.String(), out.String(), err
}

// Each root is exercised through the real installNodeWriteWarnings path: the stub
// sends `mutation { <root> { id } }` and the fake server answers with advice for
// that root, advice for a foreign root, and (for the failure control) an error.
// Stdout stays untouched; advice is stderr only.
func TestNodeWriteAdviceRootsAreEachPinned1152(t *testing.T) {
	for _, root := range nodeWriteAdviceRoots1152 {
		t.Run(root, func(t *testing.T) {
			run := func(query, body string) (string, error) {
				stderr, stdout, err := stubRun1152(t, map[string]string{"M1152": body}, [][2]string{{"M1152", query}})
				if stdout != "" {
					t.Fatalf("stdout changed: %q", stdout)
				}
				return stderr, err
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
			stderr, err = run(`mutation M1152 { saved: `+root+` { id } }`, `{"data":{"saved":{"id":"n"}},"extensions":{"warnings":[{"path":"saved","code":"include.pin.unkept","text":`+quote1142("aliased advice")+`},{"path":"`+root+`","code":"wrong.root","text":"keyed by the field, not the alias"}]}}`)
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

// The other direction (George's P2): a root outside the set stays silent even when
// the server answers with advice under its own response key, so adding one to the
// production list, against the documented exact set, fails here.
func TestExcludedNodeRootsStaySilent1152(t *testing.T) {
	for root := range nodeMutationExclusions1152 {
		t.Run(root, func(t *testing.T) {
			body := `{"data":{"` + root + `":{"id":"n"}},"extensions":{"warnings":[{"path":"` + root + `","code":"include.pin.unkept","text":` + quote1142(pinWarningText) + `}]}}`
			stderr, stdout, err := stubRun1152(t, map[string]string{"M1152": body}, [][2]string{{"M1152", `mutation M1152 { ` + root + ` { id } }`}})
			if err != nil || stdout != "" || strings.Contains(stderr, "warning:") {
				t.Fatalf("%s is outside the advice set and must stay silent: err=%v stdout=%q stderr=%q", root, err, stdout, stderr)
			}
		})
	}
}

// The failed-command contract the comment in nodewarnings.go states: a later
// failed write suppresses the advice already collected for an earlier committed
// one (two requests: advice on the first, an error on the second).
func TestLaterFailedWriteSuppressesEarlierAdvice1152(t *testing.T) {
	first := `{"data":{"updateNode":{"id":"n"}},"extensions":{"warnings":[{"path":"updateNode","code":"include.pin.unkept","text":"first write advice"}]}}`
	second := `{"errors":[{"message":"refused"}]}`
	stderr, stdout, err := stubRun1152(t, map[string]string{"M1152A": first, "M1152B": second},
		[][2]string{{"M1152A", `mutation M1152A { updateNode { id } }`}, {"M1152B", `mutation M1152B { updateNode { id } }`}})
	if err == nil || stdout != "" || strings.Contains(stderr, "warning:") {
		t.Fatalf("a later failure must suppress the earlier write's advice: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	// Control: with no later failure the first write's advice shows.
	stderr, _, err = stubRun1152(t, map[string]string{"M1152A": first, "M1152B": `{"data":{"updateNode":{"id":"n"}}}`},
		[][2]string{{"M1152A", `mutation M1152A { updateNode { id } }`}, {"M1152B", `mutation M1152B { updateNode { id } }`}})
	if err != nil || !strings.Contains(stderr, "first write advice") {
		t.Fatalf("control: err=%v stderr=%q", err, stderr)
	}
}

// Roots whose names mention a node but which are not in the advice set. An entry
// is either verified (a reason) or marked UNVERIFIED (not evaluated). A node-named mutation that is in neither this table nor
// nodeWriteAdviceRoots1152 fails the inventory test below.
var nodeMutationExclusions1152 = map[string]string{
	// Verified against hadron-server c9d7820e resolvers.mutation.node.ts exportNodeToGoogleDoc:
	"exportNodeToGoogleDoc": "reads resolved node content for an external Doc; does not write a Hadron node or carry plain-pin write advice",
	// Verified against hadron-server (writeNodeCore, resolvers.mutation.node.ts and
	// createAssetReferenceNode.ts at origin/main when #1152 was built):
	"updateNodeRevision": "edits a revision's label only; no content is written",
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
	"extractParentNodeToMemory": "UNVERIFIED: not evaluated for advice",
	"mintMemoryNodes":           "UNVERIFIED: not evaluated for advice",
	"updateNodeProperties":      "UNVERIFIED: not evaluated for advice (properties, not content)",
	// cli#398: spec new's allocation creates an EMPTY unminted placeholder; the
	// authored content is written by its one guarded updateSpecNode fill, which
	// is in the advice set. Whether the provider attaches plain-pin advice to
	// the allocation call itself was not checked.
	"allocateSpecNode": "UNVERIFIED: reserves an empty placeholder (no content written); the content write is the updateSpecNode fill",
}

// nodeMutationRoots1152 returns the node-named root fields a document's mutations
// select, directly, through root-level inline fragments and through named
// fragment spreads (George's P2: a direct-field walk misses fragment-selected
// roots).
func nodeMutationRoots1152(doc *ast.QueryDocument) map[string]bool {
	roots := map[string]bool{}
	var walk func(set ast.SelectionSet, seen map[string]bool)
	walk = func(set ast.SelectionSet, seen map[string]bool) {
		for _, sel := range set {
			switch s := sel.(type) {
			case *ast.Field:
				if strings.Contains(strings.ToLower(s.Name), "node") {
					roots[s.Name] = true
				}
			case *ast.InlineFragment:
				walk(s.SelectionSet, seen)
			case *ast.FragmentSpread:
				if seen[s.Name] {
					continue
				}
				seen[s.Name] = true
				if def := doc.Fragments.ForName(s.Name); def != nil {
					walk(def.SelectionSet, seen)
				}
			}
		}
	}
	for _, op := range doc.Operations {
		if op.Operation == ast.Mutation {
			walk(op.SelectionSet, map[string]bool{})
		}
	}
	return roots
}

func TestNodeMutationInventorySeesFragmentSelectedRoots1152(t *testing.T) {
	doc, err := parser.ParseQuery(&ast.Source{Name: "fixture", Input: `
mutation A { ...Direct }
fragment Direct on Mutation { cloneNode(sourceRef: "x") { id } ...Nested }
fragment Nested on Mutation { moveNode(sourceRef: "x") { id } }
mutation B { ... on Mutation { mergeNodes(input: {}) { id } } }
mutation C { updateNodeData(a: 1) { id } plain: createNode(a: 1) { id } }
mutation D { notRelated { id } }
`})
	if err != nil {
		t.Fatal(err)
	}
	got := nodeMutationRoots1152(doc)
	for _, want := range []string{"cloneNode", "moveNode", "mergeNodes", "updateNodeData", "createNode"} {
		if !got[want] {
			t.Errorf("%s was not found through the document's selections: %v", want, got)
		}
	}
	if got["notRelated"] {
		t.Errorf("a root with no node in its name was collected: %v", got)
	}
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
		for r := range nodeMutationRoots1152(doc) {
			roots[r] = true
		}
	}
	advised := map[string]bool{}
	for _, r := range nodeWriteAdviceRoots1152 {
		advised[r] = true
	}
	var unclassified, stale []string
	for r := range roots {
		if !advised[r] && nodeMutationExclusions1152[r] == "" {
			unclassified = append(unclassified, r)
		}
	}
	for r := range advised {
		if !roots[r] {
			stale = append(stale, r)
		}
	}
	for r := range nodeMutationExclusions1152 {
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
		t.Errorf("node-named mutations with no advice classification (add to installNodeWriteWarnings and nodeWriteAdviceRoots1152, or to nodeMutationExclusions1152 with a reason): %v", unclassified)
	}
	if len(stale) > 0 {
		t.Errorf("classified names no longer in the typed queries: %v", stale)
	}
}

// Item 4: the agent guide names the exact eligible set, so a reader (or agent)
// can tell which writes display advice; and nodewarnings.go says why the two
// look-alike roots are not in it (item 2). The set is compared as a set, from the
// bounded list sentence, so moving an excluded root into the list (or dropping
// one) fails.
func TestNodeWriteAdviceSetIsDocumented1152(t *testing.T) {
	guide, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	adviceItem := func(text string) string {
		lines := strings.Split(text, "\n")
		for start, line := range lines {
			if !strings.HasPrefix(line, "- Successful node writes ") {
				continue
			}
			end := start + 1
			for end < len(lines) && (strings.TrimSpace(lines[end]) == "" || strings.HasPrefix(lines[end], "  ")) {
				end++
			}
			return strings.Join(strings.Fields(strings.Join(lines[start:end], "\n")), " ")
		}
		return ""
	}
	const open, mid, closeMark = "exactly those that send one of these mutations:", "(generic and kind add/update", "are outside the set"
	item := "- Successful node writes can carry server advice.\n  " + open + " `createNode` " + mid + "; `updateNodeRevision` " + closeMark + "."
	later := "\n- A later item repeats " + open + " " + mid + " " + closeMark + "."
	t.Run("bounded_item", func(t *testing.T) {
		want := strings.Join(strings.Fields(item), " ")
		if got := adviceItem("- An earlier item.\n" + item + later); got != want {
			t.Errorf("advice extraction borrowed text from a neighboring list item: %q", got)
		}
	})
	// The report supplied no failing fixture: rephrase each marker in the real
	// guide and repeat its original wording in a later synthetic list item.
	for _, marker := range []string{open, mid, closeMark} {
		t.Run("rephrased_"+marker, func(t *testing.T) {
			pattern := regexp.MustCompile(strings.ReplaceAll(regexp.QuoteMeta(marker), " ", `\s+`))
			rephrased := pattern.ReplaceAllString(string(guide), "rephrased marker")
			if strings.Contains(adviceItem(rephrased+later), marker) {
				t.Errorf("a later list item supplied the rephrased marker %q", marker)
			}
		})
	}
	flat := adviceItem(string(guide))
	i := strings.Index(flat, open)
	j := strings.Index(flat, mid)
	k := strings.Index(flat, closeMark)
	if i < 0 || j < i || k < j {
		t.Fatal("the agent guide's advice list sentence moved or was rephrased")
	}
	names := func(text string) map[string]bool {
		out := map[string]bool{}
		for _, m := range regexp.MustCompile("`(\\w+)`").FindAllStringSubmatch(text, -1) {
			out[m[1]] = true
		}
		return out
	}
	documented, outside := names(flat[i+len(open):j]), names(flat[j:k])
	want := map[string]bool{}
	for _, r := range nodeWriteAdviceRoots1152 {
		want[r] = true
	}
	if !reflect.DeepEqual(documented, want) {
		t.Errorf("the guide's eligible set differs from the pinned set:\n guide: %v\n pinned: %v", sortedKeys1152(documented), sortedKeys1152(want))
	}
	for _, root := range []string{"updateNodeRevision"} {
		if !outside[root] || documented[root] {
			t.Errorf("the guide must name %s as outside the set (and not in the list)", root)
		}
	}
	// The explanation lives in the comment above installNodeWriteWarnings.
	src, err := os.ReadFile("nodewarnings.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	a, b := strings.Index(text, "// The set is the mutation roots"), strings.Index(text, "func installNodeWriteWarnings(")
	if a < 0 || b < a {
		t.Fatal("the exclusion comment moved")
	}
	comment := strings.Join(strings.Fields(strings.ReplaceAll(text[a:b], "\n//", "\n")), " ")
	for _, want := range []string{"createAssetReferenceNode is in the set", "draws plain-pin advice", "updateNodeRevision is left out on purpose", "edits a revision's label only"} {
		if !strings.Contains(comment, want) {
			t.Errorf("the exclusion comment lacks %q", want)
		}
	}
}

func sortedKeys1152(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
