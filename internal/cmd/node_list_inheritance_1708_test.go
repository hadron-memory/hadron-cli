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

func runInherited1708(t *testing.T, rows string, checkDocument bool, args ...string) (string, int32, error) {
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
		// Offset pagination drains through one empty page. Bound unexpected extra
		// reads so a future variable-name change fails instead of looping forever.
		if reads.Load() > 2 {
			t.Error("unexpected extra page; update this offset fake for the real access pattern")
			rows = ""
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
	return out.String(), reads.Load(), err
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
	for _, flags := range [][]string{nil, {"--with-data"}, {"--with-properties"}, {"--with-data", "--with-properties"}, {"--has-unresolved-comments=false"}, {"--seq-gt", "1"}, {"--has-unresolved-comments=false", "--with-data", "--with-properties", "--seq-gt", "1"}} {
		t.Run(strings.Join(flags, "_"), func(t *testing.T) {
			out, reads, err := runInherited1708(t, rows, true, append(append([]string{}, flags...), "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			if reads != 2 {
				t.Fatalf("list reads=%d, want data page plus empty terminating page", reads)
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
			if _, exists := got[1]["inheritedFrom"]; exists {
				t.Fatalf("own node invented inheritance %s", out)
			}
			out, _, err = runInherited1708(t, rows, false, flags...)
			if err != nil || !strings.Contains(out, inheritedURN1708) || !strings.Contains(out, baseAgent1708) || !strings.Contains(strings.ToLower(out), "inherited") {
				t.Fatalf("text err%v out%q", err, out)
			}
			projected := false
			for _, flag := range flags {
				projected = projected || flag == "--with-data" || flag == "--with-properties"
			}
			if projected {
				blocks := strings.Split(out, "\n\n")
				if len(blocks) < 2 || !strings.HasPrefix(blocks[0], "tasks:review  Review  (task)  seq 2\n  URN: "+inheritedURN1708+"\n  INHERITED FROM: "+baseAgent1708+"\n") || !strings.HasPrefix(blocks[1], "tasks:build  Build  (task)  seq 3\n") || strings.Contains(blocks[1], "INHERITED FROM:") {
					t.Fatalf("projected block association: %q", out)
				}
			} else {
				assertInheritanceTable1708(t, out)
			}
		})
	}
}
func TestNodeListOwnAndConcealed1708(t *testing.T) {
	out, _, err := runInherited1708(t, hit1708(ownRow1708), false)
	if err != nil || strings.Contains(strings.ToLower(out), "inherited") || !strings.Contains(out, "tasks:build") {
		t.Fatalf("own text %v %q", err, out)
	}
	// A base memory id alone is not evidence of inheritance: never infer a base.
	concealed := strings.Replace(inheritedRow1708, `{"agentId":"base-agent","agentUrn":"`+baseAgent1708+`"}`, `null`, 1)
	out, _, err = runInherited1708(t, hit1708(concealed), false, "--json")
	if err != nil || strings.Contains(out, baseAgent1708) {
		t.Fatalf("concealed %v %q", err, out)
	}
	for _, flags := range [][]string{nil, {"--with-data"}, {"--has-unresolved-comments=false"}} {
		out, _, err = runInherited1708(t, hit1708(concealed), false, flags...)
		if err != nil || strings.Contains(out, baseAgent1708) || strings.Contains(out, "INHERITED FROM") || !strings.Contains(out, "tasks:review") {
			t.Fatalf("concealed text: %v %q", err, out)
		}
	}
}

func TestNodeListInheritedMalformed1708(t *testing.T) {
	for _, pair := range [][2]string{{`"urn":"` + inheritedURN1708 + `"`, `"urn":""`}, {`"agentId":"base-agent"`, `"agentId":""`}, {`"agentUrn":"` + baseAgent1708 + `"`, `"agentUrn":""`}} {
		row := strings.Replace(inheritedRow1708, pair[0], pair[1], 1)
		out, reads, err := runInherited1708(t, hit1708(row), false, "--limit", "1", "--json")
		if err == nil || out != "" || reads != 1 {
			t.Fatalf("malformed %v out%q reads%d", err, out, reads)
		}
	}
}
func TestNodeListInheritedSafeText1708(t *testing.T) {
	for _, field := range []string{baseAgent1708, inheritedURN1708} {
		row := strings.Replace(inheritedRow1708, field, field+`\nFORGED`, 1)
		for _, flags := range [][]string{nil, {"--with-data"}} {
			out, _, err := runInherited1708(t, hit1708(row), false, flags...)
			if err != nil || strings.Contains(out, "\nFORGED") {
				t.Fatalf("unsafe %v %q", err, out)
			}
		}
		out, _, err := runInherited1708(t, hit1708(row), false, "--json")
		if err != nil || !strings.Contains(out, `\nFORGED`) {
			t.Fatalf("raw provenance %v %q", err, out)
		}
	}
}

// Use header start offsets so blank RUN and own-row origin cells remain observable.
// Splitting on whitespace would collapse those cells and miss a column shift.
func assertInheritanceTable1708(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("table cardinality: %q", out)
	}
	headers := []string{"LOC", "NAME", "TYPE", "SEQ", "RUN", "URN", "INHERITED FROM"}
	if strings.Contains(lines[0], "OPEN THREADS") {
		headers = append(headers, "OPEN THREADS", "OLDEST")
	}
	starts := make([]int, len(headers))
	for i, h := range headers {
		starts[i] = strings.Index(lines[0], h)
		if starts[i] < 0 || (i > 0 && starts[i] <= starts[i-1]) {
			t.Fatalf("header order: %q", lines[0])
		}
	}
	want := [][]string{
		{"tasks:review", "Review", "task", "2", "", inheritedURN1708, baseAgent1708},
		{"tasks:build", "Build", "task", "3", "", "hrn:node:acme.com:child-system:tasks:build", ""},
	}
	for r, line := range lines[1:] {
		if len(headers) > 7 {
			want[r] = append(want[r], "0", "-")
		}
		for c, start := range starts {
			end := len(line)
			if c+1 < len(starts) && starts[c+1] < end {
				end = starts[c+1]
			}
			got := ""
			if start < len(line) {
				got = strings.TrimSpace(line[start:end])
			}
			if got != want[r][c] {
				t.Fatalf("row%d column%s = %q want%q; table%q", r, headers[c], got, want[r][c], out)
			}
		}
	}
}
