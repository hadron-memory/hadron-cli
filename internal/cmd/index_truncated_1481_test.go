package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// hadron-cli#1481 — the CLI half of hadron-server#887 slice 2: a node with more
// chunks than the index cap records how many it left out
// (Node.indexTruncated { omittedChunks }, null when fully indexed). `node get`
// shows it in human output and --json, single and batch alike.

const truncated1481 = `{"omittedChunks":44}`

// withIndexTruncated puts the server's indexTruncated answer into a payload.
func withIndexTruncated(payload, v string) string {
	return strings.Replace(payload, `"outgoingEdges"`, `"indexTruncated":`+v+`,"outgoingEdges"`, 1)
}

func singleJSON1481(t *testing.T, v string) map[string]json.RawMessage {
	t.Helper()
	out, _, err := run1513(t, nodeGetStubs(withIndexTruncated(node1513("null"), v)), "node", "get", testNodeURN, "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	return m
}

func batchJSON1481(t *testing.T, v string) map[string]json.RawMessage {
	t.Helper()
	out, _, err := run1513(t, map[string]string{
		"NodeBatch": nodeBatchResult([]string{withIndexTruncated(batchNode1513("null"), v)}, ""),
	}, "node", "get", "alpha", "beta", "-m", "acme.com::kb", "--json")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	var env struct {
		Nodes []map[string]json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Nodes) == 0 {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	return env.Nodes[0]
}

// Acceptance 1 (JSON): the key is always present — the object when the server
// reports a truncation, an explicit null when fully indexed — and single and
// batch agree.
func TestNodeGetIndexTruncatedJSON1481(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"truncated", truncated1481, `{"omittedChunks":44}`},
		{"fully indexed", "null", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for label, m := range map[string]map[string]json.RawMessage{
				"single": singleJSON1481(t, tc.in),
				"batch":  batchJSON1481(t, tc.in),
			} {
				got, ok := m["indexTruncated"]
				if !ok {
					t.Fatalf("%s: indexTruncated key must always be present", label)
				}
				if cmp(got) != tc.want {
					t.Errorf("%s indexTruncated = %s, want %s", label, got, tc.want)
				}
			}
		})
	}
}

// Acceptance 1 (human) and 2: the marker prints when present, and a fully
// indexed node prints nothing about indexing.
func TestNodeGetIndexTruncatedHuman1481(t *testing.T) {
	human := func(v string, args ...string) string {
		t.Helper()
		out, _, err := run1513(t, nodeGetStubs(withIndexTruncated(node1513("null"), v)), append([]string{"node", "get", testNodeURN}, args...)...)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		return out
	}
	out := human(truncated1481)
	if !strings.Contains(out, "  index: truncated, 44 chunks not embedded\n") {
		t.Errorf("truncated node must print its marker:\n%s", out)
	}
	if out := human("null"); strings.Contains(strings.ToLower(out), "index") {
		t.Errorf("fully indexed node prints nothing about indexing:\n%s", out)
	}
	one := human(`{"omittedChunks":1}`)
	if !strings.Contains(one, "  index: truncated, 1 chunk not embedded\n") {
		t.Errorf("singular chunk wording:\n%s", one)
	}
}

// `node next` renders through the same detail DTO, so it carries the key too
// (Jack's BLIND P2, Celine's QA P3): the query must select the field, and the
// output must show the object when truncated and an explicit null otherwise.
func TestNodeNextIndexTruncated1481(t *testing.T) {
	if !strings.Contains(gen.NextNode_Operation, "indexTruncated") {
		t.Fatal("the NextNode query must select indexTruncated")
	}
	for _, tc := range []struct{ name, in, want string }{
		{"truncated", truncated1481, `{"omittedChunks":44}`},
		{"fully indexed", "null", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := `{"data":{"nextNode":` + withIndexTruncated(nodeDetailJSON, tc.in) + `}}`
			out, stderr, code, _ := runNext1009(t, resp, "hrn:node:acme.com:kb:p", "--json")
			if code != 0 || stderr != "" {
				t.Fatalf("code %d stderr %q", code, stderr)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &m); err != nil {
				t.Fatalf("--json: %v\n%s", err, out)
			}
			got, ok := m["indexTruncated"]
			if !ok {
				t.Fatal("indexTruncated key must always be present")
			}
			if cmp(got) != tc.want {
				t.Errorf("indexTruncated = %s, want %s", got, tc.want)
			}
		})
	}
}
