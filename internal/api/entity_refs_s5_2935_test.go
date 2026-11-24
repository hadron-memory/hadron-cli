package api

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// cli#2935, the entity-refs S5 companion (hadron-server#4141: #4789 and
// #4810, a hard rename with no alias, cor:api:140:01). The CLI's documents
// must send the renamed arguments:
//
//	cancelAppRun(id:)            -> cancelAppRun(ref:)          (#4789)
//	startAgentSession(agentId:)  -> startAgentSession(agentRef:) (#4789)
//	appRuns(parentRunId:)        -> appRuns(parentRunRef:)       (#4810)
//
// That every document validates against the snapshot (so a document still
// naming a retired argument fails) is TestDocumentedEntityRefArgsStillInSchema2696's
// sweep; these pins add what it cannot say: which name each field sends, and
// that the snapshot carries only the new ones.

var s5Renames = []struct {
	op            ast.Operation // the root type the field hangs off
	field         string
	want, retired string
}{
	{ast.Mutation, "cancelAppRun", "ref", "id"},
	{ast.Mutation, "startAgentSession", "agentRef", "agentId"},
	{ast.Query, "appRuns", "parentRunRef", "parentRunId"},
}

// fieldUse is one root selection of a field: where it is and the argument
// names it sends.
type fieldUse struct {
	where string
	names []string
}

// rootFieldArgs returns every root selection of field in every operation of
// type op across the query documents, one entry per occurrence (an operation
// that selects the field twice, for example under two aliases, yields two).
// Root inline fragments and fragment spreads are followed, so a selection
// moved into a fragment on the root type is still seen. Only root selections
// count: the three fields are root fields, so a same-named field nested under
// another type cannot match.
func rootFieldArgs(t *testing.T, op ast.Operation, field string) []fieldUse {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("queries", "*.graphql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query documents found: %v", err)
	}
	docs := map[string]*ast.QueryDocument{}
	fragments := map[string]*ast.FragmentDefinition{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		doc, perr := parser.ParseQuery(&ast.Source{Name: f, Input: string(b)})
		if perr != nil {
			t.Fatalf("%s does not parse: %v", f, perr)
		}
		docs[f] = doc
		for _, fr := range doc.Fragments {
			fragments[fr.Name] = fr
		}
	}
	var found []fieldUse
	var walk func(where string, set ast.SelectionSet, seen map[string]bool)
	walk = func(where string, set ast.SelectionSet, seen map[string]bool) {
		for _, sel := range set {
			switch s := sel.(type) {
			case *ast.Field:
				if s.Name == field {
					names := []string{}
					for _, a := range s.Arguments {
						names = append(names, a.Name)
					}
					found = append(found, fieldUse{where, names})
				}
			case *ast.InlineFragment:
				walk(where, s.SelectionSet, seen)
			case *ast.FragmentSpread:
				fr, ok := fragments[s.Name]
				if !ok {
					t.Fatalf("%s spreads unknown fragment %s", where, s.Name)
				}
				if seen[s.Name] {
					continue
				}
				seen[s.Name] = true
				walk(where+"..."+s.Name, fr.SelectionSet, seen)
			}
		}
	}
	for _, f := range files {
		for _, o := range docs[f].Operations {
			if o.Operation != op {
				continue
			}
			walk(f+":"+o.Name, o.SelectionSet, map[string]bool{})
		}
	}
	return found
}

func TestS5DocumentsSendTheRefArgumentsNotTheRetiredOnes_2935(t *testing.T) {
	for _, tc := range s5Renames {
		uses := rootFieldArgs(t, tc.op, tc.field)
		if len(uses) == 0 {
			t.Errorf("no %s operation selects %s: the pin cannot see it", tc.op, tc.field)
			continue
		}
		for _, u := range uses {
			if !slices.Contains(u.names, tc.want) {
				t.Errorf("%s: %s must send %s (hadron-server#4141 S5), sends %v", u.where, tc.field, tc.want, u.names)
			}
			if slices.Contains(u.names, tc.retired) {
				t.Errorf("%s: %s still sends the retired %s, sends %v", u.where, tc.field, tc.retired, u.names)
			}
		}
	}
}

// The snapshot carries the renamed arguments and not the retired ones, so the
// 2696 sweep fails a document that still names a retired one.
func TestS5SnapshotCarriesOnlyTheRefArguments_2935(t *testing.T) {
	sdl, err := os.ReadFile(filepath.Join("..", "..", "schema", "schema.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	schema, gerr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if gerr != nil {
		t.Fatalf("the snapshot does not load: %v", gerr)
	}
	for _, tc := range s5Renames {
		root := schema.Query
		if tc.op == ast.Mutation {
			root = schema.Mutation
		}
		if root == nil {
			t.Fatalf("the snapshot has no %s type", tc.op)
		}
		f := root.Fields.ForName(tc.field)
		if f == nil {
			t.Errorf("the snapshot has no %s.%s", root.Name, tc.field)
			continue
		}
		if f.Arguments.ForName(tc.want) == nil {
			t.Errorf("the snapshot's %s lacks %s", tc.field, tc.want)
		}
		if f.Arguments.ForName(tc.retired) != nil {
			t.Errorf("the snapshot's %s still has the retired %s", tc.field, tc.retired)
		}
	}
}
