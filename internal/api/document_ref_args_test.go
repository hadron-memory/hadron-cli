package api

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
)

// The S4 entity-ref renames (server#4219) renamed the identifying ARGUMENT of a
// family of operations — orgId/memoryId/appId/agentId/userId became
// orgRef/memoryRef/appRef/agentRef/userRef, the former optional *Id/*Ref pairs
// collapsed into one required arg, and four mutations (deleteMemory,
// syncMemory, setMemorySourceToken, deleteOrganization) renamed id -> ref
// outright. genqlient refuses a document that names a dropped argument, but
// only at `make generate`, against a schema the committed generated code may
// predate — and it says nothing if the server re-adds an old spelling as a
// deprecated alias (several *MemoryMember operations carry exactly such
// aliases today). This test loads every committed operation document against
// the committed schema snapshot (the same SDL genqlient checks against) and
// fails, in plain `go test`, when a document no longer validates against the
// snapshot — the signature of ANY rename a document missed — or when a field
// call passes an argument the snapshot declares only as a deprecated alias.
// That makes "every renamed operation is pinned" (entity_refs_s4_2011_test.go)
// self-checking document-wide instead of a hand-claimed table: the next rename
// that lands in the snapshot without its documents turns this red by name,
// file and line (cli#2679 review).
//
// Documents share fragments ACROSS files (genqlient concatenates them), so a
// file cannot validate in isolation: each file is validated with the foreign
// fragments it transitively spreads appended after its own text. Validation
// errors below the file's own line count are attributed to the pooled
// fragments rather than mis-printed as a file line, and the deprecated-alias
// walk covers only the file's own operations and fragments — a foreign
// fragment is walked exactly once, in its own file.
//
// Resolution is exact, not name-based: gqlparser fills each field's
// Definition from its ACTUAL parent type, so an argument is checked against
// the field that was called — never against a same-named field on another
// type, and never depending on map iteration order (#2696 review: a
// by-field-name union across schema.Types is nondeterministic when two types
// disagree). The self-test at the bottom proves the machinery fails on a
// synthetic stale document and a synthetic deprecated-alias document, so green
// here means "checked", not "empty".
//
// Scope notes, on purpose:
//   - Validation covers dropped arguments on EVERY field, not only the
//     renamed stems — a committed document that does not validate against the
//     committed snapshot is red whatever the cause. The deprecated-alias walk
//     IS stem-filtered, to the nine rename stems below plus id/ref: the
//     corpus has living deprecated usages outside the renames
//     (encryptMemory's passphrase moved to a header, appRuns' status became
//     statuses), which are the server's own deprecation policy, not a rename
//     this sweep polices.
//   - The deprecated-alias walk is existence-and-not-deprecated, not typing:
//     memoryRef is String (server-resolved) on addMemoryMember and ID
//     elsewhere, so an ID-only rule would false-positive. Validation itself
//     is not that loose: gqlparser.LoadQuery runs the full rule set, including
//     variables-in-allowed-position, so a variable whose declared type does
//     not fit the argument it feeds fails (pinned by the synthetic type case
//     below). The exact typing the CLI sends is pinned per operation by the
//     table test in internal/cmd.
//   - updateOrgGithubApp's appId is GitHub's app id, not the renamed appRef;
//     it is declared and undeprecated in the snapshot, so it passes here and
//     its story lives in the table test's comment.
func TestDocumentedEntityRefArgsStillInSchema2696(t *testing.T) {
	sdl, err := os.ReadFile(filepath.Join("..", "..", "schema", "schema.graphql"))
	if err != nil {
		t.Fatalf("read schema snapshot: %v", err)
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if err != nil {
		t.Fatalf("parse schema snapshot: %v", err)
	}

	// Walk, not ReadDir: a subdirectory of queries/ appearing one day must be
	// swept too, not silently skipped (#2696 review).
	var docs []sweepSource
	err = filepath.WalkDir("queries", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".graphql" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		docs = append(docs, sweepSource{path: path, text: string(src)})
		return nil
	})
	if err != nil {
		t.Fatalf("read queries: %v", err)
	}
	problems, err := sweepDocuments(schema, docs)
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range problems {
		t.Error(problem)
	}
}

// sweepSource is one operation document: the path it is reported under and its
// text. The sweep takes these in memory so synthetic documents can drive the
// exact path the real corpus takes (cli#2712).
type sweepSource struct{ path, text string }

// sweepDocuments parses every document, pools the fragments they define, and
// validates each document with the foreign fragments it spreads appended.
func sweepDocuments(schema *ast.Schema, docs []sweepSource) ([]string, error) {
	type file struct {
		path     string
		src      string
		lines    int
		doc      *ast.QueryDocument
		ownFrags map[string]bool
	}
	var files []file
	pool := map[string]string{} // fragment name -> printed definition
	for _, d := range docs {
		doc, err := parser.ParseQuery(&ast.Source{Name: d.path, Input: d.text})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.path, err)
		}
		f := file{path: d.path, src: d.text, lines: strings.Count(d.text, "\n") + 1, doc: doc, ownFrags: map[string]bool{}}
		for _, frag := range doc.Fragments {
			f.ownFrags[frag.Name] = true
			var buf bytes.Buffer
			fd := &ast.QueryDocument{Fragments: []*ast.FragmentDefinition{frag}}
			formatter.NewFormatter(&buf).FormatQueryDocument(fd)
			if prev, dup := pool[frag.Name]; dup && prev != buf.String() {
				return nil, fmt.Errorf("fragment %s is defined differently in more than one file", frag.Name)
			}
			pool[frag.Name] = buf.String()
		}
		files = append(files, f)
	}
	var problems []string
	for _, f := range files {
		pooled, err := pooledFragments(f.doc, pool, f.ownFrags)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", f.path, err))
			continue
		}
		problems = append(problems, documentRefArgProblems(schema, f.path, f.src+pooled, f.lines, f.ownFrags)...)
	}
	return problems, nil
}

// pooledFragments prints the foreign fragment definitions the document
// transitively spreads, for appending after the file's own text. A pooled
// definition that does not parse is an error, not a silent gap: dropping it
// would under-pool the document (cli#2712).
func pooledFragments(doc *ast.QueryDocument, pool map[string]string, own map[string]bool) (string, error) {
	needed := map[string]bool{}
	var parseErr error
	var addSet func(set ast.SelectionSet)
	addSet = func(set ast.SelectionSet) {
		for _, sel := range set {
			switch s := sel.(type) {
			case *ast.Field:
				addSet(s.SelectionSet)
			case *ast.InlineFragment:
				addSet(s.SelectionSet)
			case *ast.FragmentSpread:
				if !own[s.Name] && !needed[s.Name] {
					needed[s.Name] = true
					if frag, ok := pool[s.Name]; ok {
						// The pooled text may spread further foreign fragments.
						fdoc, err := parser.ParseQuery(&ast.Source{Name: s.Name, Input: frag})
						if err != nil {
							if parseErr == nil {
								parseErr = fmt.Errorf("pooled fragment %s does not parse: %w", s.Name, err)
							}
						} else {
							for _, fd := range fdoc.Fragments {
								addSet(fd.SelectionSet)
							}
						}
					}
				}
			}
		}
	}
	for _, op := range doc.Operations {
		addSet(op.SelectionSet)
	}
	for _, frag := range doc.Fragments {
		addSet(frag.SelectionSet)
	}
	names := make([]string, 0, len(needed))
	for n := range needed {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString("\n")
		b.WriteString(pool[n])
	}
	return b.String(), parseErr
}

// refArgStems are the argument names the S4 renames turned over: the five
// *Id/*Ref pairs and the outright id -> ref of deleteMemory/syncMemory/
// setMemorySourceToken/deleteOrganization. The deprecated-alias walk fires
// only on these; validation is document-wide.
var refArgStems = map[string]bool{
	"orgId": true, "memoryId": true, "appId": true, "agentId": true, "userId": true,
	"orgRef": true, "memoryRef": true, "appRef": true, "agentRef": true, "userRef": true,
	"id": true, "ref": true,
}

// documentRefArgProblems loads one operation document (its file's text plus
// pooled fragments) against the schema and returns every way it has drifted:
// a validation failure (any argument — or field — the snapshot no longer
// declares, reported by gqlparser), or a field call passing an argument that
// exists only as a deprecated alias. ownLines/ownFrags keep the reporting
// honest: positions past the file's own text belong to pooled fragments, and
// the deprecated-alias walk skips them (their own file walks them).
func documentRefArgProblems(schema *ast.Schema, name, src string, ownLines int, ownFrags map[string]bool) []string {
	// Parse and validate by hand rather than gqlparser.LoadQuery: LoadQuery
	// returns a nil document whenever validation reports anything, and the
	// alias walk below needs the validated document (the walker fills each
	// field's Definition) even when the only reports are filtered out.
	doc, parseErr := parser.ParseQuery(&ast.Source{Input: src})
	var gqlErrs gqlerror.List
	if parseErr != nil {
		gqlErrs = gqlerror.List{gqlerror.Wrap(parseErr)}
		if e, ok := parseErr.(*gqlerror.Error); ok {
			gqlErrs = gqlerror.List{e}
		}
		doc = nil
	} else {
		gqlErrs = validator.Validate(schema, doc)
	}
	var problems []string
	if len(gqlErrs) > 0 {
		for _, e := range gqlErrs {
			// A fragment this file DEFINES but only another file spreads is
			// "never used" to this file alone; its own file is not at fault
			// and the corpus-wide check is that every document validates
			// with what it spreads (cli#2712).
			if frag, ok := unusedFragmentName(e.Message); ok && ownFrags[frag] {
				continue
			}
			where := name
			if len(e.Locations) > 0 {
				if e.Locations[0].Line <= ownLines {
					where = fmt.Sprintf("%s:%d", name, e.Locations[0].Line)
				} else {
					where = fmt.Sprintf("%s (a pooled fragment named in the message)", name)
				}
			}
			problems = append(problems, fmt.Sprintf("%s: does not validate against the snapshot — %s", where, e.Message))
		}
		if len(problems) > 0 || doc == nil {
			return problems
		}
	}
	var visit func(set ast.SelectionSet)
	visit = func(set ast.SelectionSet) {
		for _, sel := range set {
			switch s := sel.(type) {
			case *ast.Field:
				for _, arg := range s.Arguments {
					if !refArgStems[arg.Name] {
						continue
					}
					var def *ast.ArgumentDefinition
					for _, a := range s.Definition.Arguments {
						if a.Name == arg.Name {
							def = a
							break
						}
					}
					if def == nil {
						continue // impossible after validation; re-checked there
					}
					for _, d := range def.Directives {
						if d.Name != "deprecated" {
							continue
						}
						reason := ""
						if len(d.Arguments) > 0 && d.Arguments[0].Value != nil {
							reason = strings.Trim(d.Arguments[0].Value.String(), `"`)
						}
						line := 0
						if arg.Position != nil {
							line = arg.Position.Line
						}
						problems = append(problems, fmt.Sprintf(
							"%s:%d: %s(%s: …): declared only as a deprecated alias (%s)",
							name, line, s.Name, arg.Name, reason))
					}
				}
				visit(s.SelectionSet)
			case *ast.InlineFragment:
				visit(s.SelectionSet)
			}
		}
	}
	for _, op := range doc.Operations {
		visit(op.SelectionSet)
	}
	for _, frag := range doc.Fragments {
		if ownFrags[frag.Name] {
			visit(frag.SelectionSet)
		}
	}
	return problems
}

// The self-test: the sweep must actually FAIL on a document that names a
// dropped argument, and on one that leans on a deprecated alias — otherwise
// green above proves only that the walker ran (#2696 review). Synthetic SDL,
// not a mutation of the real snapshot: the real one is the contract under
// test and must not be doctored in place.
func TestDocumentRefArgSweepCatchesStaleDocuments2696(t *testing.T) {
	sdl := `
		type Query { node(id: ID!): Int }
		type Mutation {
			dropMemory(ref: ID!): Boolean
			syncMemory(id: ID @deprecated(reason: "use ref"), ref: ID): Boolean
		}`
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "synthetic.graphql", Input: sdl})
	if err != nil {
		t.Fatalf("parse synthetic schema: %v", err)
	}
	for _, tc := range []struct {
		name, doc, want string
	}{
		{
			"dropped argument",
			`mutation M($id: ID!) { dropMemory(id: $id) }`,
			`dropMemory`,
		},
		{
			"deprecated alias",
			`mutation M($id: ID) { syncMemory(id: $id) }`,
			`declared only as a deprecated alias`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := documentRefArgProblems(schema, "synthetic.graphql", tc.doc,
				strings.Count(tc.doc, "\n")+1, map[string]bool{})
			if len(problems) == 0 {
				t.Fatalf("sweep found nothing — it cannot catch the real thing; doc: %s", tc.doc)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("sweep reported the wrong thing:\n%s", strings.Join(problems, "\n"))
			}
		})
	}
	// And the all-clear shape: a document on the renamed spellings stays green.
	if problems := documentRefArgProblems(schema, "synthetic.graphql",
		`mutation M($ref: ID!) { dropMemory(ref: $ref) syncMemory(ref: $ref) }`,
		1, map[string]bool{}); len(problems) != 0 {
		t.Errorf("sweep flagged a clean document:\n%s", strings.Join(problems, "\n"))
	}
}

// unusedFragmentName extracts X from gqlparser's `Fragment "X" is never used.`.
func unusedFragmentName(msg string) (string, bool) {
	const prefix, suffix = `Fragment "`, `" is never used.`
	if strings.HasPrefix(msg, prefix) && strings.HasSuffix(msg, suffix) {
		return strings.TrimSuffix(strings.TrimPrefix(msg, prefix), suffix), true
	}
	return "", false
}

// syntheticSchema2712 gives the sweep's synthetic cases two types whose
// same-named field takes different arguments, so exact resolution is observable.
func syntheticSchema2712(t *testing.T) *ast.Schema {
	t.Helper()
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "synthetic.graphql", Input: `
		type Query {
			node(id: ID!): Thing
			other(id: ID!): Other
		}
		type Thing { name: String items(first: Int): [Int] legacy(id: ID @deprecated(reason: "use ref"), ref: ID): Int }
		type Other { name: String items(limit: Int): [Int] }
		type Mutation { dropMemory(ref: ID!): Boolean }`})
	if err != nil {
		t.Fatalf("parse synthetic schema: %v", err)
	}
	return schema
}

// Full validation includes variables-in-allowed-position: a variable declared
// String cannot feed an ID! argument, so the sweep is typed, not only an
// existence check (the scope note above says so).
func TestDocumentRefArgSweepCatchesTypeMismatch2712(t *testing.T) {
	schema := syntheticSchema2712(t)
	doc := `mutation M($id: String) { dropMemory(ref: $id) }`
	problems := documentRefArgProblems(schema, "synthetic.graphql", doc, 1, map[string]bool{})
	if !strings.Contains(strings.Join(problems, "\n"), "used in position expecting") {
		t.Fatalf("a String variable feeding an ID! argument was not flagged:\n%s", strings.Join(problems, "\n"))
	}
}

// A fragment a file defines but only ANOTHER file spreads is "never used" to
// that file alone: not a problem of its own, while a real stale argument in
// the same file is still reported.
func TestDocumentRefArgSweepIgnoresOwnUnspreadFragment2712(t *testing.T) {
	schema := syntheticSchema2712(t)
	own := map[string]bool{"F": true}
	if problems := documentRefArgProblems(schema, "a.graphql", `fragment F on Thing { name }`, 1, own); len(problems) != 0 {
		t.Errorf("an own fragment spread only elsewhere was reported:\n%s", strings.Join(problems, "\n"))
	}
	problems := documentRefArgProblems(schema, "a.graphql", "fragment F on Thing { name }\nmutation M($id: ID!) { dropMemory(id: $id) }", 2, own)
	if !strings.Contains(strings.Join(problems, "\n"), "dropMemory") {
		t.Errorf("the unspread-fragment filter swallowed a real stale argument:\n%s", strings.Join(problems, "\n"))
	}
	// A pooled (not own) fragment that nothing spreads stays reported.
	if problems := documentRefArgProblems(schema, "b.graphql", `fragment G on Thing { name }`, 1, map[string]bool{}); len(problems) == 0 {
		t.Error("a never-used fragment the file does not define was filtered")
	}
}

// The pooled-fragment path end to end, with synthetic files: a fragment defined
// in one file and spread in another validates; a stale argument inside the
// foreign fragment fails; and a pooled definition that does not parse is an
// error, not a silent gap.
func TestDocumentRefArgSweepPooledFragments2712(t *testing.T) {
	schema := syntheticSchema2712(t)
	spreader := sweepSource{path: "spread.graphql", text: `query Q { node(id: 1) { ...F } }`}
	good := sweepSource{path: "frag.graphql", text: `fragment F on Thing { name items(first: 1) }`}
	if problems, err := sweepDocuments(schema, []sweepSource{good, spreader}); err != nil || len(problems) != 0 {
		t.Fatalf("a valid foreign fragment was flagged: %v\n%s", err, strings.Join(problems, "\n"))
	}
	stale := sweepSource{path: "frag.graphql", text: `fragment F on Thing { name items(after: 1) }`}
	problems, err := sweepDocuments(schema, []sweepSource{stale, spreader})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "items") {
		t.Errorf("a stale argument inside a foreign fragment was not reported:\n%s", joined)
	}
	if !strings.Contains(joined, "spread.graphql") || !strings.Contains(joined, "frag.graphql") {
		t.Errorf("the stale fragment should be reported against the spreading document and its own file:\n%s", joined)
	}

	doc, perr := parser.ParseQuery(&ast.Source{Name: "q", Input: `query Q { node(id: 1) { ...G } }`})
	if perr != nil {
		t.Fatal(perr)
	}
	if _, err := pooledFragments(doc, map[string]string{"G": "fragment G on Thing {"}, map[string]bool{}); err == nil {
		t.Error("an unparseable pooled fragment was dropped silently")
	}
}

// Exact resolution: two types share a field name with different arguments;
// each is checked against the type actually called.
func TestDocumentRefArgSweepResolvesSameNamedFieldsPerType2712(t *testing.T) {
	schema := syntheticSchema2712(t)
	clean := `query Q { node(id: 1) { items(first: 1) } other(id: 1) { items(limit: 1) } }`
	if problems := documentRefArgProblems(schema, "s.graphql", clean, 1, map[string]bool{}); len(problems) != 0 {
		t.Errorf("per-type arguments were flagged:\n%s", strings.Join(problems, "\n"))
	}
	wrong := `query Q { other(id: 1) { items(first: 1) } }`
	if problems := documentRefArgProblems(schema, "s.graphql", wrong, 1, map[string]bool{}); len(problems) == 0 {
		t.Error("Thing.items's argument on Other.items was accepted: resolution is by name, not by type")
	}
}

// The unspread-fragment filter must not blind the deprecated-alias walk: a
// fragment one file defines and another spreads still gets its own walk, so a
// deprecated alias inside it is reported (Jane's P2 on #2725: the filter left
// the validated document nil and the walk was skipped).
func TestDocumentRefArgSweepWalksOwnUnspreadFragment2712(t *testing.T) {
	schema := syntheticSchema2712(t)
	problems, err := sweepDocuments(schema, []sweepSource{
		{path: "frag.graphql", text: `fragment F on Thing { legacy(id: 1) }`},
		{path: "spread.graphql", text: `query Q { node(id: 1) { ...F } }`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(problems, "\n"), "declared only as a deprecated alias") {
		t.Errorf("a deprecated alias inside an own fragment spread elsewhere escaped the sweep:\n%s", strings.Join(problems, "\n"))
	}
	// And the clean twin stays clean.
	if problems, err := sweepDocuments(schema, []sweepSource{
		{path: "frag.graphql", text: `fragment F on Thing { legacy(ref: 1) }`},
		{path: "spread.graphql", text: `query Q { node(id: 1) { ...F } }`},
	}); err != nil || len(problems) != 0 {
		t.Errorf("a clean cross-file fragment was flagged: %v\n%s", err, strings.Join(problems, "\n"))
	}
}
