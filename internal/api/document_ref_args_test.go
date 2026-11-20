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
	"github.com/vektah/gqlparser/v2/parser"
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
//   - The check is existence-and-not-deprecated, not typing: memoryRef is
//     String (server-resolved) on addMemoryMember and ID elsewhere, so an
//     ID-only rule would false-positive. The exact typing the CLI sends is
//     pinned per operation by the table test in internal/cmd.
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
	type file struct {
		path     string
		src      string
		lines    int
		doc      *ast.QueryDocument
		ownFrags map[string]bool
	}
	var files []file
	pool := map[string]string{} // fragment name -> printed definition
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
		doc, err := parser.ParseQuery(&ast.Source{Name: path, Input: string(src)})
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		f := file{path: path, src: string(src), lines: strings.Count(string(src), "\n") + 1, doc: doc, ownFrags: map[string]bool{}}
		for _, frag := range doc.Fragments {
			f.ownFrags[frag.Name] = true
			var buf bytes.Buffer
			fd := &ast.QueryDocument{Fragments: []*ast.FragmentDefinition{frag}}
			formatter.NewFormatter(&buf).FormatQueryDocument(fd)
			if prev, dup := pool[frag.Name]; dup && prev != buf.String() {
				return fmt.Errorf("fragment %s is defined differently in more than one file", frag.Name)
			}
			pool[frag.Name] = buf.String()
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		t.Fatalf("read queries: %v", err)
	}

	for _, f := range files {
		combined := f.src + pooledFragments(f.doc, pool, f.ownFrags)
		for _, problem := range documentRefArgProblems(schema, f.path, combined, f.lines, f.ownFrags) {
			t.Error(problem)
		}
	}
}

// pooledFragments prints the foreign fragment definitions the document
// transitively spreads, for appending after the file's own text.
func pooledFragments(doc *ast.QueryDocument, pool map[string]string, own map[string]bool) string {
	needed := map[string]bool{}
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
						if err == nil {
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
	return b.String()
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
	doc, gqlErrs := gqlparser.LoadQuery(schema, src)
	if gqlErrs != nil {
		var problems []string
		for _, e := range gqlErrs {
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
		return problems
	}
	var problems []string
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
