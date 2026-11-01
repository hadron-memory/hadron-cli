package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestContentBatchCallers1210(t *testing.T) {
	good := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:a", "tasks:a", true, `{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."}}}`, `"# A\n\nSteps."`)
	cases := []struct {
		name  string
		args  []string
		stubs map[string]string
	}{
		{"skill", []string{"skill", "lint", "-m", "hrn:mem:hadronmemory.com:core"}, map[string]string{"SkillPlan": emptyIncludeLintPlan, "GetMemory": skillMemOrg, "FindNodes": listOf("n1"), "NodeBatchContent": batchOf(good)}},
		{"coding", []string{"coding", "review", "lint", "-m", codingMem}, map[string]string{"GetNode": codingRootJSON("review", inEdge("e1", "Applies when a resolver changes", "review:ok"), ""), "FindNodes": `{"data":{"nodes":[` + codingListNode("review:ok") + `]}}`, "NodeBatchContent": codingBatch([]string{codingBatchNode("review:ok", `"review"`, "Applies when a resolver changes.")}, "")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					OperationName, Query string
					Variables            json.RawMessage
				}
				_ = json.NewDecoder(r.Body).Decode(&q)
				if q.OperationName == "NodeBatch" {
					t.Error("content-only caller selected edges")
				}
				if q.OperationName == "NodeBatchContent" {
					calls.Add(1)
					if strings.Contains(q.Query, "outgoingEdges") || strings.Contains(q.Query, "incomingEdges") {
						t.Error("document selects edges")
					}
				}
				body, ok := tc.stubs[q.OperationName]
				if !ok {
					body, ok = unstubbedDefault(q.OperationName)
				}
				if !ok {
					t.Errorf("unexpected %s", q.OperationName)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(translateFindNodes(q.OperationName, pageBeyondFirst(q.OperationName, q.Variables, body))))
			}))
			defer s.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", s.URL))
			if err := root.Execute(); err != nil || calls.Load() != 1 {
				t.Fatal(err, calls.Load(), out.String())
			}
		})
	}
}
