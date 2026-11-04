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

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
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
			// Answer on the door's own root field: a kind-changing flag can
			// route away from the stored kind's door (#1075).
			door := strings.TrimSuffix(q.OperationName, "ClearingRole")
			root := strings.ToLower(door[:1]) + door[1:]
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

// revision820Door is the operation a node update's mutation went out on and
// the guard it carried, decoded into the typed input rather than compared as
// raw JSON. An ordinary door nests the fields under `input`; a role-clearing
// door sends them as flat variables (#1075).
func revision820Door(t *testing.T, requests []revision820Request) (string, gen.UpdateNodeInput) {
	t.Helper()
	op := ""
	var input gen.UpdateNodeInput
	for _, q := range requests {
		if !strings.HasPrefix(q.OperationName, "Update") {
			continue
		}
		if op != "" {
			t.Fatalf("second mutation %s after %s", q.OperationName, op)
		}
		op = q.OperationName
		raw, ok := q.Variables["input"]
		if !ok {
			flat, err := json.Marshal(q.Variables)
			if err != nil {
				t.Fatal(err)
			}
			raw = flat
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatalf("%s variables: %v", op, err)
		}
	}
	if op == "" {
		t.Fatalf("no mutation in %v", requests)
	}
	return op, input
}

// The eight doors: generic/Task/Spec/Review, each ordinary and role-clearing.
func revision820Doors() []struct {
	kind  string
	clear bool
	door  string
} {
	doors := []struct {
		kind  string
		clear bool
		door  string
	}{}
	for _, kind := range []string{"", "Task", "Spec", "Review"} {
		for _, clear := range []bool{false, true} {
			door := "Update" + kind + "Node"
			if clear {
				door += "ClearingRole"
			}
			doors = append(doors, struct {
				kind  string
				clear bool
				door  string
			}{kind, clear, door})
		}
	}
	return doors
}

// #1075: a kind-changing flag (--role, --runnable, --type) picks the door, and
// the caller's guard must ride on that same operation, unchanged.
func TestNodeExpectedRevision1075KindFlags(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		flags []string
		door  string
		check func(gen.UpdateNodeInput) bool
	}{
		{"", []string{"--runnable"}, "UpdateTaskNode", func(in gen.UpdateNodeInput) bool { return in.IsRunnable != nil && *in.IsRunnable }},
		{"Task", []string{"--runnable=false"}, "UpdateTaskNode", func(in gen.UpdateNodeInput) bool { return in.IsRunnable != nil && !*in.IsRunnable }},
		{"", []string{"--role", "spec.rule"}, "UpdateSpecNode", func(in gen.UpdateNodeInput) bool { return in.Role != nil && *in.Role == "spec.rule" }},
		{"Spec", []string{"--role", "note"}, "UpdateSpecNode", func(in gen.UpdateNodeInput) bool { return in.Role != nil && *in.Role == "note" }},
		{"", []string{"--role", "review"}, "UpdateReviewNode", func(in gen.UpdateNodeInput) bool { return in.Role != nil && *in.Role == "review" }},
		{"Review", []string{"--role", "note"}, "UpdateReviewNode", func(in gen.UpdateNodeInput) bool { return in.Role != nil && *in.Role == "note" }},
		{"", []string{"--type", "task"}, "UpdateNode", func(in gen.UpdateNodeInput) bool { return in.NodeType != nil && *in.NodeType == "task" }},
		{"Task", []string{"--type", "note"}, "UpdateTaskNode", func(in gen.UpdateNodeInput) bool { return in.NodeType != nil && *in.NodeType == "note" }},
		{"", []string{"--runnable", "--role", ""}, "UpdateTaskNodeClearingRole", func(in gen.UpdateNodeInput) bool { return in.IsRunnable != nil && *in.IsRunnable && in.Role == nil }},
		{"Task", []string{"--runnable=false", "--role", ""}, "UpdateTaskNodeClearingRole", func(in gen.UpdateNodeInput) bool { return in.IsRunnable != nil && !*in.IsRunnable && in.Role == nil }},
	} {
		t.Run(tc.kind+"/"+strings.Join(tc.flags, "_"), func(t *testing.T) {
			args := append(append([]string{}, tc.flags...), "--expected-revision", "7", "--json")
			out, stderr, code, requests, _ := revision820Run(t, tc.kind, false, args...)
			if code != 0 {
				t.Fatalf("%d %s %s", code, out, stderr)
			}
			door, input := revision820Door(t, requests)
			if door != tc.door {
				t.Fatalf("door %s want %s", door, tc.door)
			}
			if input.ExpectedRevision == nil || *input.ExpectedRevision != 7 {
				t.Fatalf("guard on %s: %v", door, input.ExpectedRevision)
			}
			if !tc.check(input) {
				t.Fatalf("kind field not on the guarded write: %+v", input)
			}
		})
	}
}

// #1075: NODE_WRITE_CONFLICT on every door is exit 5 with currentRevision
// preserved, after exactly one mutation and no retry.
func TestNodeExpectedRevision820Conflict(t *testing.T) {
	for _, d := range revision820Doors() {
		for _, j := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json%v", d.door, j), func(t *testing.T) {
				args := []string{"--name", "new", "--expected-revision", "7"}
				if d.clear {
					args = append(args, "--role", "")
				}
				if j {
					args = append(args, "--json")
				}
				out, stderr, code, requests, _ := revision820Run(t, d.kind, true, args...)
				if code != 5 {
					t.Fatalf("%d %s %s", code, out, stderr)
				}
				door, input := revision820Door(t, requests)
				if door != d.door || input.ExpectedRevision == nil || *input.ExpectedRevision != 7 {
					t.Fatalf("door %s guard %v", door, input.ExpectedRevision)
				}
				if last := requests[len(requests)-1].OperationName; last != door {
					t.Fatalf("request after the refused write: %s", last)
				}
				if !j {
					if !strings.Contains(stderr, "(currentRevision: 9)") {
						t.Fatal(stderr)
					}
					return
				}
				var envelope struct {
					Error struct {
						Code       int            `json:"code"`
						Extensions map[string]any `json:"extensions"`
					} `json:"error"`
				}
				if err := json.Unmarshal([]byte(out), &envelope); err != nil {
					t.Fatalf("%v: %s", err, out)
				}
				ext := envelope.Error.Extensions
				if envelope.Error.Code != 5 || ext["code"] != "NODE_WRITE_CONFLICT" || ext["currentRevision"] != float64(9) {
					t.Fatalf("envelope %+v", envelope.Error)
				}
			})
		}
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
