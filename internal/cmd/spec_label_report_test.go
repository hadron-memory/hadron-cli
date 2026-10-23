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

func TestSpecLabelReportEmptyAndFailure(t *testing.T) {
	for _, mode := range []string{"empty", "null_listing", "transport", "null_batch"} {
		t.Run(mode, func(t *testing.T) {
			empty := `{"data":{"findNodes":{"hits":[]}}}`
			page := `{"data":{"findNodes":{"hits":[{"node":{"id":"p","memoryId":"mem1","loc":"msg:010","name":"Parent","tags":["spec"]}}]}}}`
			m := map[string][]string{"ResolveUrn": {resolveSpecJSON}, "FindNodes": {empty, empty}}
			switch mode {
			case "null_listing":
				m["FindNodes"] = []string{`{"data":{"findNodes":null}}`}
			case "transport":
				m["FindNodes"] = []string{`{"errors":[{"message":"unavailable","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`}
			case "null_batch":
				m["FindNodes"] = []string{page, empty, empty}
				m["NodeBatch"] = []string{`{"data":{"nodeBatch":null}}`}
			}
			gql, _ := queueGraphQL(t, m)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "label-report", "-m", specMem, "--server", gql.URL, "--json"})
			err := root.Execute()
			if mode == "empty" {
				if err != nil || !strings.Contains(out.String(), `"items": []`) {
					t.Fatal(err, out.String())
				}
			} else if err == nil || strings.Contains(out.String(), `"items"`) {
				t.Fatal("failure appeared complete", err, out.String())
			}
		})
	}
}

func TestSpecLabelReportUnavailableSemantics(t *testing.T) {
	for _, mode := range []string{"source", "parent", "child"} {
		t.Run(mode, func(t *testing.T) {
			parent := `{"id":"p","memoryId":"mem1","loc":"msg:010","name":"Parent","tags":["spec"]}`
			child := `{"id":"c","memoryId":"mem1","loc":"msg:010:02","name":"Child","tags":["spec"]}`
			listing := `{"data":{"findNodes":{"hits":[{"node":` + parent + `},{"node":` + child + `}]}}}`
			empty := `{"data":{"findNodes":{"hits":[]}}}`
			edge := `{"id":"e","name":null,"source":null}`
			if mode == "child" {
				edge = `{"id":"e","name":null,"source":{"id":"c","loc":"msg:010:02","memoryId":"mem1"}}`
			}
			p := `{"id":"p","memoryId":"mem1","loc":"msg:010","name":"Parent","incomingEdges":[` + edge + `,{"id":"foreign","name":"outside","source":{"id":"f","loc":"msg:010:02","memoryId":"another-memory"}}]}`
			nodes := p + `,` + child
			unavail := `[]`
			if mode == "parent" {
				nodes = child
				unavail = `["p"]`
			}
			if mode == "child" {
				nodes = p
				unavail = `["c"]`
			}
			batch := `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":` + unavail + `,"nodes":[` + nodes + `]}}}`
			gql, calls := queueGraphQL(t, map[string][]string{"ResolveUrn": {resolveSpecJSON}, "FindNodes": {listing, empty, empty}, "NodeBatch": {batch}})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "label-report", "-m", specMem, "--server", gql.URL, "--json"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var result struct{ Items []map[string]any }
			if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "child" {
				want = 2
			}
			if len(result.Items) != want {
				t.Fatal(out.String())
			}
			for _, row := range result.Items {
				if row["status"] != "unavailable" || row["label"] != nil || row["childName"] != nil || len(row) != 8 {
					t.Fatal(row)
				}
				if row["edgeId"] == nil {
					if row["childId"] != nil || row["childLoc"] != nil {
						t.Fatal(row)
					}
				} else if mode == "source" && (row["childId"] != nil || row["childLoc"] != nil) {
					t.Fatal(row)
				}
			}
			if len(calls["NodeBatch"]) != 1 {
				t.Fatal("hidden endpoint/history/mutation read", calls)
			}
			for op := range calls {
				if op != "FindNodes" && op != "NodeBatch" && op != "ResolveUrn" {
					t.Fatal("unexpected op", op)
				}
			}
			var vars struct{ Refs []string }
			_ = json.Unmarshal(calls["NodeBatch"][0], &vars)
			if len(vars.Refs) != 2 || vars.Refs[0] != "p" || vars.Refs[1] != "c" {
				t.Fatal("hidden batch refs", vars)
			}
			humanServer, _ := queueGraphQL(t, map[string][]string{"ResolveUrn": {resolveSpecJSON}, "FindNodes": {listing, empty, empty}, "NodeBatch": {batch}})
			hf, hout := testFactory(t)
			hr := NewRootCmd(hf)
			hr.SetArgs([]string{"spec", "label-report", "-m", specMem, "--server", humanServer.URL})
			if e := hr.Execute(); e != nil {
				t.Fatal(e)
			}
			phrase := "unclassified candidate: source unavailable"
			if mode == "parent" {
				phrase = "unavailable-parent"
			}
			if mode == "child" {
				phrase = "unavailable child: current name unavailable"
			}
			if !strings.Contains(hout.String(), phrase) || strings.Contains(hout.String(), "verified direct relationship") {
				t.Fatal(hout.String())
			}

		})
	}
}
