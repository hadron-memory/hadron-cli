package cmd

import (
	"bytes"
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"strings"
	"testing"
)

// cli#2844 item 2: edge data (the cross-memory route marker among it) is shown
// verbatim by edge get --json, edge list --json and node get --json (single and
// batch), and is an explicit null when the edge has none.

const marker2844 = `{"crossMemory":true,"counter":9007199254740993}`

func withEdgeData2844(payload, data string) string {
	payload = strings.Replace(payload, `"outgoingEdges":[]`,
		`"outgoingEdges":[{"id":"eo","name":"routes-to","loc":"a:routes-to:b","isRunnable":false,"priority":0,"data":`+data+`,"target":{"id":"n2","loc":"b","memoryId":"mem1"}}]`, 1)
	return strings.Replace(payload, `"incomingEdges":[]`,
		`"incomingEdges":[{"id":"ei","name":"from","loc":"c:from:a","isRunnable":false,"priority":0,"data":`+data+`,"source":{"id":"n3","loc":"c","memoryId":"mem1"}}]`, 1)
}

func edgeData2844(t *testing.T, label string, m map[string]json.RawMessage) string {
	t.Helper()
	v, ok := m["data"]
	if !ok {
		t.Fatalf("%s: key \"data\" must always be present (null when none), got %v", label, m)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, v); err != nil {
		t.Fatalf("%s: data is not JSON: %v", label, err)
	}
	return buf.String()
}

func TestEdgeGetShowsEdgeData2844(t *testing.T) {
	for _, tc := range []struct{ name, data, want string }{
		{"object", marker2844, marker2844},
		{"none", "null", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edge := strings.Replace(uniformEdge1513("null", "null", "null"), `"priority":0,`, `"priority":0,"data":`+tc.data+`,`, 1)
			out, _, err := run1513(t, map[string]string{"GetEdge": `{"data":{"edge":` + edge + `}}`}, "edge", "get", "e1", "--json")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &m); err != nil {
				t.Fatal(err)
			}
			if got := edgeData2844(t, "edge get", m); got != tc.want {
				t.Errorf("edge get data = %s, want %s (verbatim, big numbers exact)", got, tc.want)
			}
		})
	}
}

func TestEdgeListPageShowsEdgeData2844(t *testing.T) {
	edge := strings.Replace(uniformEdge1513("null", "null", "null"), `"priority":0,`, `"priority":0,"data":`+marker2844+`,`, 1)
	out, _, err := run1513(t, map[string]string{"ListEdges": `{"data":{"edges":{"total":1,"items":[` + edge + `]}}}`}, "edge", "list", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var page struct {
		Edges []map[string]json.RawMessage `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil || len(page.Edges) != 1 {
		t.Fatalf("page: %v\n%s", err, out)
	}
	if got := edgeData2844(t, "edge list", page.Edges[0]); got != marker2844 {
		t.Errorf("edge list data = %s, want %s", got, marker2844)
	}
}

func checkNodeEdges2844(t *testing.T, label string, m map[string]json.RawMessage, want string) {
	t.Helper()
	for _, key := range []string{"outgoingEdges", "incomingEdges"} {
		var edges []map[string]json.RawMessage
		if err := json.Unmarshal(m[key], &edges); err != nil || len(edges) != 1 {
			t.Fatalf("%s %s: %v %s", label, key, err, m[key])
		}
		if got := edgeData2844(t, label+" "+key, edges[0]); got != want {
			t.Errorf("%s %s data = %s, want %s", label, key, got, want)
		}
	}
}

func TestNodeGetShowsEdgeData2844(t *testing.T) {
	for _, tc := range []struct{ name, data, want string }{
		{"object", marker2844, marker2844},
		{"none", "null", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := run1513(t, nodeGetStubs(withEdgeData2844(node1513("null"), tc.data)), "node", "get", testNodeURN, "--json")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &m); err != nil {
				t.Fatal(err)
			}
			checkNodeEdges2844(t, "node get", m, tc.want)

			// The batch read prints what the single read prints.
			bout, _, err := run1513(t, map[string]string{
				"NodeBatch": nodeBatchResult([]string{withEdgeData2844(batchNode1513("null"), tc.data)}, ""),
			}, "node", "get", "alpha", "beta", "-m", "acme.com::kb", "--json")
			if err != nil {
				t.Fatalf("%v\n%s", err, bout)
			}
			var env struct {
				Nodes []map[string]json.RawMessage `json:"nodes"`
			}
			if err := json.Unmarshal([]byte(bout), &env); err != nil || len(env.Nodes) == 0 {
				t.Fatalf("batch: %v\n%s", err, bout)
			}
			checkNodeEdges2844(t, "node get (batch)", env.Nodes[0], tc.want)
		})
	}
}

// The stubs above answer with data whatever the query selects, so they cannot
// notice a dropped selection (review: assert-the-query-not-the-capture). These
// pin the generated operation text itself: the edge selection of each read asks
// for `data`.
func selectionAfter2844(t *testing.T, op, anchor string) string {
	t.Helper()
	i := strings.Index(op, anchor)
	if i < 0 {
		t.Fatalf("anchor %q not found in operation", anchor)
	}
	rest := op[i+len(anchor):]
	// the selection ends at the endpoint sub-selection ("target {" / "source {")
	end := len(rest)
	for _, stop := range []string{"target {", "source {"} {
		if j := strings.Index(rest, stop); j >= 0 && j < end {
			end = j
		}
	}
	return rest[:end]
}

func TestEdgeReadOperationsSelectEdgeData2844(t *testing.T) {
	for _, op := range []struct{ name, text string }{
		{"GetNode", gen.GetNode_Operation},
		{"NodeBatch", gen.NodeBatch_Operation},
	} {
		for _, anchor := range []string{"outgoingEdges {", "incomingEdges {"} {
			if !strings.Contains(selectionAfter2844(t, op.text, anchor), "data") {
				t.Errorf("%s: the %s selection must request data", op.name, anchor)
			}
		}
	}
	for _, op := range []struct{ name, text string }{
		{"GetEdge", gen.GetEdge_Operation},
		{"ListEdges", gen.ListEdges_Operation},
	} {
		if !strings.Contains(selectionAfter2844(t, op.text, "fragment UniformEdge on Edge {"), "data") {
			t.Errorf("%s: the UniformEdge fragment must request data", op.name)
		}
	}
}
