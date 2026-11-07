package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func TestLintGovernanceKnowledge1658(t *testing.T) {
	for _, kind := range []string{"governed", "neither", "tag-only"} {
		t.Run(kind, func(t *testing.T) {
			detail := cleanSpecDetail
			switch kind {
			case "governed":
				detail = strings.Replace(detail, `"nodeType":"info"`, `"nodeType":"task"`, 1)
			case "neither":
				detail = strings.Replace(detail, `"role":"spec",`, "", 1)
				detail = strings.Replace(detail, `"tags":["spec","p1","messaging"]`, `"tags":[]`, 1)
			case "tag-only":
				detail = strings.Replace(detail, `"role":"spec",`, "", 1)
			}
			gql, _ := captureGraphQL(t, map[string]string{"Memories": memListJSON, "GetMemory": memGetJSON(`null`), "ResolveUrn": resolveSpecJSON, "GetNode": `{"data":{"node":` + detail + `}}`, "NodeBatch": specLintRawBodyStub(detail)})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "lint", "msg:010:02", "-m", specMem, "--json", "--server", gql.URL})
			_ = root.Execute()
			var fs []map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out.String()), &fs); err != nil {
				t.Fatal(err)
			}
			memorySeen, nodeSeen := false, false
			for _, finding := range fs {
				var rule string
				if err := json.Unmarshal(finding["rule"], &rule); err != nil {
					t.Fatal(err)
				}
				raw, ok := finding["governed"]
				if !ok {
					t.Fatalf("governed absent: %s", out)
				}
				if rule == "vector-index" {
					memorySeen = true
					if string(raw) != "null" {
						t.Fatalf("non-node governance=%s want null", raw)
					}
					continue
				}
				nodeSeen = true
				want := "false"
				if kind == "governed" {
					want = "true"
				}
				if string(raw) != want {
					t.Fatalf("node governance=%s want %s", raw, want)
				}
			}
			if !memorySeen || !nodeSeen {
				t.Fatalf("memory=%v node=%v out=%s", memorySeen, nodeSeen, out)
			}
		})
	}
}

func TestSpecListRoleSelection1658(t *testing.T) {
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName, Query string
			Variables            json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			http.Error(w, "invalid", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if q.OperationName == "FindNodes" {
			reads.Add(1)
			doc, err := parser.ParseQuery(&ast.Source{Input: q.Query})
			if err != nil {
				t.Error(err)
				http.Error(w, "invalid", 500)
				return
			}
			op := doc.Operations.ForName("FindNodes")
			if op == nil {
				t.Error("missing FindNodes operation")
				return
			}
			var fields ast.SelectionSet = op.SelectionSet
			for _, name := range []string{"findNodes", "hits", "node", "role"} {
				var found *ast.Field
				for _, selection := range fields {
					if f, ok := selection.(*ast.Field); ok && f.Name == name {
						found = f
						break
					}
				}
				if found == nil {
					t.Errorf("actual query missing selection %s", name)
					http.Error(w, "projection", 500)
					return
				}
				if name == "role" && len(found.Directives) != 0 {
					t.Error("role must be unconditional")
				}
				fields = found.SelectionSet
			}
			body := `{"data":{"nodes":[` + specNodeListWithRole("role-only", `[]`) + `]}}`
			_, _ = w.Write([]byte(translateFindNodes(q.OperationName, pageBeyondFirst(q.OperationName, q.Variables, body))))
			return
		}
		if q.OperationName == "Memories" {
			_, _ = w.Write([]byte(memListJSON))
			return
		}
		if body, ok := unstubbedDefault(q.OperationName); ok {
			_, _ = w.Write([]byte(body))
			return
		}
		t.Errorf("unexpected %s", q.OperationName)
		http.Error(w, "unexpected", 500)
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "list", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if reads.Load() == 0 || !strings.Contains(out.String(), `"governed": true`) {
		t.Fatalf("reads=%d out=%s", reads.Load(), out)
	}
}
