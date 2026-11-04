package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const labelRelationshipHelp = "A visible same-memory direct citation source establishes the relationship even when its current name cannot be read; in that case unavailable describes the current name."
const labelEdgeHelp = "Every incoming edge satisfying that source condition is inventoried, regardless of its editorial label; no special edge kind is selected."

func TestSpecLabelReportRelationshipHelp(t *testing.T) {
	for _, args := range [][]string{{"spec", "label-report", "--help"}, {"agentic-usage"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		for _, sentence := range []string{labelRelationshipHelp, labelEdgeHelp} {
			if !strings.Contains(out.String(), sentence) {
				t.Fatalf("%v missing %q", args, sentence)
			}
		}
	}
}

func runLabelPinFixture(t *testing.T, listing, batch string) ([]map[string]any, string) {
	t.Helper()
	empty := `{"data":{"findNodes":{"hits":[]}}}`
	gql, calls := queueGraphQL(t, map[string][]string{"FindNodes": {listing, empty, empty}, "NodeBatch": {batch}})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "label-report", "-m", "hrn:mem:example.com:specs", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for op := range calls {
		if op != "FindNodes" && op != "NodeBatch" {
			t.Fatalf("unexpected read/write: %s", op)
		}
	}
	if len(calls["NodeBatch"]) != 1 {
		t.Fatalf("extra endpoint lookup: %v", calls)
	}
	var result struct{ Items []map[string]any }
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatal(err)
	}
	return result.Items, out.String()
}

func TestSpecLabelReportChildLocMismatchKeepsNameUnavailable(t *testing.T) {
	// Same ID and memory, but the current visible child has moved to another
	// direct citation. Only the loc guard can prevent attaching its new name.
	parent := `{"id":"p","memoryId":"mem1","loc":"msg:010","name":"Parent","tags":["spec"]}`
	child := `{"id":"c","memoryId":"mem1","loc":"msg:010:03","name":"Name at another loc","tags":["spec"]}`
	listing := `{"data":{"findNodes":{"hits":[{"node":` + parent + `},{"node":` + child + `}]}}}`
	batch := `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[{"id":"p","memoryId":"mem1","loc":"msg:010","name":"Parent","incomingEdges":[{"id":"edge","name":"Any editorial label","source":{"id":"c","loc":"msg:010:02","memoryId":"mem1"}}]},` + child + `]}}}`
	rows, raw := runLabelPinFixture(t, listing, batch)
	if len(rows) != 1 {
		t.Fatal(raw)
	}
	row := rows[0]
	if row["childId"] != "c" || row["childLoc"] != "msg:010:02" || row["status"] != "unavailable" || row["childName"] != nil {
		t.Fatal(raw)
	}
}

func TestSpecLabelReportOrdersParentsThenEdgeIDs(t *testing.T) {
	listing := `{"data":{"findNodes":{"hits":[{"node":{"id":"b","memoryId":"mem1","loc":"msg:020","name":"B","tags":["spec"]}},{"node":{"id":"a","memoryId":"mem1","loc":"msg:010","name":"A","tags":["spec"]}}]}}}`
	// Both the batch and each edge list deliberately arrive in reverse order;
	// edge IDs restart per parent, so sorting only by edge ID is insufficient.
	batch := `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[
 {"id":"b","memoryId":"mem1","loc":"msg:020","name":"B","incomingEdges":[{"id":"z-b","name":"B z","source":{"id":"bz","loc":"msg:020:02","memoryId":"mem1"}},{"id":"a-b","name":"B a","source":{"id":"ba","loc":"msg:020:01","memoryId":"mem1"}}]},
 {"id":"a","memoryId":"mem1","loc":"msg:010","name":"A","incomingEdges":[{"id":"z-a","name":"A z","source":{"id":"az","loc":"msg:010:02","memoryId":"mem1"}},{"id":"a-a","name":"A a","source":{"id":"aa","loc":"msg:010:01","memoryId":"mem1"}}]}
 ]}}}`
	rows, raw := runLabelPinFixture(t, listing, batch)
	got := []string{}
	for _, row := range rows {
		got = append(got, row["parentLoc"].(string)+"/"+row["edgeId"].(string))
	}
	want := []string{"msg:010/a-a", "msg:010/z-a", "msg:020/a-b", "msg:020/z-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order=%v want%v: %s", got, want, raw)
	}
}
