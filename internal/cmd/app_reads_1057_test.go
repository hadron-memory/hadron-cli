package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

const appRead1057 = "hrn:app:acme.com:support"

func TestAppReads1057(t *testing.T) {
	for _, keys := range []bool{false, true} {
		for _, nulls := range []bool{false, true} {
			for _, jsonMode := range []bool{false, true} {
				t.Run(fmt.Sprintf("keys%v/null%v/json%v", keys, nulls, jsonMode), func(t *testing.T) {
					var mu sync.Mutex
					calls := 0
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var req struct {
							OperationName, Query string
							Variables            map[string]any
						}
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
						}
						mu.Lock()
						calls++
						mu.Unlock()
						op, field, arg := "GetAppMetadata", "app", "ref"
						if keys {
							op, field, arg = "AppKeysMetadata", "appKeys", "appRef"
						}
						if req.OperationName != op || len(req.Variables) != 1 || req.Variables[arg] != appRead1057 {
							t.Errorf("unexpected lookup/write %+v", req)
						}
						assertApp1057Document(t, req.Query, keys)
						description, label, lastUsed, revoked := `"description value"`, `"primary"`, `"2026-01-02T00:00:00Z"`, `"2026-01-03T00:00:00Z"`
						if nulls {
							description, label, lastUsed, revoked = "null", "null", "null", "null"
						}
						payload := fmt.Sprintf(`{"id":"app-id","urn":"%s","name":"Support","appType":"CHATBOT","description":%s,"settings":"private-settings-sentinel","keys":[{"rawKey":"raw-secret-sentinel"}]}`, appRead1057, description)
						if keys {
							payload = fmt.Sprintf(`[{"id":"key-id","keyPreview":"hdr_...abcd","label":%s,"createdAt":"2026-01-01T00:00:00Z","lastUsedAt":%s,"revokedAt":%s,"rawKey":"raw-secret-sentinel","hostUses":["private-host-sentinel"]}]`, label, lastUsed, revoked)
						}
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{"data":{"%s":%s}}`, field, payload)
					}))
					defer srv.Close()
					f, out := testFactory(t)
					root := NewRootCmd(f)
					args := []string{"app", "get", appRead1057}
					if keys {
						args = []string{"app", "key", "ls", appRead1057}
					}
					args = append(args, "--server", srv.URL)
					if jsonMode {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					if err := root.Execute(); err != nil {
						t.Fatal(err)
					}
					mu.Lock()
					n := calls
					mu.Unlock()
					if n != 1 {
						t.Fatalf("HTTP calls=%d want1", n)
					}
					for _, secret := range []string{"raw-secret-sentinel", "private-host-sentinel", "private-settings-sentinel"} {
						if strings.Contains(out.String(), secret) {
							t.Fatalf("confidential output %s", out)
						}
					}
					if jsonMode {
						var a map[string]any
						if keys {
							var rows []map[string]any
							if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
								t.Fatal(err)
							}
							if len(rows) != 1 {
								t.Fatal(out.String())
							}
							a = rows[0]
						} else {
							if err := json.Unmarshal([]byte(out.String()), &a); err != nil {
								t.Fatal(err)
							}
						}
						gotKeys := []string{}
						for k := range a {
							gotKeys = append(gotKeys, k)
						}
						sort.Strings(gotKeys)
						want := []string{"id", "urn", "name", "appType", "description"}
						if keys {
							want = []string{"id", "keyPreview", "label", "createdAt", "lastUsedAt", "revokedAt"}
						}
						sort.Strings(want)
						if !reflect.DeepEqual(gotKeys, want) {
							t.Fatalf("JSON keys %v", gotKeys)
						}
						expected := map[string]any{"id": "app-id", "urn": appRead1057, "name": "Support", "appType": "CHATBOT", "description": "description value"}
						if keys {
							expected = map[string]any{"id": "key-id", "keyPreview": "hdr_...abcd", "label": "primary", "createdAt": "2026-01-01T00:00:00Z", "lastUsedAt": "2026-01-02T00:00:00Z", "revokedAt": "2026-01-03T00:00:00Z"}
						}
						if nulls {
							if keys {
								for _, k := range []string{"label", "lastUsedAt", "revokedAt"} {
									expected[k] = nil
								}
							} else {
								expected["description"] = nil
							}
						}
						if !reflect.DeepEqual(a, expected) {
							t.Fatalf("metadata output=%v want%v", a, expected)
						}

						if nulls {
							fields := []string{"description"}
							if keys {
								fields = []string{"label", "lastUsedAt", "revokedAt"}
							}
							for _, key := range fields {
								if a[key] != nil {
									t.Fatalf("%s should stay null: %s", key, out)
								}
							}
						}
					} else {
						want := "Support"
						if keys {
							want = "hdr_...abcd"
						}
						if !strings.Contains(out.String(), want) {
							t.Fatal(out.String())
						}
						if strings.Contains(out.String(), "not shown to you") {
							t.Fatal("invented withholding reason")
						}
						if nulls {
							if !keys {
								if !strings.Contains(out.String(), "description: -") {
									t.Fatal(out.String())
								}
							} else {
								lines := strings.Split(strings.TrimSpace(out.String()), "\n")
								row := strings.Fields(lines[len(lines)-1])
								if len(row) != 6 || row[2] != "-" || row[4] != "-" || row[5] != "-" {
									t.Fatalf("null text=%s", out)
								}
							}
						}

					}
				})
			}
		}
	}
}

func TestAppReads1057EmptyAndMissing(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonMode), func(t *testing.T) {
			gql := fakeGraphQL(t, map[string]string{"AppKeysMetadata": `{"data":{"appKeys":[]}}`, "GetAppMetadata": `{"data":{"app":null}}`})
			defer gql.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"app", "key", "list", appRead1057, "--server", gql.URL}
			if jsonMode {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if jsonMode && strings.TrimSpace(out.String()) != "[]" {
				t.Fatalf("empty JSON=%s", out)
			}
			if !jsonMode && !strings.Contains(out.String(), "no active App keys") {
				t.Fatal(out.String())
			}
			f, _ = testFactory(t)
			root = NewRootCmd(f)
			root.SetArgs([]string{"app", "get", appRead1057, "--server", gql.URL})
			if got := exitCodeFor(root.Execute()); got != exitcode.NotFound {
				t.Fatalf("missing exit%d", got)
			}
		})
	}
}
func TestAppReads1057Refusals(t *testing.T) {
	for _, keys := range []bool{false, true} {
		for _, tc := range []struct {
			status int
			code   string
			want   int
		}{{200, "FORBIDDEN", 8}, {200, "NOT_FOUND", 4}, {200, "UNAUTHENTICATED", 3}, {502, "", 7}} {
			t.Run(fmt.Sprintf("%v/%s/%d", keys, tc.code, tc.status), func(t *testing.T) {
				var mu sync.Mutex
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					calls++
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					if tc.code != "" {
						fmt.Fprintf(w, `{"errors":[{"message":"read refusal sentinel","extensions":{"code":%q}}]}`, tc.code)
					} else {
						fmt.Fprint(w, "bad gateway")
					}
				}))
				defer srv.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"app", "get", appRead1057}
				if keys {
					args = []string{"app", "key", "ls", appRead1057}
				}
				root.SetArgs(append(args, "--server", srv.URL, "--json"))
				err := root.Execute()
				if got := exitCodeFor(err); got != tc.want {
					t.Fatalf("exit%d want%d: %v", got, tc.want, err)
				}
				if out.Len() != 0 {
					t.Fatalf("refusal emitted success %s", out)
				}
				mu.Lock()
				n := calls
				mu.Unlock()
				if n != 1 {
					t.Fatalf("requests%d", n)
				}
			})
		}
	}
}
func TestAppReads1057InvalidRefNoHTTP(t *testing.T) {
	for _, ref := range []string{"", "  ", "bare-name", "hrn:node:acme.com:kb:x"} {
		for _, keys := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/%s", keys, ref), func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("invalid ref reached HTTP") }))
				defer srv.Close()
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"app", "get", ref}
				if keys {
					args = []string{"app", "key", "ls", ref}
				}
				root.SetArgs(append(args, "--server", srv.URL))
				if got := exitCodeFor(root.Execute()); got != 2 {
					t.Fatalf("exit%d", got)
				}
			})
		}
	}
}

func TestAppReads1057CanonicalRefs(t *testing.T) {
	for _, ref := range []string{"acme.com:support", "acme.com::support", "urn:app:acme.com::support", "0123456789abcdef0123456789abcdef"} {
		for _, keys := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", ref, keys), func(t *testing.T) {
				want := appRead1057
				if !strings.Contains(ref, ":") {
					want = ref
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var q struct {
						OperationName string
						Variables     map[string]any
					}
					_ = json.NewDecoder(r.Body).Decode(&q)
					arg := "ref"
					if keys {
						arg = "appRef"
					}
					if len(q.Variables) != 1 || q.Variables[arg] != want {
						t.Errorf("ref changed: %+v", q)
					}
					w.Header().Set("Content-Type", "application/json")
					if keys {
						fmt.Fprint(w, `{"data":{"appKeys":[]}}`)
					} else {
						fmt.Fprintf(w, `{"data":{"app":{"id":"app-id","urn":%q,"name":"Support","appType":"CHATBOT","description":null}}}`, appRead1057)
					}
				}))
				defer srv.Close()
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"app", "get", ref}
				if keys {
					args = []string{"app", "key", "ls", ref}
				}
				root.SetArgs(append(args, "--server", srv.URL, "--json"))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
func TestAppReads1057InvalidRowNoPartialOutput(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"AppKeysMetadata": `{"data":{"appKeys":[{"id":"key-id","keyPreview":"hdr_...abcd","label":null,"createdAt":"2026-01-01T00:00:00Z","lastUsedAt":null,"revokedAt":null},null]}}`})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "key", "ls", appRead1057, "--json", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != 1 {
		t.Fatalf("exit=%d", got)
	}
	if out.Len() != 0 {
		t.Fatalf("partial success: %s", out)
	}
}

func assertApp1057Document(t *testing.T, query string, keys bool) {
	t.Helper()
	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		t.Error(err)
		return
	}
	opName, field, arg := "GetAppMetadata", "app", "ref"
	want := []string{"id", "urn", "name", "appType", "description"}
	if keys {
		opName, field, arg = "AppKeysMetadata", "appKeys", "appRef"
		want = []string{"id", "keyPreview", "label", "createdAt", "lastUsedAt", "revokedAt"}
	}
	op := doc.Operations.ForName(opName)
	if op == nil || op.Operation != ast.Query || len(doc.Operations) != 1 || len(op.SelectionSet) != 1 || len(op.VariableDefinitions) != 1 {
		t.Error("not one bounded query")
		return
	}
	root, ok := op.SelectionSet[0].(*ast.Field)
	if !ok || root.Name != field || (root.Alias != "" && root.Alias != field) || len(root.Arguments) != 1 {
		t.Error("wrong root")
		return
	}
	argument := root.Arguments.ForName(arg)
	if argument == nil || argument.Value.Kind != ast.Variable || argument.Value.Raw != arg {
		t.Error("wrong argument")
		return
	}
	if len(root.SelectionSet) != len(want) {
		t.Errorf("expanded projection: %s", query)
		return
	}
	for i, selection := range root.SelectionSet {
		f, ok := selection.(*ast.Field)
		if !ok || f.Name != want[i] || (f.Alias != "" && f.Alias != want[i]) || len(f.SelectionSet) != 0 || len(f.Arguments) != 0 || len(f.Directives) != 0 {
			t.Errorf("unexpected metadata projection %s", query)
			return
		}
	}
}
