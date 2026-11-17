package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Both legacy and paged projections are supplied so this pin exposes the old
// silent first-page output rather than merely an absent response field.
func TestOrgPublic1858PartialPages(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprint(asJSON), func(t *testing.T) {
			rows := make([]map[string]any, 20)
			for i := range rows {
				rows[i] = map[string]any{"id": fmt.Sprint(i), "name": fmt.Sprintf("Row%02d", i), "urn": fmt.Sprintf("acme.com::r%d", i), "description": nil}
			}
			page := map[string]any{"items": rows, "total": 45, "hasMore": true}
			body, _ := json.Marshal(map[string]any{"data": map[string]any{"publicOrganization": map[string]any{"id": "o", "urn": "acme.com", "name": "Acme", "listedOnMarketplace": true, "publicMemories": rows, "publicAgents": rows, "publicMemoriesPage": page, "publicAgentsPage": page}}})
			gql, captured := captureGraphQL(t, map[string]string{"PublicOrganization": string(body)})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"org", "public", "acme.com", "--server", gql.URL}
			if asJSON {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if asJSON {
				var got map[string]json.RawMessage
				if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
					t.Fatal(err)
				}
				for _, kind := range []string{"publicMemories", "publicAgents"} {
					var items []any
					if err := json.Unmarshal(got[kind], &items); err != nil {
						t.Fatal(err)
					}
					if len(items) != 20 {
						t.Errorf("%s rows=%d", kind, len(items))
					}
					if string(got[kind+"Total"]) != "45" || string(got[kind+"HasMore"]) != "true" {
						t.Errorf("missing %s page metadata: %s", kind, out.String())
					}
				}
			} else if strings.Count(out.String(), "showing 20 of 45") != 2 {
				t.Errorf("both partial lists need notice: %s", out.String())
			}
			var vars map[string]any
			_ = json.Unmarshal(captured["PublicOrganization"], &vars)
			if vars["limit"] != float64(20) || vars["offset"] != float64(0) {
				t.Errorf("default page variables: %v", vars)
			}
		})
	}
}

func TestOrgPublic1858PagingFlagsAndCompletePage(t *testing.T) {
	for _, tc := range []struct {
		limit, offset         string
		wantLimit, wantOffset float64
	}{{"7", "21", 7, 21}, {"999", "0", 999, 0}, {"0", "0", 0, 0}, {"-4", "-9", 0, 0}} {
		t.Run(tc.limit+"/"+tc.offset, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"PublicOrganization": `{"data":{"publicOrganization":{"id":"o","urn":"acme.com","name":"Acme","listedOnMarketplace":false,"publicMemoriesPage":{"items":[],"total":0,"hasMore":false},"publicAgentsPage":{"items":[],"total":0,"hasMore":false}}}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"org", "public", "acme.com", "--limit", tc.limit, "--offset", tc.offset, "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var vars map[string]any
			_ = json.Unmarshal(captured["PublicOrganization"], &vars)
			if vars["limit"] != tc.wantLimit || vars["offset"] != tc.wantOffset {
				t.Errorf("variables %v", vars)
			}
			if strings.Contains(out.String(), "showing ") {
				t.Errorf("complete list has partial notice: %s", out.String())
			}
		})
	}
}

func TestOrgPublic1858PagedQueryAndIndependentCounts(t *testing.T) {
	calls := 0
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"capabilities":[]}`)
			return
		}
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		calls++
		if req.Query != gen.PublicOrganization_Operation {
			t.Error("wire document differs from generated operation")
		}
		for _, field := range []string{"publicMemoriesPage(limit: $limit, offset: $offset)", "publicAgentsPage(limit: $limit, offset: $offset)"} {
			found := false
			for _, line := range strings.Split(gen.PublicOrganization_Operation, "\n") {
				if strings.TrimSpace(line) == field+" {" {
					found = true
				}
			}
			if !found {
				t.Errorf("missing exact paged selection: %s", field)
			}
		}
		fmt.Fprint(w, `{"data":{"publicOrganization":{"id":"o","urn":"acme.com","name":"Acme","listedOnMarketplace":false,"publicMemoriesPage":{"items":[],"total":42,"hasMore":true},"publicAgentsPage":{"items":[],"total":0,"hasMore":false}}}}`)
	}))
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "public", "acme.com", "--limit", "0", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("must fetch one page, calls=%d", calls)
	}
	if !strings.Contains(out.String(), "public memories: showing 0 of 42") || !strings.Contains(out.String(), "public agents: (none)") {
		t.Errorf("independent count-only output: %s", out.String())
	}
}

func TestOrgPublic1858NotFoundUnchanged(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"PublicOrganization": `{"data":{"publicOrganization":null}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "public", "ghost.com", "--server", gql.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.NotFound || err.Error() != `organization "ghost.com" is not discoverable (or does not exist)` {
		t.Fatalf("notfound contract: %v", err)
	}
}
