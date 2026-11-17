package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const appNodesRef1626 = "hrn:app:acme.com:team"
const appNodesRows1626 = `[{"id":"n1","memoryId":"m1","loc":"alpha","name":"First","nodeType":"info"},{"id":"n2","memoryId":"m2","loc":"alpha","name":"Second","nodeType":"data"}]`

func TestAppNodes1626PageWireAndStableJSON(t *testing.T) {
	for _, tc := range []struct {
		offset int
		rows   string
		more   bool
	}{
		{0, appNodesRows1626, true}, {2, `[{"id":"n3","memoryId":"m1","loc":"beta","name":"Third","nodeType":"info"}]`, false},
	} {
		t.Run(fmt.Sprint(tc.offset), func(t *testing.T) {
			response := fmt.Sprintf(`{"data":{"appNodesPage":{"items":%s,"total":3,"hasMore":%t}}}`, tc.rows, tc.more)
			gql, captured := captureGraphQL(t, map[string]string{"AppNodesPage": response})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"app", "nodes", "acme.com:team", "--limit", "2", "--offset", fmt.Sprint(tc.offset), "--json", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			vars, present := varsOf(t, captured, "AppNodesPage")
			if !present {
				t.Fatal("no page read")
			}
			if vars["appRef"] != appNodesRef1626 || vars["limit"] != float64(2) || vars["offset"] != float64(tc.offset) {
				t.Fatalf("wire=%v", vars)
			}
			if _, ok := vars["filter"]; ok {
				t.Fatalf("unset filter must be omitted: %v", vars)
			}
			want := fmt.Sprintf(`{"items":%s,"total":3,"hasMore":%t}`, tc.rows, tc.more)
			var actual, expected any
			if err := json.Unmarshal([]byte(out.String()), &actual); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(want), &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("JSON=%s want=%s", out.String(), want)
			}
		})
	}
}

func TestAppNodes1626DefaultAndCountOnlyAreOneRequest(t *testing.T) {
	for _, tc := range []struct {
		args          []string
		limit, offset int
	}{
		{nil, 20, 0}, {[]string{"--limit", "0"}, 0, 0}, {[]string{"--limit", "999"}, 200, 0}, {[]string{"--limit", "-5", "--offset", "-2"}, 0, 0},
	} {
		t.Run(fmt.Sprint(tc.args), func(t *testing.T) {
			calls := 0
			gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					OperationName string
					Query         string
					Variables     map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				calls++
				if !strings.Contains(q.Query, "appNodesPage(") || strings.Contains(q.Query, "appNodes(") {
					t.Errorf("wrong root field on the wire: %s", q.Query)
				}
				if q.OperationName != "AppNodesPage" || q.Variables["limit"] != float64(tc.limit) || q.Variables["offset"] != float64(tc.offset) {
					t.Errorf("request=%+v", q)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"data":{"appNodesPage":{"items":[],"total":30,"hasMore":true}}}`)
			}))
			defer gql.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"app", "nodes", appNodesRef1626, "--json", "--server", gql.URL}
			root.SetArgs(append(args, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("read %d requests, want one deliberate page", calls)
			}
			if !strings.Contains(out.String(), `"items": []`) || !strings.Contains(out.String(), `"hasMore": true`) {
				t.Fatalf("empty/count page lost envelope: %s", out.String())
			}
		})
	}
}

func TestAppNodes1626HumanOutputStatesMemoryAndContinuation(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"AppNodesPage": `{"data":{"appNodesPage":{"items":` + appNodesRows1626 + `,"total":3,"hasMore":true}}}`})
	f, out := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "nodes", appNodesRef1626, "--limit", "2", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"LOC", "NAME", "TYPE", "MEMORY", "alpha", "First", "Second", "m1", "m2"} {
		if !strings.Contains(out.String(), v) {
			t.Errorf("table lacks %q: %s", v, out.String())
		}
	}
	for _, v := range []string{"3", "more: yes", "--offset 2"} {
		if !strings.Contains(stderr.String(), v) {
			t.Errorf("continuation lacks %q: %s", v, stderr.String())
		}
	}
}

func TestAppNodes1626FiltersAndFalseAreForwarded(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"AppNodesPage": `{"data":{"appNodesPage":{"items":[],"total":0,"hasMore":false}}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "nodes", appNodesRef1626, "--prefix", "tasks:", "--type", "task", "--object-type", "work", "--tag", "a", "--tag", "b", "--runnable=false", "--created-by", "u1", "--role", "review", "--updated-after", "2026-10-01T00:00:00Z", "--updated-before", "2026-11-01T00:00:00Z", "--include-deleted=false", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	vars, _ := varsOf(t, captured, "AppNodesPage")
	filter, _ := vars["filter"].(map[string]any)
	want := map[string]any{"locPrefix": "tasks:", "nodeType": "task", "objectType": "work", "tags": []any{"a", "b"}, "isRunnable": false, "createdBy": "u1", "role": "review", "updatedAfter": "2026-10-01T00:00:00Z", "updatedBefore": "2026-11-01T00:00:00Z", "includeDeleted": false}
	if !reflect.DeepEqual(filter, want) {
		t.Fatalf("filter=%v want=%v", filter, want)
	}
}

func TestAppNodes1626MemoryFiltersAreLocalUsageErrors(t *testing.T) {
	for _, flags := range [][]string{{"--memory", "m1"}, {"-m", "m1"}, {"--memory="}, {"--memory-class", "system"}} {
		t.Run(fmt.Sprint(flags), func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"app", "nodes", appNodesRef1626, "--server", gql.URL}
			root.SetArgs(append(args, flags...))
			err := root.Execute()
			if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 {
				t.Fatalf("err=%v requests=%v", err, captured)
			}
			if !strings.Contains(err.Error(), "App") || !strings.Contains(err.Error(), "node list") {
				t.Fatalf("missing scope/remedy: %v", err)
			}
		})
	}
}

func TestAppNodes1626RefusalAndMalformedPage(t *testing.T) {
	for _, tc := range []struct {
		response string
		code     int
	}{
		{`{"errors":[{"message":"App not found.","extensions":{"code":"NOT_FOUND"}}]}`, exitcode.NotFound},
		{`{"data":{"appNodesPage":null}}`, exitcode.Error},
		{`{"data":{"appNodesPage":{"items":null,"total":0,"hasMore":false}}}`, exitcode.Error},
		{`{"data":{"appNodesPage":{"items":[null],"total":1,"hasMore":false}}}`, exitcode.Error},
	} {
		t.Run(tc.response, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{"AppNodesPage": tc.response})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"app", "nodes", appNodesRef1626, "--json", "--server", gql.URL})
			err := root.Execute()
			if exitCodeFor(err) != tc.code {
				t.Fatalf("exit=%d err=%v", exitCodeFor(err), err)
			}
			if out.Len() != 0 {
				t.Fatalf("partial success output: %s", out.String())
			}
		})
	}
}

func TestAppLs1626AgentComposesAndDoesNotResolveTheAgent(t *testing.T) {
	for _, extra := range [][]string{nil, {"--org", "acme.com"}, {"--owned-by-me"}} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"Apps": appsOwnedJSON})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"app", "ls", "--agent", "hrn:agent:acme.com:iris", "--json", "--server", gql.URL}
			root.SetArgs(append(args, extra...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			vars, present := varsOf(t, captured, "Apps")
			if !present {
				t.Fatal("Apps not called")
			}
			filter, _ := vars["filter"].(map[string]any)
			if vars["limit"] != float64(20) {
				t.Fatalf("agent listing must not ask for more than default20: %v", vars)
			}
			if filter["agentId"] != "hrn:agent:acme.com:iris" {
				t.Fatalf("agent selector lost: %v", filter)
			}
			if len(extra) > 0 && extra[0] == "--owned-by-me" && filter["ownedByMe"] != true {
				t.Fatalf("ownership lost: %v", filter)
			}
			if len(extra) == 0 {
				if _, ok := vars["orgId"]; ok {
					t.Fatalf("agent-only must not infer org: %v", vars)
				}
			}
			if len(captured) != 1 {
				t.Fatalf("must pass reference directly, no resolution read: %v", captured)
			}
			var rows []any
			if err := json.Unmarshal([]byte(out.String()), &rows); err != nil || len(rows) != 1 {
				t.Fatalf("old bare-array contract changed: %s err=%v", out.String(), err)
			}
		})
	}
}

func TestAppLs1626EmptyAgentIsLocalUsage(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "ls", "--agent=", "--server", gql.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 || !strings.Contains(err.Error(), "--agent") {
		t.Fatalf("err=%v requests=%v", err, captured)
	}
}

func TestAppNodes1626EmptyDisplayedPageAdvancesRawWindow(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"AppNodesPage": `{"data":{"appNodesPage":{"items":[],"total":10,"hasMore":true}}}`})
	f, _ := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "nodes", appNodesRef1626, "--limit", "2", "--offset", "4", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "--offset 6") {
		t.Fatalf("must advance requested raw window, not displayed0: %s", stderr.String())
	}
}

func TestAppLs1626StubFiltersInstalledAgentAndKeepsArray(t *testing.T) {
	for _, agent := range []string{"hrn:agent:acme.com:iris", "hrn:agent:acme.com:missing"} {
		t.Run(agent, func(t *testing.T) {
			gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					OperationName string
					Query         string
					Variables     struct {
						Filter struct {
							AgentID string `json:"agentId"`
						} `json:"filter"`
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				rows := []map[string]any{}
				installed := map[string][]string{"with-iris": {"hrn:agent:acme.com:iris"}, "without-iris": {"hrn:agent:acme.com:other"}}
				for _, id := range []string{"with-iris", "without-iris"} {
					match := q.Variables.Filter.AgentID == ""
					for _, ref := range installed[id] {
						if ref == q.Variables.Filter.AgentID {
							match = true
						}
					}
					if match {
						rows = append(rows, map[string]any{"id": id, "urn": "hrn:app:acme.com:" + id, "name": id, "appType": "STANDARD", "agentId": nil, "memberCount": 1, "createdAt": "2026-11-01T00:00:00Z"})
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"apps": map[string]any{"items": rows, "total": len(rows)}}})
			}))
			defer gql.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"app", "ls", "--agent", agent, "--json", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var rows []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(agent, ":missing") {
				if len(rows) != 0 || strings.TrimSpace(out.String()) != "[]" {
					t.Fatalf("missing/unreadable Agent should be empty array: %s", out.String())
				}
			} else if len(rows) != 1 || rows[0].ID != "with-iris" {
				t.Fatalf("installed-Agent filtering lost: %s", out.String())
			}
		})
	}
}

func TestAppNodes1626UnsupportedNarrowingRefusesBeforeAnyRequest(t *testing.T) {
	for _, flags := range [][]string{{"--children-of", "tasks"}, {"--children-of="}, {"--where", `{"path":["kind"],"eq":"x"}`}, {"--where="}} {
		t.Run(fmt.Sprint(flags), func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"app", "nodes", appNodesRef1626, "--server", gql.URL}
			root.SetArgs(append(args, flags...))
			err := root.Execute()
			if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 {
				t.Fatalf("unsupported narrowing sent request: err=%v vars=%v", err, captured)
			}
			if !strings.Contains(err.Error(), "not supported") || !strings.Contains(err.Error(), "node list") {
				t.Fatalf("missing actionable unsupported-filter explanation: %v", err)
			}
		})
	}
}

func TestAppLs1626UnsetAgentDoesNotReachTheWire(t *testing.T) {
	for _, flags := range [][]string{{"--org", "acme.com"}, {"--owned-by-me"}} {
		t.Run(fmt.Sprint(flags), func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"Apps": appsOwnedJSON})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"app", "ls", "--json", "--server", gql.URL}
			root.SetArgs(append(args, flags...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			vars, _ := varsOf(t, captured, "Apps")
			if filter, ok := vars["filter"].(map[string]any); ok {
				if _, present := filter["agentId"]; present {
					t.Fatalf("unset agent must be omitted, not null: %v", filter)
				}
			}
		})
	}
}

func TestAppLs1626AgentWalksEveryBoundedPage(t *testing.T) {
	calls := 0
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Variables map[string]any }
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		offset := int(q.Variables["offset"].(float64))
		if q.Variables["limit"] != float64(20) || offset != calls*20 {
			t.Errorf("page %d: %v", calls, q.Variables)
		}
		calls++
		rows := make([]map[string]any, 0)
		for i := offset; i < offset+20 && i < 25; i++ {
			rows = append(rows, map[string]any{"id": fmt.Sprintf("a%d", i), "name": fmt.Sprintf("App %d", i)})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"apps": map[string]any{"items": rows, "total": 25}}})
	}))
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "list", "--agent", "hrn:agent:acme.com:iris", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(rows) != 25 {
		t.Fatalf("calls=%d rows=%d", calls, len(rows))
	}
}
