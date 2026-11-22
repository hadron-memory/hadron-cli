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

// rootFieldArgs returns, for every operation of type op in every query
// document, the argument names its root selection of field sends, keyed by
// "<file>:<operation>". Only root selections count: the three fields are
// root fields, so a same-named field nested under another type cannot match.
func rootFieldArgs(t *testing.T, op ast.Operation, field string) map[string][]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("queries", "*.graphql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query documents found: %v", err)
	}
	found := map[string][]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		doc, perr := parser.ParseQuery(&ast.Source{Name: f, Input: string(b)})
		if perr != nil {
			t.Fatalf("%s does not parse: %v", f, perr)
		}
		for _, o := range doc.Operations {
			if o.Operation != op {
				continue
			}
			for _, sel := range o.SelectionSet {
				if fl, ok := sel.(*ast.Field); ok && fl.Name == field {
					names := []string{}
					for _, a := range fl.Arguments {
						names = append(names, a.Name)
					}
					found[f+":"+o.Name] = names
				}
			}
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
		for where, names := range uses {
			if !slices.Contains(names, tc.want) {
				t.Errorf("%s: %s must send %s (hadron-server#4141 S5), sends %v", where, tc.field, tc.want, names)
			}
			if slices.Contains(names, tc.retired) {
				t.Errorf("%s: %s still sends the retired %s, sends %v", where, tc.field, tc.retired, names)
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
