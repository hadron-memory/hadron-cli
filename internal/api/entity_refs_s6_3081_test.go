package api

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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
// No alias and no fallback, by ruling 0274 (an ordered hard break: the server
// first, then this companion inside the paired window; cor:api:140:01). A
// fallback to the old names would have to test a capability token rather than
// try the operation (cor:api:220:01), so released CLIs older than this change
// stop working with those reads at the server cutover, by design.
//
// The CLI selects memoryAssets, memoryLocSegments and usageEventsPage;
// usageEvents has no CLI document, so only its snapshot side is pinned.
// rootFieldArgs is the S5 pin's helper (entity_refs_s5_2935_test.go).

// s6Rename is one row of the rename table above.
type s6Rename struct {
	field         string
	want, retired string
	cliSelects    bool
}

var s6Renames = []s6Rename{
	{"memoryAssets", "memoryRef", "memoryId", true},
	{"memoryLocSegments", "memoryRefs", "memoryIds", true},
	{"usageEventsPage", "memoryRef", "memoryId", true},
	{"usageEventsPage", "nodeRef", "nodeId", true},
	{"usageEvents", "memoryRef", "memoryId", false},
	{"usageEvents", "nodeRef", "nodeId", false},
}

// unpinnedReport is one tripwire failure: a document that selects a field
// whose arguments the pin does not cover (its rows have cliSelects false),
// with the unpinned field names it selects.
type unpinnedReport struct {
	where  string
	fields []string
}

// s6UnpinnedReports folds the rename table's cliSelects=false rows into one
// report per failing document, with the distinct unpinned fields it selects
// in table order. The usageEvents arguments are two rows sharing one field,
// so without the fold the tripwire named a failing document once per row
// (cli#3094). Each field's uses are looked up once.
func s6UnpinnedReports(rows []s6Rename, usesFor func(field string) []fieldUse) []unpinnedReport {
	var reports []unpinnedReport
	index := map[string]int{}
	for _, tc := range rows {
		if tc.cliSelects {
			continue
		}
		for _, u := range usesFor(tc.field) {
			i, ok := index[u.where]
			if !ok {
				i = len(reports)
				index[u.where] = i
				reports = append(reports, unpinnedReport{where: u.where})
			}
			if !slices.Contains(reports[i].fields, tc.field) {
				reports[i].fields = append(reports[i].fields, tc.field)
			}
		}
	}
	return reports
}

func TestS6DocumentsSendTheRefArgumentsNotTheRetiredOnes_3081(t *testing.T) {
	for _, tc := range s6Renames {
		uses := rootFieldArgs(t, ast.Query, tc.field)
		if !tc.cliSelects {
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
	usesByField := map[string][]fieldUse{}
	for _, r := range s6UnpinnedReports(s6Renames, func(field string) []fieldUse {
		uses, ok := usesByField[field]
		if !ok {
			uses = rootFieldArgs(t, ast.Query, field)
			usesByField[field] = uses
		}
		return uses
	}) {
		t.Errorf("%s selects %s: set cliSelects for it so its arguments are pinned", r.where, strings.Join(r.fields, ", "))
	}
}

// The usageEvents arguments are two rows of s6Renames sharing one field, so
// the tripwire named a failing document once per row. It must name each
// failing document once (cli#3094).
func TestS6TripwireNamesEachUnpinnedDocumentOnce_3081(t *testing.T) {
	rows := []s6Rename{
		{field: "usageEvents", want: "memoryRef", retired: "memoryId"},
		{field: "usageEvents", want: "nodeRef", retired: "nodeId"},
	}
	uses := []fieldUse{{where: "queries/memory_usage.graphql:14", names: []string{}}}
	reports := s6UnpinnedReports(rows, func(string) []fieldUse { return uses })
	if len(reports) != 1 {
		t.Fatalf("the tripwire reported the failing document %d times, want once: %v", len(reports), reports)
	}
	if reports[0].where != "queries/memory_usage.graphql:14" {
		t.Errorf("the report names %s, want the failing document", reports[0].where)
	}
	if len(reports[0].fields) != 1 || reports[0].fields[0] != "usageEvents" {
		t.Errorf("the report's unpinned fields are %v, want [usageEvents]", reports[0].fields)
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
	if schema.Query == nil {
		t.Fatal("the snapshot has no Query type")
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
