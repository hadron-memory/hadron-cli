package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

var authoredCommands1685 = [][]string{{"node", "create"}, {"node", "update"}, {"node", "setproperties"}, {"node", "merge"}, {"replace", "text"}, {"spec", "new"}, {"spec", "edit"}, {"spec", "replace"}, {"spec", "supersede"}, {"spec", "extract"}}

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
