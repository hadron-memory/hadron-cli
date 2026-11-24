package api

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// cli#3081, the entity-refs S6 chunk A companion (hadron-server#4142, PR
// #4822: a hard rename with no alias, cor:api:140). The renames, read from a
// parsed diff of the SDL exported at #4822's head against its merge base:
//
//	memoryAssets(memoryId:)        -> memoryAssets(memoryRef:)
//	memoryLocSegments(memoryIds:)  -> memoryLocSegments(memoryRefs:)
//	usageEvents(memoryId:, nodeId:)     -> (memoryRef:, nodeRef:)
//	usageEventsPage(memoryId:, nodeId:) -> (memoryRef:, nodeRef:)
//
// The CLI selects memoryAssets, memoryLocSegments and usageEventsPage;
// usageEvents has no CLI document, so only its snapshot side is pinned.
// rootFieldArgs is the S5 pin's helper (entity_refs_s5_2935_test.go).

var s6Renames = []struct {
	field         string
	want, retired string
	cliSelects    bool
}{
	{"memoryAssets", "memoryRef", "memoryId", true},
	{"memoryLocSegments", "memoryRefs", "memoryIds", true},
	{"usageEventsPage", "memoryRef", "memoryId", true},
	{"usageEventsPage", "nodeRef", "nodeId", true},
	{"usageEvents", "memoryRef", "memoryId", false},
	{"usageEvents", "nodeRef", "nodeId", false},
}

func TestS6DocumentsSendTheRefArgumentsNotTheRetiredOnes_3081(t *testing.T) {
	for _, tc := range s6Renames {
		uses := rootFieldArgs(t, ast.Query, tc.field)
		if !tc.cliSelects {
			if len(uses) != 0 {
				t.Errorf("a CLI document now selects %s: set cliSelects so its arguments are pinned", tc.field)
			}
			continue
		}
		if len(uses) == 0 {
			t.Errorf("no query selects %s: the pin cannot see it", tc.field)
			continue
		}
		for _, u := range uses {
			if !slices.Contains(u.names, tc.want) {
				t.Errorf("%s: %s must send %s (hadron-server#4142 S6), sends %v", u.where, tc.field, tc.want, u.names)
			}
			if slices.Contains(u.names, tc.retired) {
				t.Errorf("%s: %s still sends the retired %s, sends %v", u.where, tc.field, tc.retired, u.names)
			}
		}
	}
}

// The snapshot carries the renamed arguments and not the retired ones, so the
// 2696 sweep fails a document that still names a retired one.
func TestS6SnapshotCarriesOnlyTheRefArguments_3081(t *testing.T) {
	sdl, err := os.ReadFile(filepath.Join("..", "..", "schema", "schema.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	schema, gerr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if gerr != nil {
		t.Fatalf("the snapshot does not load: %v", gerr)
	}
	for _, tc := range s6Renames {
		f := schema.Query.Fields.ForName(tc.field)
		if f == nil {
			t.Errorf("the snapshot has no Query.%s", tc.field)
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
