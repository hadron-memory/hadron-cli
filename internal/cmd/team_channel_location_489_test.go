package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
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
					if teamChannel489ChatWording(text) {
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
			srv, calls := teamChannel489Capture(t, map[string]string{"TeamChannelLocation": tc.body})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "channel", "--app", "acme.com:team", "--server", srv.URL})
			if got := exitCodeFor(root.Execute()); got != tc.code {
				t.Fatal(got)
			}
			if out.Len() != 0 || len(calls().Operations) != 1 {
				t.Fatalf("%v %s", calls(), out.String())
			}
		})
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "channel", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if teamChannel489ChatWording(out.String()) || !strings.Contains(out.String(), "server-built") {
		t.Fatal(out.String())
	}
}

func TestTeamChannel489BindingAndContext(t *testing.T) {
	for _, scope := range []string{"binding", "config", "flag"} {
		t.Run(scope, func(t *testing.T) {
			dir := teamGitDir(t)
			if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0600); err != nil {
				t.Fatal(err)
			}
			srv, calls := teamChannel489Capture(t, map[string]string{"TeamChannelLocation": `{"data":{"app":{"id":"returned-app","urn":"hrn:app:acme.com:chatops","defaultChannel":{"id":"default-id","chatRootNodeId":"root-id"}}}}`, "NodePortal": `{"data":{"node":{"portalUrl":null}}}`})
			f, out := testFactory(t)
			if scope != "binding" {
				cfg, err := f.Config()
				if err != nil {
					t.Fatal(err)
				}
				if err := cfg.Set("app", "acme.com:configured"); err != nil {
					t.Fatal(err)
				}
			}
			root := NewRootCmd(f)
			args := []string{"team", "channel", "--json", "--server", srv.URL}
			if scope == "flag" {
				args = append(args, "--app", "acme.com:explicit")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var vars map[string]any
			if err := json.Unmarshal(calls().Variables["TeamChannelLocation"], &vars); err != nil {
				t.Fatal(err)
			}
			expected := "capp100000000000000000000"
			source := "from the worktree binding"
			if scope != "binding" {
				expected = "hrn:app:acme.com:configured"
				source = "from the App context"
			}
			if scope == "flag" {
				expected = "hrn:app:acme.com:explicit"
				source = "from --app"
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

// Validate before indexing or casting, returning diagnostics to the handler.
func teamChannel489Selection(operation string, doc *ast.QueryDocument) error {
	if doc == nil || len(doc.Operations) != 1 || doc.Operations[0] == nil || doc.Operations[0].Operation != ast.Query {
		return fmt.Errorf("expected one read query")
	}
	check := func(fields ast.SelectionSet, names ...string) ([]*ast.Field, error) {
		if len(fields) != len(names) {
			return nil, fmt.Errorf("expected fields %v, got %d selections", names, len(fields))
		}
		result := make([]*ast.Field, len(names))
		for i, name := range names {
			field, ok := fields[i].(*ast.Field)
			if !ok || field == nil || field.Name != name {
				return nil, fmt.Errorf("selection %d: expected field %s, got %T", i, name, fields[i])
			}
			result[i] = field
		}
		return result, nil
	}
	fields := doc.Operations[0].SelectionSet
	switch operation {
	case "TeamChannelLocation":
		app, err := check(fields, "app")
		if err != nil {
			return err
		}
		appFields, err := check(app[0].SelectionSet, "id", "urn", "defaultChannel")
		if err != nil {
			return err
		}
		_, err = check(appFields[2].SelectionSet, "id", "chatRootNodeId")
		return err
	case "NodePortal":
		node, err := check(fields, "node")
		if err != nil {
			return err
		}
		_, err = check(node[0].SelectionSet, "id", "urn", "portalUrl")
		return err
	default:
		return fmt.Errorf("unexpected operation %q", operation)
	}
}

// Check command wording, not legitimate App slugs such as chatops.
func teamChannel489ChatWording(text string) bool {
	text = strings.ToLower(text)
	for _, phrase := range []string{"team chat", "chat history", "chat:"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
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

// A local recorder avoids the shared captureGraphQL helper's unsynchronized
// map. Snapshots copy raw messages while holding the same lock as the writer.
type teamChannel489Snapshot struct {
	Variables  map[string]json.RawMessage
	Operations []string
}

func teamChannel489Capture(t *testing.T, responses map[string]string) (*httptest.Server, func() teamChannel489Snapshot) {
	t.Helper()
	var mu sync.Mutex
	calls := map[string]json.RawMessage{}
	operations := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string
			Variables     json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		body, ok := responses[req.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", req.OperationName)
			http.Error(w, "unexpected operation", http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls[req.OperationName] = append(json.RawMessage(nil), req.Variables...)
		operations = append(operations, req.OperationName)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() teamChannel489Snapshot {
		mu.Lock()
		defer mu.Unlock()
		snapshot := map[string]json.RawMessage{}
		for k, v := range calls {
			snapshot[k] = append(json.RawMessage(nil), v...)
		}
		return teamChannel489Snapshot{Variables: snapshot, Operations: append([]string(nil), operations...)}
	}
}

func TestTeamChannel489EmptyRoot(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonOutput), func(t *testing.T) {
			srv, calls := teamChannel489Capture(t, map[string]string{
				"TeamChannelLocation": `{"data":{"app":{"id":"app","urn":"hrn:app:acme.com:chatops","defaultChannel":{"id":"channel","chatRootNodeId":""}}}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "channel", "--app", "acme.com:chatops", "--server", srv.URL}
			if jsonOutput {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			snapshot := calls()
			if strings.Join(snapshot.Operations, ",") != "TeamChannelLocation" || snapshot.Variables["TeamChannelLocation"] == nil {
				t.Fatalf("expected exactly TeamChannelLocation: %v", snapshot)
			}
			if jsonOutput {
				var dto map[string]any
				if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
					t.Fatal(err)
				}
				portal, present := dto["portalUrl"]
				if !present || portal != nil {
					t.Fatalf("portalUrl must be null: %v", dto)
				}
			} else if !strings.Contains(out.String(), "no channel URL is available") || strings.Contains(out.String(), "URL:") {
				t.Fatal(out.String())
			}
		})
	}
}

func TestTeamChannel489ChatWording(t *testing.T) {
	for _, text := range []string{"team chat", "Chat history", "chat:"} {
		if !teamChannel489ChatWording(text) {
			t.Errorf("missed obsolete phrase %q", text)
		}
	}
	if teamChannel489ChatWording("app: hrn:app:acme.com:chatops") {
		t.Error("rejected legitimate slug")
	}
}

// Exercise concurrent HTTP writers and snapshots, rather than relying only on
// the command's normally sequential requests to make -race meaningful.
func TestTeamChannel489ConcurrentCapture(t *testing.T) {
	srv, calls := teamChannel489Capture(t, map[string]string{"Probe": `{"data":{}}`})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"operationName":"Probe","variables":{"appRef":"test"}}`))
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			_ = calls()
		}()
	}
	wg.Wait()
	if got := len(calls().Operations); got != 16 {
		t.Errorf("captured %d requests, want 16", got)
	}
	snapshot := calls()
	snapshot.Variables["Probe"][0] = 'x'
	if !json.Valid(calls().Variables["Probe"]) {
		t.Error("snapshot aliases recorder data")
	}
}

func TestTeamChannel489InvalidNodeSelections(t *testing.T) {
	for _, query := range []string{
		`query { ...Wrong }`,
		`query { node { id ...Wrong portalUrl } }`,
		`query { node { id urn } }`,
		`query { node { id urn portalUrl content } }`,
		`query { node { id urn content } }`,
		`mutation { node { id urn portalUrl } }`,
	} {
		t.Run(query, func(t *testing.T) {
			doc, err := parser.ParseQuery(&ast.Source{Input: query})
			if err != nil {
				t.Fatal(err)
			}
			if err := teamChannel489Selection("NodePortal", doc); err == nil {
				t.Error("accepted malformed node selection")
			}
		})
	}
	for _, doc := range []*ast.QueryDocument{nil, {}, {Operations: ast.OperationList{nil}}} {
		if err := teamChannel489Selection("NodePortal", doc); err == nil {
			t.Error("accepted missing operation")
		}
	}
}
