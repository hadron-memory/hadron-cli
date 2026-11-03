package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const edgeCap1210 = `{"errors":[{"message":"Read edges in pages","extensions":{"code":"BAD_USER_INPUT","limit":"edges","max":2000}}]}`

// The same provider projection must survive a bounded split: no edges() query,
// no new endpoint lookup, and no accidental disclosure of a null endpoint.
func TestNodeBatchEdgeCap1210(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonMode], func(t *testing.T) {
			run := func(limit int) (string, int, error) {
				var calls atomic.Int64
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var q struct {
						OperationName string
						Variables     struct{ Refs []string }
					}
					_ = json.NewDecoder(r.Body).Decode(&q)
					if body, ok := unstubbedDefault(q.OperationName); ok {
						_, _ = w.Write([]byte(body))
						return
					}
					if q.OperationName != "NodeBatch" {
						t.Errorf("unexpected operation %s", q.OperationName)
					}
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if len(q.Variables.Refs) > limit {
						_, _ = w.Write([]byte(edgeCap1210))
						return
					}
					var nodes []string
					for _, ref := range q.Variables.Refs {
						loc := ref[strings.LastIndex(ref, ":")+1:]
						n := batchNodeJSON("id-"+loc, loc)
						n = strings.Replace(n, `"outgoingEdges":[]`, `"outgoingEdges":[{"id":"hidden","name":"hidden","target":null}]`, 1)
						n = strings.Replace(n, `"incomingEdges":[]`, `"incomingEdges":[{"id":"incoming","name":"inherited","source":{"id":"origin","loc":"base:rule","memoryId":"base"}}]`, 1)
						nodes = append(nodes, n)
					}
					_, _ = w.Write([]byte(nodeBatchResult(nodes, "")))
				}))
				defer s.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"node", "get", "alpha", "beta", "-m", "acme.com::kb", "--server", s.URL}
				if jsonMode {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				err := root.Execute()
				return out.String(), int(calls.Load()), err
			}
			want, n, err := run(2)
			if err != nil || n != 1 {
				t.Fatal(err, n)
			}
			got, n, err := run(1)
			if err != nil {
				t.Fatalf("split failed: %v", err)
			}
			if n != 3 || got != want {
				t.Fatalf("calls=%d; output parity=%v\n%s", n, got == want, got)
			}
		})
	}
}

func TestNodeBatchEdgeCapExplicitRefusal1210(t *testing.T) {
	for _, args := range [][]string{
		{"node", "get", "alpha", "beta", "-m", "acme.com::kb"},
		{"node", "get", "--prefix", "alpha:", "-m", "acme.com::kb"},
	} {
		gql, _ := captureGraphQL(t, map[string]string{"NodeBatch": edgeCap1210})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", gql.URL))
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "2000") || !strings.Contains(err.Error(), "hadron edge list") || out.Len() != 0 {
			t.Fatalf("want actionable refusal/no output; err=%v out=%s", err, out)
		}
	}
}
