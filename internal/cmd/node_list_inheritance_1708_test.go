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

const inheritedURN1708 = "hrn:node:acme.com:base-system:tasks:review"
const baseAgent1708 = "hrn:agent:acme.com:base"
const inheritedRow1708 = `{"id":"base-node","urn":"` + inheritedURN1708 + `","memoryId":"base-memory","loc":"tasks:review","name":"Review","nodeType":"task","seq":2,"tags":[],"role":"task","inheritedFrom":{"agentId":"base-agent","agentUrn":"` + baseAgent1708 + `"},"commentSummary":{"openThreads":0,"oldestOpenThreadAt":null},"data":{"x":1},"properties":{"y":2}}`
const ownRow1708 = `{"id":"own-node","urn":"hrn:node:acme.com:child-system:tasks:build","memoryId":"child-memory","loc":"tasks:build","name":"Build","nodeType":"task","seq":3,"tags":[],"role":"task","inheritedFrom":null,"commentSummary":{"openThreads":0,"oldestOpenThreadAt":null},"data":null,"properties":null}`

func runInherited1708(t *testing.T, rows string, checkDocument bool, args ...string) (string, error, int32) {
	t.Helper()
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var q struct {
			Query         string                     `json:"query"`
			OperationName string                     `json:"operationName"`
			Variables     map[string]json.RawMessage `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		if q.OperationName != "FindNodes" && q.OperationName != "FindNodesWithCommentSummary" {
			t.Errorf("unexpected operation %s", q.OperationName)
		}
		reads.Add(1)
		if checkDocument {
			assertInheritedSelection1708(t, q.Query, q.OperationName)
		}
		items := rows
		if string(q.Variables["offset"]) != "" && string(q.Variables["offset"]) != "null" && string(q.Variables["offset"]) != "0" {
			items = ""
		}
		_, _ = w.Write([]byte(`{"data":{"findNodes":{"total":null,"degraded":null,"reason":null,"hits":[` + items + `]}}}`))
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"node", "list", "--server", srv.URL, "-m", "hrn:mem:acme.com:child-system"}, args...))
	err := root.Execute()
	return out.String(), err, reads.Load()
}
func assertInheritedSelection1708(t *testing.T, query, opName string) {
	t.Helper()
	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		t.Error(err)
		return
	}
	op := doc.Operations.ForName(opName)
	if op == nil {
		t.Error("operation missing")
		return
	}
	fields := op.SelectionSet
	for _, name := range []string{"findNodes", "hits", "node"} {
		f := field1708(fields, name)
		if f == nil {
			t.Errorf("missing %s", name)
			return
		}
		fields = f.SelectionSet
	}
	for _, name := range []string{"urn", "inheritedFrom"} {
		f := field1708(fields, name)
		if f == nil || len(f.Directives) != 0 {
			t.Errorf("missing unconditional node.%s", name)
			continue
		}
		if name == "inheritedFrom" {
			for _, child := range []string{"agentId", "agentUrn"} {
				if field1708(f.SelectionSet, child) == nil {
					t.Errorf("missing inheritedFrom.%s", child)
				}
			}
		}
	}
}
func field1708(fields ast.SelectionSet, name string) *ast.Field {
	for _, s := range fields {
		if f, ok := s.(*ast.Field); ok && f.Name == name {
			return f
		}
	}
	return nil
}
func hit1708(row string) string { return `{"score":0,"node":` + row + `}` }
func TestNodeListInherited1708(t *testing.T) {
	rows := hit1708(inheritedRow1708) + "," + hit1708(ownRow1708)
	for _, flags := range [][]string{nil, {"--with-data"}, {"--with-properties"}, {"--with-data", "--with-properties"}, {"--has-unresolved-comments=false"}, {"--seq-gt", "1"}} {
		t.Run(strings.Join(flags, "_"), func(t *testing.T) {
			out, err, reads := runInherited1708(t, rows, true, append(append([]string{}, flags...), "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			if reads == 0 {
				t.Fatal("no list request")
			}
			var got []map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 {
				t.Fatalf("rows%s", out)
			}
			if string(got[0]["urn"]) != `"`+inheritedURN1708+`"` {
				t.Fatalf("base address lost: %s", out)
			}
			var inherited map[string]string
			if err := json.Unmarshal(got[0]["inheritedFrom"], &inherited); err != nil {
				t.Fatal(err)
			}
			if inherited["agentId"] != "base-agent" || inherited["agentUrn"] != baseAgent1708 {
				t.Fatalf("base marker %s", out)
			}
			if string(got[1]["urn"]) != `"hrn:node:acme.com:child-system:tasks:build"` {
				t.Fatal(out)
			}
			if v := got[1]["inheritedFrom"]; len(v) > 0 && string(v) != "null" {
				t.Fatalf("own node invented inheritance %s", out)
			}
			out, err, _ = runInherited1708(t, rows, false, flags...)
			if err != nil || !strings.Contains(out, inheritedURN1708) || !strings.Contains(out, baseAgent1708) || !strings.Contains(strings.ToLower(out), "inherited") {
				t.Fatalf("text err%v out%q", err, out)
			}
		})
	}
}
func TestNodeListOwnAndConcealed1708(t *testing.T) {
	out, err, _ := runInherited1708(t, hit1708(ownRow1708), false)
	if err != nil || strings.Contains(strings.ToLower(out), "inherited") || !strings.Contains(out, "tasks:build") {
		t.Fatalf("own text %v %q", err, out)
	}
	// A base memory id alone is not evidence of inheritance: never infer a base.
	concealed := strings.Replace(inheritedRow1708, `{"agentId":"base-agent","agentUrn":"`+baseAgent1708+`"}`, `null`, 1)
	out, err, _ = runInherited1708(t, hit1708(concealed), false, "--json")
	if err != nil || strings.Contains(out, baseAgent1708) {
		t.Fatalf("concealed %v %q", err, out)
	}
}
