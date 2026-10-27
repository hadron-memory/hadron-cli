package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestTeamChannel489(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		url        bool
	}{
		{"link", `{"data":{"node":{"id":"root-id","urn":"server-address","portalUrl":"https://portal.test/opaque?q=a%2Fb&x=1"}}}`, 200, true},
		{"null", `{"data":{"node":{"portalUrl":null}}}`, 200, false},
		{"concealed", `{"data":{"node":null}}`, 200, false},
		{"denied", `{"errors":[{"message":"private-pointer-sentinel","extensions":{"code":"FORBIDDEN"}}]}`, 200, false},
		{"gateway", `bad gateway`, 502, false},
	} {
		for _, j := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", tc.name, j), func(t *testing.T) {
				calls := []string{}
				var callsMu sync.Mutex
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						OperationName, Query string
						Variables            map[string]any
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						return
					}
					callsMu.Lock()
					calls = append(calls, req.OperationName)
					callsMu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					doc, err := parser.ParseQuery(&ast.Source{Input: req.Query})
					if err != nil {
						t.Error(err)
						return
					}
					if len(doc.Operations) != 1 || doc.Operations[0].Operation != ast.Query {
						t.Error("not one read query")
						return
					}
					switch req.OperationName {
					case "TeamChannelLocation":
						if len(req.Variables) != 1 || req.Variables["appRef"] != "hrn:app:acme.com:team" {
							t.Error(req.Variables)
						}
						if err := teamChannel489Selection(req.OperationName, doc); err != nil {
							t.Error(err)
							http.Error(w, err.Error(), http.StatusBadRequest)
							return
						}
						fmt.Fprint(w, `{"data":{"app":{"id":"actual-app","urn":"hrn:app:acme.com:chatops","defaultChannel":{"id":"channel-id","chatRootNodeId":"root-id","chatRootUrn":null}}}}`)
					case "NodePortal":
						if len(req.Variables) != 1 || req.Variables["ref"] != "root-id" {
							t.Error(req.Variables)
						}
						if err := teamChannel489Selection(req.OperationName, doc); err != nil {
							t.Error(err)
							http.Error(w, err.Error(), http.StatusBadRequest)
							return
						}
						w.WriteHeader(tc.status)
						fmt.Fprint(w, tc.body)
					default:
						t.Error("unexpected operation", req.OperationName)
					}
				}))
				defer srv.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"team", "channel", "--app", "acme.com:team", "--server", srv.URL}
				if j {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				callsMu.Lock()
				defer callsMu.Unlock()
				if strings.Join(calls, ",") != "TeamChannelLocation,NodePortal" {
					t.Fatal(calls)
				}
				if strings.Contains(out.String(), "private-pointer-sentinel") {
					t.Fatal(out.String())
				}
				if j {
					var d map[string]any
					if err := json.Unmarshal([]byte(out.String()), &d); err != nil {
						t.Fatal(err)
					}
					if len(d) != 4 || d["app"] != "hrn:app:acme.com:chatops" || d["appSource"] != "from --app" || d["channelId"] != "channel-id" {
						t.Fatal(d)
					}
					v, ok := d["portalUrl"]
					if !ok {
						t.Fatal("missing null key")
					}
					if tc.url {
						if v != "https://portal.test/opaque?q=a%2Fb&x=1" {
							t.Fatal(v)
						}
					} else if v != nil {
						t.Fatal(v)
					}
				} else {
					text := out.String()
					if strings.Contains(strings.ToLower(text), "chat") {
						t.Fatal(text)
					}
					if tc.url {
						if !strings.Contains(text, "URL: https://portal.test/opaque?q=a%2Fb&x=1") {
							t.Fatal(text)
						}
					} else if strings.Contains(text, "URL:") || !strings.Contains(text, "no channel URL is available") {
						t.Fatal(text)
					}
				}
			})
		}
	}
}

func TestTeamChannel489FailuresAndHelp(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"no-app", `{"data":{"app":null}}`, 4},
		{"no-channel", `{"data":{"app":{"id":"a","defaultChannel":null}}}`, 4},
		{"forbidden", `{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := captureGraphQL(t, map[string]string{"TeamChannelLocation": tc.body})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "channel", "--app", "acme.com:team", "--server", srv.URL})
			if got := exitCodeFor(root.Execute()); got != tc.code {
				t.Fatal(got)
			}
			if out.Len() != 0 || len(calls) != 1 {
				t.Fatalf("%v %s", calls, out.String())
			}
		})
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "channel", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(out.String()), "chat") || !strings.Contains(out.String(), "server-built") {
		t.Fatal(out.String())
	}
}

func TestTeamChannel489BindingAndContext(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			dir := teamGitDir(t)
			if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0600); err != nil {
				t.Fatal(err)
			}
			srv, calls := captureGraphQL(t, map[string]string{"TeamChannelLocation": `{"data":{"app":{"id":"returned-app","urn":"hrn:app:acme.com:chatops","defaultChannel":{"id":"default-id","chatRootNodeId":"root-id"}}}}`, "NodePortal": `{"data":{"node":{"portalUrl":null}}}`})
			f, out := testFactory(t)
			if configured {
				cfg, err := f.Config()
				if err != nil {
					t.Fatal(err)
				}
				if err := cfg.Set("app", "acme.com:configured"); err != nil {
					t.Fatal(err)
				}
			}
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "channel", "--json", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var vars map[string]any
			if err := json.Unmarshal(calls["TeamChannelLocation"], &vars); err != nil {
				t.Fatal(err)
			}
			expected := "capp100000000000000000000"
			source := "from the worktree binding"
			if configured {
				expected = "hrn:app:acme.com:configured"
				source = "from the App context"
			}
			if vars["appRef"] != expected || !strings.Contains(out.String(), source) {
				t.Fatalf("%v %s", vars, out.String())
			}
			data, err := os.ReadFile(filepath.Join(dir, "hadron-team-session.json"))
			if err != nil || string(data) != bindingFixture {
				t.Fatal("binding changed", err)
			}
		})
	}
}

// Pure query validation lets malformed fixtures fail with diagnostics outside
// the HTTP goroutine. This version retains the old casts for the red pin.
func teamChannel489Selection(operation string, doc *ast.QueryDocument) error {
	fields := doc.Operations[0].SelectionSet
	field := fields[0].(*ast.Field)
	if operation == "TeamChannelLocation" {
		if len(fields) != 1 || field.Name != "app" || len(field.SelectionSet) != 3 {
			return fmt.Errorf("invalid app selection")
		}
		channel := field.SelectionSet[2].(*ast.Field)
		if field.SelectionSet[0].(*ast.Field).Name != "id" || field.SelectionSet[1].(*ast.Field).Name != "urn" || channel.Name != "defaultChannel" || len(channel.SelectionSet) != 2 || channel.SelectionSet[0].(*ast.Field).Name != "id" || channel.SelectionSet[1].(*ast.Field).Name != "chatRootNodeId" {
			return fmt.Errorf("invalid channel selection")
		}
	} else if operation == "NodePortal" {
		if len(fields) != 1 || field.Name != "node" || len(field.SelectionSet) != 3 {
			return fmt.Errorf("invalid node selection")
		}
		for i, name := range []string{"id", "urn", "portalUrl"} {
			if field.SelectionSet[i].(*ast.Field).Name != name {
				return fmt.Errorf("invalid node field")
			}
		}
	}
	return nil
}

func TestTeamChannel489MalformedSelections(t *testing.T) {
	for _, query := range []string{
		`query { ...Wrong }`,
		`query { app { ...Wrong urn defaultChannel { id chatRootNodeId } } }`,
		`query { app { id urn ...Wrong } }`,
		`query { app { id urn defaultChannel { ...Wrong chatRootNodeId } } }`,
	} {
		t.Run(query, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("validator panicked instead of returning a diagnostic: %v", p)
				}
			}()
			doc, err := parser.ParseQuery(&ast.Source{Input: query})
			if err != nil {
				t.Fatal(err)
			}
			if err := teamChannel489Selection("TeamChannelLocation", doc); err == nil {
				t.Error("accepted malformed selection")
			}
		})
	}
}

func TestTeamChannel489EmptySelections(t *testing.T) {
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("validator panicked on empty selection: %v", p)
		}
	}()
	doc := &ast.QueryDocument{Operations: ast.OperationList{&ast.OperationDefinition{Operation: ast.Query}}}
	if err := teamChannel489Selection("TeamChannelLocation", doc); err == nil {
		t.Error("accepted empty selection")
	}
}
