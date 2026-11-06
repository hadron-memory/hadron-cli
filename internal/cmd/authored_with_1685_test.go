package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

var authoredCommands1685 = [][]string{{"node", "create"}, {"node", "update"}, {"node", "set-properties"}, {"node", "merge"}, {"replace", "text"}, {"spec", "new"}, {"spec", "edit"}, {"spec", "replace"}, {"spec", "supersede"}, {"spec", "extract"}}

func TestAuthoredFlags1685(t *testing.T) {
	for _, path := range authoredCommands1685 {
		t.Run(strings.Join(path, "/"), func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, path...), "--help"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			for _, flag := range []string{"--authored-with", "--authored-revision"} {
				if !strings.Contains(out.String(), flag) {
					t.Errorf("missing %s", flag)
				}
			}
		})
	}
}

func TestAuthoredCreateWire1685(t *testing.T) {
	for _, claim := range []bool{false, true} {
		t.Run(fmt.Sprint(claim), func(t *testing.T) {
			var mu sync.Mutex
			var vars map[string]json.RawMessage
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls++
				var req struct {
					Variables struct {
						Input map[string]json.RawMessage `json:"input"`
					} `json:"variables"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				vars = req.Variables.Input
				fmt.Fprint(w, `{"errors":[{"message":"stop","extensions":{"code":"FORBIDDEN"}}]}`)
			}))
			defer srv.Close()
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"node", "create", "-m", "acme.com::kb", "--loc", "x", "--name", "X", "--server", srv.URL}
			if claim {
				args = append(args, "--authored-with", "task1", "--authored-revision", "4")
			}
			root.SetArgs(args)
			_ = root.Execute()
			mu.Lock()
			defer mu.Unlock()
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			v, ok := vars["authoredWith"]
			if claim {
				if string(v) != `{"task":"task1","revision":4}` && string(v) != `{"revision":4,"task":"task1"}` {
					t.Fatalf("claim=%s", v)
				}
			} else if ok {
				t.Fatalf("absent claim sent: %s", v)
			}
		})
	}
}

func TestAuthoredRefusal1685(t *testing.T) {
	for _, code := range []string{"AUTHORING_ATTESTATION_MISSING", "AUTHORING_ATTESTATION_WRONG_TASK", "AUTHORING_ATTESTATION_STALE", "AUTHOR_TASK_BROKEN"} {
		for _, access := range []string{"READABLE", "UNAVAILABLE"} {
			for _, jsonOut := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", code, access, jsonOut), func(t *testing.T) {
					body := fmt.Sprintf(`{"errors":[{"message":"server mentions SECRET_TASK","extensions":{"code":%q,"taskAccess":%q,"task":"SECRET_TASK","taskId":"SECRET_ID","taskRevision":4,"load":{"graphql":"SECRET_GRAPHQL","mcp":"SECRET_MCP","cli":"SECRET_CLI"}}}]}`, code, access)
					srv := fakeGraphQL(t, map[string]string{"CreateNode": body})
					f, out := testFactory(t)
					var stderr bytes.Buffer
					f.IOStreams.ErrOut = &stderr
					root := NewRootCmd(f)
					args := []string{"node", "create", "-m", "acme.com::kb", "--loc", "x", "--name", "X", "--server", srv.URL}
					if jsonOut {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					err := root.Execute()
					if err == nil {
						t.Fatal("expected refusal")
					}
					before := exitCodeFor(err)
					if got := renderFailure(f, args, err); got != before {
						t.Fatal("exit changed")
					}
					text := out.String() + stderr.String()
					if access == "UNAVAILABLE" {
						for _, secret := range []string{"SECRET_TASK", "SECRET_ID", "SECRET_GRAPHQL", "SECRET_MCP", "SECRET_CLI"} {
							if strings.Contains(text, secret) {
								t.Errorf("leaked %s: %s", secret, text)
							}
						}
						if jsonOut {
							var envelope map[string]map[string]json.RawMessage
							if json.Unmarshal([]byte(out.String()), &envelope) != nil {
								t.Fatal(out.String())
							}
							var details map[string]json.RawMessage
							_ = json.Unmarshal(envelope["error"]["extensions"], &details)
							for _, key := range []string{"task", "taskId", "taskRevision", "load"} {
								if _, exists := details[key]; exists {
									t.Errorf("concealed key emitted: %s", key)
								}
							}
						}
						if !strings.Contains(text, "owner") {
							t.Error("missing owner remedy")
						}
					} else {
						for _, want := range []string{"SECRET_TASK", "SECRET_CLI", "4"} {
							if !strings.Contains(text, want) {
								t.Errorf("missing %s: %s", want, text)
							}
						}
						if jsonOut {
							for _, want := range []string{"taskAccess", "taskId", "taskRevision", "graphql", "mcp", "cli"} {
								if !strings.Contains(text, want) {
									t.Errorf("missing %s", want)
								}
							}
						} else if !strings.Contains(text, "Retry with:") {
							t.Errorf("missing retry: %s", text)
						}
					}
				})
			}
		}
	}
}

// auditAuthored1685 observes every request without replacing the command's real
// read/write flow. The original fake's strict operation routing stays in place.
func auditAuthored1685(t *testing.T, inner *httptest.Server, claim bool) (*httptest.Server, func(...string)) {
	t.Helper()
	var mu sync.Mutex
	type call struct {
		OperationName string                     `json:"operationName"`
		Variables     map[string]json.RawMessage `json:"variables"`
	}
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var c call
		_ = json.Unmarshal(body, &c)
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		inner.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, func(ops ...string) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		seen := map[string]int{}
		for _, c := range calls {
			match := false
			for _, op := range ops {
				if op == c.OperationName {
					match = true
				}
			}
			if !match {
				continue
			}
			seen[c.OperationName]++
			v := c.Variables
			if in, ok := v["input"]; ok {
				if err := json.Unmarshal(in, &v); err != nil {
					t.Fatal(err)
				}
			}
			raw, ok := v["authoredWith"]
			if claim {
				var got struct {
					Task     string `json:"task"`
					Revision int    `json:"revision"`
				}
				if json.Unmarshal(raw, &got) != nil || got.Task != "task1" || got.Revision != 4 {
					t.Errorf("%s claim=%s", c.OperationName, raw)
				}
			} else if ok {
				t.Errorf("%s absent claim sent %s", c.OperationName, raw)
			}
		}
		for _, op := range ops {
			if seen[op] == 0 {
				t.Errorf("did not exercise %s; calls=%v", op, calls)
			}
		}
	}
}

func TestAuthoredCommandFlows1685(t *testing.T) {
	for _, claim := range []bool{false, true} {
		run := func(t *testing.T, inner *httptest.Server, args []string, stdin string, ops ...string) {
			t.Helper()
			srv, check := auditAuthored1685(t, inner, claim)
			f, _ := testFactory(t)
			f.IOStreams.In = strings.NewReader(stdin)
			root := NewRootCmd(f)
			args = append(args, "--server", srv.URL)
			if claim {
				args = append(args, "--authored-with", "task1", "--authored-revision", "4")
			}
			root.SetArgs(args)
			err := root.Execute()
			check(ops...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
		}
		t.Run(fmt.Sprintf("%t/edit", claim), func(t *testing.T) {
			inner, _ := queueGraphQL(t, guardMocks(guardWriteOK))
			run(t, inner, []string{"spec", "edit", "msg:010:02", "-m", specMem, "--content", "-"}, "# new body\n", "UpdateSpecNode")
		})
		t.Run(fmt.Sprintf("%t/extract", claim), func(t *testing.T) {
			inner, _ := captureGraphQL(t, extractMocks())
			run(t, inner, []string{"spec", "extract", "cor:dmo:060:02", "-m", specMem, "--to-feature", "020", "--title", "Node type", "--content", "-", "--strip-source"}, extractChunk, "CreateSpecNode", "UpdateSpecNode")
		})
		t.Run(fmt.Sprintf("%t/new-path", claim), func(t *testing.T) {
			inner := fakeGraphQL(t, map[string]string{"FindNodes": `{"data":{"nodes":[]}}`, "CreateSpecNode": `{"data":{"createSpecNode":{"id":"n1","memoryId":"mem1","loc":"x","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`})
			run(t, inner, []string{"spec", "new", "msg:010:01", "--new-path", "--title", "Send", "--no-edges", "-m", specMem}, "", "CreateSpecNode")
		})
		t.Run(fmt.Sprintf("%t/new-contract", claim), func(t *testing.T) {
			inner := fakeGraphQL(t, map[string]string{"FindNodes": `{"data":{"nodes":[` + specNodeList("msg", `["spec"]`) + `]}}`, "CreateSpecNode": `{"data":{"createSpecNode":{"id":"n1","memoryId":"mem1","loc":"x","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`})
			run(t, inner, []string{"spec", "new", "--new-module", "--module", "brd", "--title", "Send", "--no-edges", "-m", specMem}, "", "CreateSpecNode")
		})

		t.Run(fmt.Sprintf("%t/supersede", claim), func(t *testing.T) {
			inner, _ := captureSupersedeGraphQL(t, "msg:010:03", map[string]string{
				"ResolveUrn":     resolveSpecJSON,
				"GetNode":        `{"data":{"node":` + cleanSpecDetail + `}}`,
				"GetSpecNodeRaw": `{"data":{"node":` + cleanSpecDetail + `}}`,
				"NodeBatch":      specLintRawBodyStub(cleanSpecDetail),
				"FindNodes":      `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:00", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
				"CreateSpecNode": `{"data":{"createSpecNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:03","name":"msg:010:03 — W2 v2","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
				"UpdateSpecNode": `{"data":{"updateSpecNode":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2","nodeType":"info","tags":["spec","p1","superseded"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
				"CreateEdge":     `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"sp1","loc":"msg:010:02"},"target":{"id":"new1","loc":"msg:010:03"}}}}`,
			})
			run(t, inner, []string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--yes"}, "", "CreateSpecNode", "UpdateSpecNode")
		})
		t.Run(fmt.Sprintf("%t/replace", claim), func(t *testing.T) {
			inner, _ := queueGraphQL(t, map[string][]string{"SearchReplaceInNodes": {searchReplaceDryJSON, searchReplaceRealJSON}})
			run(t, inner, []string{"replace", "text", "cat", "dog", "--field", "content", "-m", "acme.com::kb", "--yes"}, "", "SearchReplaceInNodes")
		})
		for _, draft := range []bool{false, true} {
			t.Run(fmt.Sprintf("%t/spec-replace/%t", claim, draft), func(t *testing.T) {
				state, op := mintedStateJSON, "SearchReplaceInNodes"
				answers := []string{searchReplaceDryJSON, searchReplaceRealJSON}
				if draft {
					state, op = draftStateJSON, "SearchReplaceInSpecNodes"
					answers = []string{draftReplaceResponse(true, "plan-one", 1), draftReplaceResponse(false, "plan-one", 1)}
				}
				inner, _ := queueGraphQL(t, map[string][]string{"SpecCorpusState": {state}, "FindNodes": {`{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec"]`) + `]}}`, `{"data":{"nodes":[]}}`}, op: answers, "NodeBatch": {specBatchResp("msg:010:02")}})
				run(t, inner, []string{"spec", "replace", "cat", "dog", "-m", specMem, "--yes"}, "", op)
			})
		}
		t.Run(fmt.Sprintf("%t/new-at", claim), func(t *testing.T) {
			inner := fakeGraphQL(t, map[string]string{"ResolveUrn": `{"data":{"resolveUrn":null}}`, "CreateSpecNode": `{"data":{"createSpecNode":{"id":"n1","memoryId":"mem1","loc":"custom:rule","name":"X","nodeType":"info","tags":[],"updatedAt":"2026-06-14T00:00:00Z"}}}`})
			run(t, inner, []string{"spec", "new", "custom:rule", "--title", "X", "--no-edges", "-m", specMem}, "", "CreateSpecNode")
		})
		for _, c := range []struct {
			name string
			args []string
			op   string
		}{
			{"update", []string{"node", "update", "hrn:node:acme.com:kb:x", "--name", "X"}, "UpdateNode"},
			{"data-merge", []string{"node", "update", "hrn:node:acme.com:kb:x", "--data-merge", `{"x":1}`}, "UpdateNodeData"},
			{"data-replace", []string{"node", "update", "hrn:node:acme.com:kb:x", "--data", `{"x":1}`}, "UpdateNode"},
			{"properties", []string{"node", "set-properties", "hrn:node:acme.com:kb:x", "--patch", `{"x":1}`}, "UpdateNodeProperties"},
			{"merge", []string{"node", "merge", "hrn:node:acme.com:kb:x", "--into", "hrn:node:acme.com:kb:y", "--yes"}, "MergeNodes"},
		} {
			t.Run(fmt.Sprintf("%t/%s", claim, c.name), func(t *testing.T) {
				baseOp := c.op
				if claim && (c.op == "UpdateNodeData" || c.op == "UpdateNodeProperties") {
					c.op += "Authored"
				}
				node := `{"id":"n1","memoryId":"mem1","loc":"x","name":"X","nodeType":"info","tags":[],"updatedAt":"2026-06-14T00:00:00Z"}`
				field := map[string]string{"UpdateNode": "updateNode", "UpdateNodeData": "updateNodeData", "UpdateNodeProperties": "updateNodeProperties", "MergeNodes": "mergeNodes"}[baseOp]
				answer := `{"data":{"` + field + `":` + node + `}}`
				if c.op == "MergeNodes" {
					answer = `{"data":{"mergeNodes":{"node":` + node + `,"mergedNodeId":"n2"}}}`
				}
				inner := fakeGraphQL(t, map[string]string{"ResolveUrn": `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`, "GetNode": `{"data":{"node":` + node + `}}`, c.op: answer})
				run(t, inner, c.args, "", c.op)
			})
		}
	}
}

func TestAuthoredPairBeforeAnyRequest1685(t *testing.T) {
	for _, c := range []struct{ args []string }{
		{[]string{"node", "create", "-m", "acme.com::kb", "--loc", "x", "--name", "X"}},
		{[]string{"node", "update", "hrn:node:acme.com:kb:x", "--name", "X"}},
		{[]string{"node", "set-properties", "hrn:node:acme.com:kb:x", "--patch", "{}"}},
		{[]string{"node", "merge", "hrn:node:acme.com:kb:x", "--into", "hrn:node:acme.com:kb:y", "--yes"}},
		{[]string{"replace", "text", "a", "b", "-m", "acme.com::kb", "--yes"}},
		{[]string{"spec", "new", "--module", "msg", "--title", "X", "-m", specMem}},
		{[]string{"spec", "edit", "msg:010:02", "--content", "X", "-m", specMem}},
		{[]string{"spec", "replace", "a", "b", "-m", specMem, "--yes"}},
		{[]string{"spec", "supersede", "msg:010:02", "--title", "X", "--yes", "-m", specMem}},
		{[]string{"spec", "extract", "msg:010:02", "--to-feature", "020", "--title", "X", "-m", specMem}},
	} {
		for _, flags := range [][]string{{"--authored-with", "task1"}, {"--authored-revision", "4"}} {
			t.Run(strings.Join(append(c.args[:2:2], flags[0]), "/"), func(t *testing.T) {
				var n atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1); http.Error(w, "unexpected", 500) }))
				defer srv.Close()
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := append(append([]string{}, c.args...), flags...)
				args = append(args, "--server", srv.URL)
				root.SetArgs(args)
				err := root.Execute()
				if exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "both --authored-with") {
					t.Fatalf("bad pair refusal: %v", err)
				}
				if n.Load() != 0 {
					t.Fatal("request before validation")
				}
			})
		}
	}
}

func TestAuthoredFlaglessRefusal1685(t *testing.T) {
	for _, code := range []string{"AUTHORING_ATTESTATION_MISSING", "AUTHORING_ATTESTATION_WRONG_TASK", "AUTHORING_ATTESTATION_STALE", "AUTHOR_TASK_BROKEN"} {
		for _, access := range []string{"READABLE", "UNAVAILABLE"} {
			for _, j := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", code, access, j), func(t *testing.T) {
					body := fmt.Sprintf(`{"errors":[{"message":"SECRET_TASK","extensions":{"code":%q,"taskAccess":%q,"task":"SECRET_TASK","taskId":"SECRET_ID","taskRevision":4,"load":{"graphql":"SECRET_GRAPHQL","mcp":"SECRET_MCP","cli":"SECRET_CLI"}}}]}`, code, access)
					srv := fakeGraphQL(t, map[string]string{"CreateObject": body})
					f, out := testFactory(t)
					var stderr bytes.Buffer
					f.IOStreams.ErrOut = &stderr
					root := NewRootCmd(f)
					args := []string{"object", "create", "-m", "acme.com::kb", "--type", "thing", "--fields", `{"name":"X"}`, "--server", srv.URL}
					if j {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					err := root.Execute()
					if err == nil {
						t.Fatal("expected refusal")
					}
					renderFailure(f, args, err)
					text := out.String() + stderr.String()
					if strings.Contains(text, "Retry with:") {
						t.Fatalf("unsupported retry: %s", text)
					}
					if access == "READABLE" {
						for _, v := range []string{"SECRET_TASK", "SECRET_CLI"} {
							if !strings.Contains(text, v) {
								t.Errorf("missing %s: %s", v, text)
							}
						}
					} else if strings.Contains(text, "SECRET_") {
						t.Fatalf("leak: %s", text)
					}
				})
			}
		}
	}
}

func TestAuthoredUnmappedRefusalNoLeak1685(t *testing.T) {
	f, out := testFactory(t)
	var stderr bytes.Buffer
	f.IOStreams.ErrOut = &stderr
	err := &gqlerror.Error{Message: "SECRET_TASK", Extensions: map[string]any{"code": "AUTHOR_TASK_BROKEN", "taskAccess": "UNAVAILABLE", "task": "SECRET_TASK", "taskId": "SECRET_ID", "taskRevision": 4, "load": map[string]any{"cli": "SECRET_CLI"}}}
	renderFailure(f, []string{"--json"}, err)
	if text := out.String() + stderr.String(); strings.Contains(text, "SECRET_") || !strings.Contains(text, "owner") {
		t.Fatalf("unmapped refusal leaked: %s", text)
	}
	// Partial-write callers can format MapError without passing through root.
	if msg := api.MapError(err).Error(); strings.Contains(msg, "SECRET_") || !strings.Contains(msg, "owner") {
		t.Fatalf("mapped warning leaked: %s", msg)
	}
}

func TestAuthoredOtherCodeUnchanged1685(t *testing.T) {
	srv := fakeGraphQL(t, map[string]string{"CreateNode": `{"errors":[{"message":"same refusal","extensions":{"code":"FORBIDDEN","taskAccess":"UNAVAILABLE","task":"SHOULD_NOT_CURATE"}}]}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"node", "create", "-m", "acme.com::kb", "--loc", "x", "--name", "X", "--server", srv.URL, "--json"}
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		t.Fatal("expected refusal")
	}
	code := renderFailure(f, args, err)
	var got struct {
		Error map[string]any `json:"error"`
	}
	if json.Unmarshal([]byte(out.String()), &got) != nil {
		t.Fatal(out.String())
	}
	if got.Error["message"] != "same refusal" || got.Error["extensions"] != nil || got.Error["code"] != float64(code) {
		t.Fatalf("unrelated refusal changed: %s", out.String())
	}
}

func TestAuthoredPinnedContract1685(t *testing.T) {
	b, err := os.ReadFile("testdata/attestation_refusals_1685.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ServerSHA string           `json:"serverSHA"`
		Cases     []map[string]any `json:"cases"`
	}
	if json.Unmarshal(b, &fixture) != nil || fixture.ServerSHA != "07d96c78eb29eb6e3e36b2ea2ac9e6f165214dbf" {
		t.Fatal("fixture provenance")
	}
	for i, row := range fixture.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": "attestation refused", "extensions": row}}})
			srv := fakeGraphQL(t, map[string]string{"CreateNode": string(body)})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"node", "create", "-m", "acme.com::kb", "--loc", "x", "--name", "X", "--server", srv.URL, "--json"}
			root.SetArgs(args)
			err := root.Execute()
			if err == nil {
				t.Fatal("expected refusal")
			}
			renderFailure(f, args, err)
			if row["taskRevision"] == float64(1000000000) && !strings.Contains(out.String(), "--authored-revision 1000000000") {
				t.Fatal("revision hint used scientific notation: " + out.String())
			}
			var envelope struct {
				Error struct {
					Extensions map[string]any `json:"extensions"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(out.String()), &envelope) != nil {
				t.Fatal(out.String())
			}
			d := envelope.Error.Extensions
			if row["taskAccess"] == "UNAVAILABLE" {
				for _, k := range []string{"task", "taskId", "taskRevision", "load"} {
					if _, ok := d[k]; ok {
						t.Errorf("concealed key emitted %s", k)
					}
				}
			} else {
				for _, k := range []string{"taskAccess", "task", "taskId", "taskRevision", "load"} {
					if !reflect.DeepEqual(row[k], d[k]) {
						t.Errorf("%s: want%v got%v", k, row[k], d[k])
					}
				}
			}
		})
	}
}

func TestAuthoredExtractWarning1685(t *testing.T) {
	for _, access := range []string{"READABLE", "UNAVAILABLE"} {
		t.Run(access, func(t *testing.T) {
			mocks := extractMocks()
			mocks["UpdateSpecNode"] = fmt.Sprintf(`{"errors":[{"message":"SECRET_TASK","extensions":{"code":"AUTHOR_TASK_BROKEN","taskAccess":%q,"task":"SECRET_TASK","taskId":"SECRET_ID","taskRevision":4,"load":{"graphql":"SECRET_GRAPHQL","mcp":"SECRET_MCP","cli":"SECRET_CLI"}}}]}`, access)
			srv := fakeGraphQL(t, mocks)
			f, out := testFactory(t)
			var stderr bytes.Buffer
			f.IOStreams.ErrOut = &stderr
			f.IOStreams.In = strings.NewReader(extractChunk)
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "extract", "cor:dmo:060:02", "-m", specMem, "--to-feature", "020", "--title", "Node type", "--content", "-", "--strip-source", "--authored-with", "task1", "--authored-revision", "4", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("best-effort trim changed exit: %v", err)
			}
			text := stderr.String()
			if access == "READABLE" {
				for _, want := range []string{"SECRET_TASK", "SECRET_CLI", "Retry with:"} {
					if !strings.Contains(text, want) {
						t.Errorf("missing recovery %s: %s", want, text)
					}
				}
			} else if strings.Contains(text, "Required task:") || strings.Contains(text+out.String(), "SECRET_") {
				t.Fatal("partial warning leaked")
			}
		})
	}
}

func TestAuthoredSupersedePartialRefusal1685(t *testing.T) {
	for _, access := range []string{"READABLE", "UNAVAILABLE"} {
		for _, j := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", access, j), func(t *testing.T) {
				refusal := fmt.Sprintf(`{"errors":[{"message":"SECRET_TASK","extensions":{"code":"AUTHOR_TASK_BROKEN","taskAccess":%q,"task":"SECRET_TASK","taskId":"SECRET_ID","taskRevision":4,"load":{"graphql":"SECRET_GRAPHQL","mcp":"SECRET_MCP","cli":"SECRET_CLI"}}}]}`, access)
				inner, _ := captureSupersedeGraphQL(t, "msg:010:03", map[string]string{
					"ResolveUrn":     resolveSpecJSON,
					"GetNode":        `{"data":{"node":` + cleanSpecDetail + `}}`,
					"GetSpecNodeRaw": `{"data":{"node":` + cleanSpecDetail + `}}`,
					"NodeBatch":      specLintRawBodyStub(cleanSpecDetail),
					"FindNodes":      `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:00", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
					"CreateSpecNode": `{"data":{"createSpecNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:03","name":"msg:010:03 — W2 v2","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
					"UpdateSpecNode": refusal,
					"CreateEdge":     `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"sp1","loc":"msg:010:02"},"target":{"id":"new1","loc":"msg:010:03"}}}}`,
				})
				f, out := testFactory(t)
				var stderr bytes.Buffer
				f.IOStreams.ErrOut = &stderr
				root := NewRootCmd(f)
				args := []string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--yes", "--authored-with", "task1", "--authored-revision", "4", "--server", inner.URL}
				if j {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				err := root.Execute()
				if err == nil {
					t.Fatal("expected retirement refusal")
				}
				if exitCodeFor(err) != 1 {
					t.Fatalf("partial exit changed: %v", err)
				}
				if !strings.Contains(err.Error(), "linked") {
					t.Fatalf("partial status lost: %v", err)
				}
				renderFailure(f, args, err)
				text := out.String() + stderr.String()
				if access == "READABLE" {
					for _, want := range []string{"SECRET_TASK", "SECRET_CLI", "Retry with:"} {
						if !strings.Contains(text, want) {
							t.Errorf("missing recovery %s: %s", want, text)
						}
					}
				} else if strings.Contains(text, "SECRET_") {
					t.Fatalf("partial refusal leaked: %s", text)
				}
			})
		}
	}
}

func TestAuthoredBlankTaskBeforeRequest1685(t *testing.T) {
	for _, task := range []string{"", "  "} {
		t.Run(fmt.Sprintf("blank-%d", len(task)), func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1); http.Error(w, "unexpected", 500) }))
			defer srv.Close()
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "create", "-m", "acme.com::kb", "--loc", "x", "--name", "X", "--authored-with", task, "--authored-revision", "4", "--server", srv.URL})
			err := root.Execute()
			if exitCodeFor(err) != 2 || n.Load() != 0 {
				t.Fatalf("blank task requested: %v calls%d", err, n.Load())
			}
		})
	}
}
