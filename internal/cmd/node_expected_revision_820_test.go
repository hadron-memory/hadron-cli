package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type revision820Request struct {
	OperationName string
	Variables     map[string]json.RawMessage
}
type revision820NoRead struct{ reads int }

func (r *revision820NoRead) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }

func revision820Run(t *testing.T, kind string, conflict bool, args ...string) (string, string, int, []revision820Request, int) {
	t.Helper()
	var node map[string]any
	if err := json.Unmarshal([]byte(nodeDetailJSON), &node); err != nil {
		t.Fatal(err)
	}
	node["revision"] = 99
	node["role"] = nil
	node["isRunnable"] = false
	switch kind {
	case "Task":
		node["isRunnable"] = true
	case "Spec":
		node["role"] = "spec.rule"
	case "Review":
		node["role"] = "review"
	}
	raw, _ := json.Marshal(node)
	var mu sync.Mutex
	requests := []revision820Request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q revision820Request
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests = append(requests, q)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch q.OperationName {
		case "ResolveUrn":
			fmt.Fprint(w, resolveNodeJSON)
		case "GetNode", "GetNodeRaw":
			fmt.Fprintf(w, `{"data":{"node":%s}}`, raw)
		default:
			if !strings.HasPrefix(q.OperationName, "Update") {
				t.Errorf("unexpected %s", q.OperationName)
				return
			}
			if conflict {
				fmt.Fprint(w, `{"errors":[{"message":"revision changed","extensions":{"code":"NODE_WRITE_CONFLICT","currentRevision":9}}]}`)
				return
			}
			root := "update" + kind + "Node"
			if kind == "" {
				root = "updateNode"
			}
			fmt.Fprintf(w, `{"data":{%q:%s}}`, root, raw)
		}
	}))
	defer srv.Close()
	f, out := testFactory(t)
	stderr := &strings.Builder{}
	f.IOStreams.ErrOut = stderr
	reader := &revision820NoRead{}
	f.IOStreams.In = reader
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"node", "update", nodeURN, "--server", srv.URL}, args...))
	err := root.Execute()
	code := 0
	if err != nil {
		code = renderError(f, err)
	}
	mu.Lock()
	result := append([]revision820Request(nil), requests...)
	mu.Unlock()
	return out.String(), stderr.String(), code, result, reader.reads
}
func TestNodeExpectedRevision820Doors(t *testing.T) {
	for _, kind := range []string{"", "Task", "Spec", "Review"} {
		for _, clear := range []bool{false, true} {
			for _, rev := range []string{"", "7", "2147483647"} {
				t.Run(fmt.Sprintf("%s/clear%v/rev%s", kind, clear, rev), func(t *testing.T) {
					args := []string{"--name", "new", "--json"}
					if clear {
						args = append(args, "--role", "")
					}
					if rev != "" {
						args = append(args, "--expected-revision", rev)
					}
					out, stderr, code, requests, _ := revision820Run(t, kind, false, args...)
					if code != 0 {
						t.Fatalf("%d %s %s", code, out, stderr)
					}
					mutations := 0
					for _, q := range requests {
						if strings.HasPrefix(q.OperationName, "Update") {
							mutations++
							want := "Update" + kind + "Node"
							if kind == "" {
								want = "UpdateNode"
							}
							if clear {
								want += "ClearingRole"
							}
							if q.OperationName != want {
								t.Fatalf("door %s want%s", q.OperationName, want)
							}
							vars := q.Variables
							if !clear {
								if err := json.Unmarshal(vars["input"], &vars); err != nil {
									t.Fatal(err)
								}
							}
							got, ok := vars["expectedRevision"]
							if rev == "" {
								if ok {
									t.Fatalf("omitted guard serialized: %s", got)
								}
							} else if !ok || string(got) != rev {
								t.Fatalf("expected%s got%s", rev, got)
							}
						}
					}
					if mutations != 1 {
						t.Fatalf("mutations%d requests%v", mutations, requests)
					}
				})
			}
		}
	}
}
func TestNodeExpectedRevision820EarlyRefusals(t *testing.T) {
	cases := [][]string{}
	for _, v := range []string{"0", "-1", "2147483648", "1.5", "abc"} {
		cases = append(cases, []string{"--name", "new", "--expected-revision", v})
	}
	cases = append(cases, []string{"--expected-revision", "3"})
	for _, merge := range [][]string{{"--data-merge", "-"}, {"--data-merge-file", "/not-a-readable-fixture"}, {"--data-merge", "-", "--name", "new"}, {"--content", "-", "--data-merge", "{}"}} {
		cases = append(cases, append(merge, "--expected-revision", "7"))
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			_, stderr, code, requests, reads := revision820Run(t, "", false, args...)
			if code != 2 || len(requests) != 0 || reads != 0 {
				t.Fatalf("exit%d requests%d stdin%d: %s", code, len(requests), reads, stderr)
			}
			if strings.Contains(strings.Join(args, " "), "--data-merge") && !strings.Contains(stderr, "cannot guard") {
				t.Fatalf("merge failed for wrong reason: %s", stderr)
			}
			if len(args) == 2 && !strings.Contains(stderr, "nothing to update") {
				t.Fatal(stderr)
			}
		})
	}
}
func TestNodeExpectedRevision820DryRun(t *testing.T) {
	for _, j := range []bool{false, true} {
		t.Run(fmt.Sprint(j), func(t *testing.T) {
			args := []string{"--name", "new", "--expected-revision", "7", "--dry-run"}
			if j {
				args = append(args, "--json")
			}
			out, stderr, code, requests, _ := revision820Run(t, "", false, args...)
			if code != 0 {
				t.Fatalf("%d %s %s", code, out, stderr)
			}
			for _, q := range requests {
				if strings.HasPrefix(q.OperationName, "Update") {
					t.Fatal("dry-run wrote", q)
				}
			}
			if !strings.Contains(out, "not validated") {
				t.Fatalf("CAS claim not advisory: %s", out)
			}
			if j {
				var d map[string]any
				if err := json.Unmarshal([]byte(out), &d); err != nil {
					t.Fatal(err)
				}
				if d["expectedRevision"] != float64(7) {
					t.Fatal(d)
				}
			}
		})
	}
}
func TestNodeExpectedRevision820Conflict(t *testing.T) {
	for _, j := range []bool{false, true} {
		t.Run(fmt.Sprint(j), func(t *testing.T) {
			args := []string{"--name", "new", "--expected-revision", "7"}
			if j {
				args = append(args, "--json")
			}
			out, stderr, code, requests, _ := revision820Run(t, "", true, args...)
			if code != 5 {
				t.Fatalf("%d %s %s", code, out, stderr)
			}
			mutations := 0
			for _, q := range requests {
				if strings.HasPrefix(q.OperationName, "Update") {
					mutations++
				}
			}
			if mutations != 1 || len(requests) != 3 {
				t.Fatalf("retry or extra request: %v", requests)
			}
			if j {
				if !strings.Contains(out, `"currentRevision": 9`) {
					t.Fatal(out)
				}
			} else if !strings.Contains(stderr, "currentRevision: 9") {
				t.Fatal(stderr)
			}
		})
	}
}

func TestNodeExpectedRevision820Replacement(t *testing.T) {
	out, stderr, code, requests, _ := revision820Run(t, "", false, "--data", `{"x":1}`, "--expected-revision", "7", "--json")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	mutations := 0
	for _, q := range requests {
		if strings.HasPrefix(q.OperationName, "Update") {
			mutations++
			if q.OperationName != "UpdateNode" {
				t.Fatal(q.OperationName)
			}
			var input map[string]json.RawMessage
			if err := json.Unmarshal(q.Variables["input"], &input); err != nil {
				t.Fatal(err)
			}
			if string(input["expectedRevision"]) != "7" || string(input["data"]) != `{"x":1}` {
				t.Fatal(input)
			}
		}
	}
	if mutations != 1 {
		t.Fatal(mutations)
	}
}
