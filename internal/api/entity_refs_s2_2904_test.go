package api

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// cli#2904, the entity-refs S2 companion (hadron-server#4138, PR #4766): the
// node-revision family's revisionId argument is hard-renamed to nodeRevRef
// (a revision URN hrn:noderev:… or a primary key), with no alias:
//
//	nodeRevision(revisionId:)        -> nodeRevision(nodeRevRef:)
//	restoreNodeRevision(revisionId:) -> restoreNodeRevision(nodeRevRef:)
//	updateNodeRevision(revisionId:)  -> updateNodeRevision(nodeRevRef:)
//	deleteNodeRevision(revisionId:)  -> deleteNodeRevision(nodeRevRef:)
//
// Ruling 0274: an ordered hard break with no fallback to the old name.
// cor:api:140:01: an argument taking a primary key or a URN is <entity>Ref.
// nodeRevisionsPage(nodeRef:) is not renamed and is out of scope.
//
// Two documents select nodeRevision (NodeRevision and NodeRevisionStamped);
// minUses pins both, so dropping one from the walk cannot pass silently.
// rootFieldArgs is the S5 pin's helper (entity_refs_s5_2935_test.go).

var s2Renames = []struct {
	op      ast.Operation
	field   string
	minUses int
}{
	{ast.Query, "nodeRevision", 2},
	{ast.Mutation, "restoreNodeRevision", 1},
	{ast.Mutation, "updateNodeRevision", 1},
	{ast.Mutation, "deleteNodeRevision", 1},
}

const (
	s2Want    = "nodeRevRef"
	s2Retired = "revisionId"
)

func TestS2DocumentsSendNodeRevRefNotRevisionId_2904(t *testing.T) {
	for _, tc := range s2Renames {
		uses := rootFieldArgs(t, tc.op, tc.field)
		if len(uses) < tc.minUses {
			t.Errorf("%s: %d selection(s), want at least %d", tc.field, len(uses), tc.minUses)
		}
		for _, u := range uses {
			if !slices.Contains(u.names, s2Want) {
				t.Errorf("%s: %s must send %s (hadron-server#4138 S2), sends %v", u.where, tc.field, s2Want, u.names)
			}
			if slices.Contains(u.names, s2Retired) {
				t.Errorf("%s: %s still sends the retired %s", u.where, tc.field, s2Retired)
			}
		}
	}
}

// The snapshot carries nodeRevRef and not revisionId on all four fields, so
// the 2696 sweep fails a document that still names the retired argument.
func TestS2SnapshotCarriesOnlyNodeRevRef_2904(t *testing.T) {
	sdl, err := os.ReadFile(filepath.Join("..", "..", "schema", "schema.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	schema, gerr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if gerr != nil {
		t.Fatalf("the snapshot does not load: %v", gerr)
	}
	for _, tc := range s2Renames {
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
		if f.Arguments.ForName(s2Want) == nil {
			t.Errorf("the snapshot's %s lacks %s", tc.field, s2Want)
		}
		if f.Arguments.ForName(s2Retired) != nil {
			t.Errorf("the snapshot's %s still has the retired %s", tc.field, s2Retired)
		}
	}
}
