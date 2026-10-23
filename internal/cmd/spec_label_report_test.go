package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSpecLabelReportPagesAndSpilloverReadOnly(t *testing.T) {
	p := `{"id":"p","memoryId":"mem1","loc":"msg:010","name":"Parent","tags":["spec"]}`
	c := `{"id":"c","memoryId":"mem1","loc":"msg:010:02","name":"Current child title","tags":["spec"]}`
	page := func(n string) string { return `{"data":{"findNodes":{"hits":[{"node":` + n + `}],"total":2}}}` }
	empty := `{"data":{"findNodes":{"hits":[],"total":2}}}`
	m := map[string][]string{
		"ResolveUrn": {resolveSpecJSON},
		"FindNodes":  {page(p), page(c), empty, empty},
		"NodeBatch": {
			`{"data":{"nodeBatch":{"truncated":true,"omitted":["c"],"unavailable":[],"nodes":[{"id":"p","memoryId":"mem1","loc":"msg:010","name":"Parent","incomingEdges":[{"id":"edge","name":"Editorial summary","source":{"id":"c","loc":"msg:010:02","memoryId":"mem1"}},{"id":"cross","name":"cross ref","source":{"id":"x","loc":"other:010","memoryId":"mem1"}}]}]}}}`,
			`{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` + c + `]}}}`,
		},
	}
	gql, calls := queueGraphQL(t, m)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "label-report", "-m", specMem, "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var r struct {
		MemoryRef, Prefix string
		Items             []struct{ ParentId, ParentLoc, EdgeId, Label, ChildId, ChildLoc, ChildName, Status string }
	}
	if err := json.Unmarshal([]byte(out.String()), &r); err != nil {
		t.Fatal(err, out.String())
	}
	if len(r.Items) != 1 || r.Items[0].Label != "Editorial summary" || r.Items[0].ChildName != "Current child title" || r.Items[0].Status != "available" || r.Items[0].ParentLoc != "msg:010" {
		t.Fatal(out.String())
	}
	if len(calls["FindNodes"]) != 4 || len(calls["NodeBatch"]) != 2 {
		t.Fatal(calls)
	}
	for op := range calls {
		if op != "ResolveUrn" && op != "FindNodes" && op != "NodeBatch" {
			t.Fatal("unexpected operation", op)
		}
	}
	if strings.Contains(out.String(), "stale") {
		t.Fatal("invented label quality", out.String())
	}
}
