package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const renderNodeRef1010 = "hrn:node:acme.com:kb:tasks:brief"

// The fixture accepts only the Query: any lookup, legacy renderer or launch is
// an error. Counting requests also detects fallback/retry on refused answers.
func renderFixture1010(t *testing.T, reply string) (*httptest.Server, *[]map[string]json.RawMessage) {
	t.Helper()
	requests := []map[string]json.RawMessage{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request: %v", err)
		}
		requests = append(requests, body)
		var op, query string
		_ = json.Unmarshal(body["operationName"], &op)
		_ = json.Unmarshal(body["query"], &query)
		if op != "RenderRunNode" || !strings.Contains(query, "query RenderRunNode") || !strings.Contains(query, "renderRunNode(") || strings.Contains(query, "mutation") {
			t.Errorf("unexpected document: %s", query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, reply)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func TestTaskRenderBytesAndArguments1010(t *testing.T) {
	for _, rendered := range []string{"", "no newline", "  first\nsecond\n\n"} {
		for _, asJSON := range []bool{false, true} {
			for _, arg := range []string{"omitted", "{}", `{"n":7,"ok":true,"list":[1,null],"nested":{"s":"x"}}`, "file"} {
				t.Run(fmt.Sprintf("%q/json=%t/args=%s", rendered, asJSON, arg), func(t *testing.T) {
					b, _ := json.Marshal(map[string]any{"data": map[string]string{"renderRunNode": rendered}})
					server, requests := renderFixture1010(t, string(b))
					f, out := testFactory(t)
					args := []string{"task", "render", renderNodeRef1010, "--server", server.URL, "--app", "ambient-app"}
					expected := arg
					if arg == "file" {
						expected = `{"count":9007199254740993,"null":null}`
						p := filepath.Join(t.TempDir(), "args.json")
						if err := os.WriteFile(p, []byte(expected), 0600); err != nil {
							t.Fatal(err)
						}
						args = append(args, "--args", "@"+p)
					} else if arg != "omitted" {
						args = append(args, "--args", arg)
					}
					if asJSON {
						args = append(args, "--json")
					}
					root := NewRootCmd(f)
					root.SetArgs(args)
					if err := root.Execute(); err != nil {
						t.Fatal(err)
					}
					if len(*requests) != 1 {
						t.Fatalf("requests=%d", len(*requests))
					}
					var vars map[string]json.RawMessage
					_ = json.Unmarshal((*requests)[0]["variables"], &vars)
					var ref string
					_ = json.Unmarshal(vars["nodeRef"], &ref)
					if ref != renderNodeRef1010 {
						t.Fatalf("ref=%q", ref)
					}
					if arg == "omitted" {
						if _, ok := vars["args"]; ok {
							t.Fatalf("omitted args sent: %s", vars["args"])
						}
					} else {
						var want, got bytes.Buffer
						if err := json.Compact(&want, []byte(expected)); err != nil {
							t.Fatal(err)
						}
						if err := json.Compact(&got, vars["args"]); err != nil {
							t.Fatal(err)
						}
						if want.String() != got.String() {
							t.Fatalf("args=%s want %s", &got, &want)
						}
					}
					if len(vars) > 2 {
						t.Fatalf("unexpected variables: %v", vars)
					}
					if asJSON {
						var dto map[string]json.RawMessage
						if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
							t.Fatal(err)
						}
						var got string
						_ = json.Unmarshal(dto["rendered"], &got)
						if len(dto) != 1 || got != rendered {
							t.Fatalf("json=%s", out)
						}
					} else if out.String() != rendered {
						t.Fatalf("raw=%q want %q", out.String(), rendered)
					}
				})
			}
		}
	}
}

func TestTaskRenderInvalidArgumentsBeforeRequests1010(t *testing.T) {
	for _, arg := range []string{"", " ", "{", "[]", "null", "1", "true", `"text"`, "{} {}", "@", "@/missing/render-args-1010"} {
		t.Run(arg, func(t *testing.T) {
			server, requests := renderFixture1010(t, `{"data":{"renderRunNode":"wrong"}}`)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"task", "render", renderNodeRef1010, "--args", arg, "--server", server.URL})
			err := root.Execute()
			if exitCodeFor(err) != exitcode.Usage || len(*requests) != 0 {
				t.Fatalf("err=%v requests=%d", err, len(*requests))
			}
		})
	}
}

func TestTaskRenderReferenceForms1010(t *testing.T) {
	for _, tc := range []struct{ ref, memory, want string }{
		{renderNodeRef1010, "", renderNodeRef1010}, {"0123456789abcdef0123456789abcdef", "", "0123456789abcdef0123456789abcdef"},
		{"0123456789abcdef0123456789abcdef", "acme.com:kb", "0123456789abcdef0123456789abcdef"},
		{"tasks:brief", "acme.com::kb", "hrn:node:acme.com:kb:tasks:brief"},
		{"acme.com::kb::tasks:brief", "", "hrn:node:acme.com::kb::tasks:brief"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			server, requests := renderFixture1010(t, `{"data":{"renderRunNode":"ok"}}`)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"task", "render", tc.ref, "--server", server.URL}
			if tc.memory != "" {
				args = append(args, "-m", tc.memory)
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if len(*requests) != 1 {
				t.Fatalf("requests=%d", len(*requests))
			}
			var vars map[string]any
			_ = json.Unmarshal((*requests)[0]["variables"], &vars)
			if vars["nodeRef"] != tc.want {
				t.Fatalf("variables=%v", vars)
			}
		})
	}
}

func TestTaskRenderRefusalsNeverFallback1010(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"NODE_NOT_FOUND", 4}, {"FORBIDDEN", 8}, {"BAD_USER_INPUT", 2}, {"INTERNAL_SERVER_ERROR", 1}, {"GRAPHQL_VALIDATION_FAILED", 2}} {
		t.Run(tc.code, func(t *testing.T) {
			server, requests := renderFixture1010(t, fmt.Sprintf(`{"errors":[{"message":"refused","extensions":{"code":%q,"requestId":"req-render"}}]}`, tc.code))
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"task", "render", renderNodeRef1010, "--server", server.URL, "--json"})
			err := root.Execute()
			if exitCodeFor(err) != tc.exit || len(*requests) != 1 || strings.Contains(out.String(), `"rendered"`) {
				t.Fatalf("err=%v requests=%d output=%s", err, len(*requests), out)
			}
			if got := renderError(f, err); got != tc.exit {
				t.Fatalf("rendered exit=%d", got)
			}
			var envelope struct {
				Error struct {
					Code       int    `json:"code"`
					ServerCode string `json:"serverCode"`
					RequestID  string `json:"requestId"`
				} `json:"error"`
			}
			if e := json.Unmarshal([]byte(out.String()), &envelope); e != nil || envelope.Error.Code != tc.exit {
				t.Fatalf("envelope=%s err=%v", out, e)
			}
			if tc.code == "INTERNAL_SERVER_ERROR" && (envelope.Error.ServerCode != tc.code || envelope.Error.RequestID != "req-render") {
				t.Fatalf("fault metadata lost: %s", out)
			}
		})
	}
}

func TestTaskRenderHelp1010(t *testing.T) {
	for _, verb := range []string{"run", "render"} {
		t.Run(verb, func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"task", verb, "--help"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"task render", "task run", "action-run", "read"} {
				if !strings.Contains(out.String(), text) {
					t.Errorf("help missing %q", text)
				}
			}
		})
	}
}

func TestTaskRenderBrokenReplyNeverRetries1010(t *testing.T) {
	for _, reply := range []string{"not JSON", `{"errors":[{"message":"unavailable","extensions":{"code":"SERVICE_UNAVAILABLE"}}]}`} {
		t.Run(reply, func(t *testing.T) {
			server, requests := renderFixture1010(t, reply)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"task", "render", renderNodeRef1010, "--server", server.URL, "--json"})
			if err := root.Execute(); err == nil || len(*requests) != 1 || strings.Contains(out.String(), `"rendered"`) {
				t.Fatalf("err=%v requests=%d output=%s", err, len(*requests), out)
			}
		})
	}
}

func TestTaskRenderInvalidFileAndRefBeforeRequests1010(t *testing.T) {
	file := filepath.Join(t.TempDir(), "args.json")
	if err := os.WriteFile(file, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"task", "render", renderNodeRef1010, "--args", "@" + file}, {"task", "render", "unqualified"}} {
		server, requests := renderFixture1010(t, `{"data":{"renderRunNode":"wrong"}}`)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", server.URL))
		if err := root.Execute(); exitCodeFor(err) != exitcode.Usage || len(*requests) != 0 {
			t.Fatalf("err=%v requests=%d", err, len(*requests))
		}
	}
}
