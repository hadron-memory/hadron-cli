package api

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// cli#2935, the entity-refs S5 companion (hadron-server#4141: #4789 and
// #4810, a hard rename with no alias, cor:api:140:01). The CLI's documents
// must send the renamed arguments:
//
//	cancelAppRun(id:)            -> cancelAppRun(ref:)          (#4789)
//	startAgentSession(agentId:)  -> startAgentSession(agentRef:) (#4789)
//	appRuns(parentRunId:)        -> appRuns(parentRunRef:)       (#4810)
//
// and every query document must validate against the committed snapshot, so a
// document and the snapshot cannot drift apart (an unknown argument is an
// operation-level validation failure on the live server).

// loadQueryDocuments parses every query document against the snapshot. The
// files are loaded as one source because fragments are shared across them.
func loadQueryDocuments(t *testing.T) (*ast.Schema, *ast.QueryDocument) {
	t.Helper()
	sdl, err := os.ReadFile(filepath.Join("..", "..", "schema", "schema.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	schema, gerr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if gerr != nil {
		t.Fatalf("the snapshot does not load: %v", gerr)
	}
	files, err := filepath.Glob(filepath.Join("queries", "*.graphql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query documents found: %v", err)
	}
	sort.Strings(files)
	var all strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
		all.WriteString("\n")
	}
	doc, errs := gqlparser.LoadQuery(schema, all.String())
	for _, e := range errs {
		t.Errorf("a query document does not validate against the snapshot: %s", describe(e))
	}
	if doc == nil {
		t.FailNow()
	}
	return schema, doc
}

func describe(e *gqlerror.Error) string {
	if len(e.Locations) > 0 {
		return e.Message + " (line " + strconv.Itoa(e.Locations[0].Line) + " of the joined documents)"
	}
	return e.Message
}

// argsOf returns the argument names every selection of field `name` sends,
// across all operations and fragments, one entry per occurrence.
func argsOf(doc *ast.QueryDocument, name string) [][]string {
	var found [][]string
	var walk func(ast.SelectionSet)
	walk = func(set ast.SelectionSet) {
		for _, sel := range set {
			switch s := sel.(type) {
			case *ast.Field:
				if s.Name == name {
					var names []string
					for _, a := range s.Arguments {
						names = append(names, a.Name)
					}
					sort.Strings(names)
					found = append(found, names)
				}
				walk(s.SelectionSet)
			case *ast.InlineFragment:
				walk(s.SelectionSet)
			}
		}
	}
	for _, op := range doc.Operations {
		walk(op.SelectionSet)
	}
	for _, fr := range doc.Fragments {
		walk(fr.SelectionSet)
	}
	return found
}

func TestEntityRefsS5DocumentsSendTheRefArguments_2935(t *testing.T) {
	_, doc := loadQueryDocuments(t)
	for _, tc := range []struct {
		field        string
		want, retire string
	}{
		{"cancelAppRun", "ref", "id"},
		{"startAgentSession", "agentRef", "agentId"},
		{"appRuns", "parentRunRef", "parentRunId"},
	} {
		uses := argsOf(doc, tc.field)
		if len(uses) == 0 {
			t.Errorf("no document selects %s: the pin cannot see it", tc.field)
			continue
		}
		for _, names := range uses {
			joined := "," + strings.Join(names, ",") + ","
			if !strings.Contains(joined, ","+tc.want+",") {
				t.Errorf("%s must send %s (hadron-server#4141 S5), sends %v", tc.field, tc.want, names)
			}
			if strings.Contains(joined, ","+tc.retire+",") {
				t.Errorf("%s still sends the retired %s, sends %v", tc.field, tc.retire, names)
			}
		}
	}
}

// The snapshot carries the renamed arguments and not the retired ones, so
// a document still naming a retired one fails validation above.
func TestEntityRefsS5SnapshotHasOnlyTheRefArguments_2935(t *testing.T) {
	schema, _ := loadQueryDocuments(t)
	for _, tc := range []struct {
		root          *ast.Definition
		field         string
		want, retired string
	}{
		{schema.Mutation, "cancelAppRun", "ref", "id"},
		{schema.Mutation, "startAgentSession", "agentRef", "agentId"},
		{schema.Query, "appRuns", "parentRunRef", "parentRunId"},
	} {
		f := tc.root.Fields.ForName(tc.field)
		if f == nil {
			t.Errorf("the snapshot has no %s.%s", tc.root.Name, tc.field)
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
