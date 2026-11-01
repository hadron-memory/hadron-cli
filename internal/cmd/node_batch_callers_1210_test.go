package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

// Run real command entry points against the same provider projection with and
// without an aggregate edge refusal. Requests never reconstruct edges locally.
func TestEdgeCapCallerParity1210(t *testing.T) {
	src := srcTree(t, map[string]string{"x.go": "// Spec: msg:010:01 and msg:010:02\n"})
	cases := []struct {
		name    string
		args    []string
		content bool
	}{
		{"spec get", []string{"spec", "get", "--prefix", "msg:010", "-m", specMem}, false},
		{"spec lint", []string{"spec", "lint", "--all", "-m", specMem}, false},
		{"spec citations", []string{"spec", "citations", "--src", src, "-m", specMem}, false},
		{"spec label-report", []string{"spec", "label-report", "-m", specMem}, false},
		{"spec check-tools", []string{"spec", "check-tools", "-m", specMem}, true},
		{"spec grep", []string{"spec", "grep", "Win", "-m", specMem}, true},
		{"spec raw body", []string{"spec", "lint", "msg:010:02", "-m", specMem}, true},
		{"spec replacements", []string{"spec", "list", "-m", specMem}, false},
		{"spec replace relint", []string{"spec", "replace", "old", "new", "-m", specMem, "--yes"}, false},
		{"memory export", []string{"memory", "export", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, false},
	}
	for _, tc := range cases {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", tc.name, asJSON), func(t *testing.T) {
				run := func(cap int) (string, map[string]string, int, int, error) {
					var batches, contents atomic.Int64
					n1, n2 := specBatchNode("msg:010:01"), specBatchNode("msg:010:02")
					// Both directions include a concealed endpoint and an inherited readable
					// endpoint. The client must keep the provider's null/identity projection.
					n1 = strings.Replace(n1, `"incomingEdges":[]`, `"incomingEdges":[{"id":"e-in","name":"in","source":null}]`, 1)
					n2 = strings.Replace(n2, `"outgoingEdges":[`, `"outgoingEdges":[{"id":"e-hidden","name":"hidden","target":null},`, 1)
					nodes := map[string]string{"id-msg:010:01": n1, "id-msg:010:02": n2}
					listed := `{"data":{"nodes":[` + specNodeList("msg:010:01", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`
					stubs := map[string]string{"ResolveUrn": resolveSpecJSON, "FindNodes": listed, "GetMemory": memGetVectorEnabledJSON, "Memories": memListJSON, "GetNode": `{"data":{"node":` + cleanSpecDetail + `}}`}
					if tc.name == "spec replacements" {
						listed = strings.ReplaceAll(listed, `["spec","p1"]`, `["spec","p1","superseded"]`)
					}
					stubs["FindNodes"] = listed
					stubs["SearchReplaceInNodes"] = specReplaceResp(false)
					s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var q struct {
							OperationName, Query string
							Variables            json.RawMessage
						}
						_ = json.NewDecoder(r.Body).Decode(&q)
						w.Header().Set("Content-Type", "application/json")
						if q.OperationName == "NodeBatch" || q.OperationName == "NodeBatchContent" {
							if q.OperationName == "NodeBatchContent" {
								contents.Add(1)
								if strings.Contains(q.Query, "outgoingEdges") || strings.Contains(q.Query, "incomingEdges") {
									t.Error("edge-free document selects edges")
								}
							} else {
								batches.Add(1)
							}
							var v struct{ Refs []string }
							_ = json.Unmarshal(q.Variables, &v)
							if q.OperationName == "NodeBatch" && len(v.Refs) > cap {
								_, _ = w.Write([]byte(edgeCap1210))
								return
							}
							out := []string{}
							for _, id := range v.Refs {
								if n, ok := nodes[id]; ok {
									out = append(out, n)
								} else if id == "n1" {
									out = append(out, cleanSpecDetail)
								}
							}
							_, _ = w.Write([]byte(nodeBatchResult(out, "")))
							return
						}
						body, ok := stubs[q.OperationName]
						if !ok {
							body, ok = unstubbedDefault(q.OperationName)
						}
						if !ok {
							t.Errorf("unexpected %s", q.OperationName)
							body = `{"errors":[{"message":"unexpected"}]}`
						}
						_, _ = w.Write([]byte(translateFindNodes(q.OperationName, pageBeyondFirst(q.OperationName, q.Variables, body))))
					}))
					defer s.Close()
					f, out := testFactory(t)
					root := NewRootCmd(f)
					args := append([]string{}, tc.args...)
					files := map[string]string{}
					dir := t.TempDir()
					if tc.name == "memory export" {
						args = append(args, "--out", dir)
					}
					if asJSON {
						args = append(args, "--json")
					}
					root.SetArgs(append(args, "--server", s.URL))
					err := root.Execute()
					if tc.name == "memory export" {
						_ = filepath.Walk(dir, func(path string, info os.FileInfo, e error) error {
							if e == nil && !info.IsDir() {
								b, _ := os.ReadFile(path)
								rel, _ := filepath.Rel(dir, path)
								files[rel] = string(b)
							}
							return e
						})
					}
					// Export summaries name the requested output directory, not provider data.
					return strings.ReplaceAll(out.String(), dir, "<out>"), files, int(batches.Load()), int(contents.Load()), err
				}
				want, wfiles, normal, _, we := run(200)
				got, gfiles, split, content, ge := run(1)
				if exitCodeFor(we) != exitCodeFor(ge) || want != got || fmt.Sprint(sortedFiles1210(wfiles)) != fmt.Sprint(sortedFiles1210(gfiles)) {
					t.Fatalf("parity failed: normal=%v split=%v\nnormal %s\nsplit %s", we, ge, want, got)
				}
				if !tc.content && tc.name != "spec replace relint" {
					refused, files, _, _, err := run(0)
					if err == nil || refused != "" || len(files) != 0 || !strings.Contains(err.Error(), "hadron edge ls") {
						t.Fatalf("terminal refusal appeared complete: %v out=%s files=%v", err, refused, files)
					}
				}
				if tc.content {
					if content == 0 || split != 0 {
						t.Fatalf("content callers: edges=%d content=%d", split, content)
					}
				} else if normal == 0 || split <= normal {
					t.Fatalf("not exercised: normal=%d split=%d", normal, split)
				}
			})
		}
	}
}
func sortedFiles1210(m map[string]string) []string {
	out := []string{}
	for k, v := range m {
		out = append(out, k+":"+v)
	}
	sort.Strings(out)
	return out
}
