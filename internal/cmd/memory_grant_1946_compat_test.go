package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestMemoryGrant1946OldSchema(t *testing.T) {
	s, err := os.ReadFile("testdata/memory_grant_pre4031.graphql")
	if err != nil {
		t.Fatal(err)
	}
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: string(s)})
	for _, tc := range surfaceCases1338() {
		if tc.name != "grant list" && tc.name != "grant add" && tc.name != "grant rm" {
			continue
		}
		for _, jsonOut := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, jsonOut), func(t *testing.T) {
				requests := make(chan struct {
					OperationName, Query string
					Variables            json.RawMessage
				}, 4)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						OperationName, Query string
						Variables            json.RawMessage
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						http.Error(w, "bad request", 400)
						return
					}
					requests <- req
					w.Header().Set("Content-Type", "application/json")
					if _, errs := gqlparser.LoadQuery(schema, req.Query); len(errs) > 0 {
						for _, e := range errs {
							e.Extensions = map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}
						}
						w.WriteHeader(400)
						_ = json.NewEncoder(w).Encode(map[string]any{"errors": errs})
						return
					}
					fmt.Fprint(w, `{"data":`+tc.response+`}`)
				}))
				defer srv.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := append([]string{}, tc.args...)
				if jsonOut {
					args = append(args, "--json")
				}
				root.SetArgs(append(args, "--server", srv.URL))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if len(requests) != 2 {
					t.Fatalf("requests=%d want2", len(requests))
				}
				modern, legacy := <-requests, <-requests
				if modern.OperationName != tc.op || legacy.OperationName != tc.op+"Legacy" {
					t.Fatalf("operations %s %s", modern.OperationName, legacy.OperationName)
				}
				if strings.Contains(legacy.Query, "notEffective") {
					t.Fatal("legacy document contains new fields")
				}
				if _, errs := gqlparser.LoadQuery(schema, legacy.Query); len(errs) > 0 {
					t.Fatal(errs)
				}
				var a, b any
				_ = json.Unmarshal(modern.Variables, &a)
				_ = json.Unmarshal(legacy.Variables, &b)
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("variables changed: %s vs %s", modern.Variables, legacy.Variables)
				}
				if jsonOut {
					if !strings.Contains(out.String(), `"notEffective": null`) || !strings.Contains(out.String(), `"notEffectiveReason": null`) {
						t.Fatal(out.String())
					}
				} else if strings.Contains(out.String(), "not-effective") {
					t.Fatal("invented mark", out.String())
				}
			})
		}
	}
}
