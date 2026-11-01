package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestFind1345(t *testing.T) {
	for _, query := range []string{"0123456789abcdef0123456789abcdef", "hrn:mem:acme.com:kb", "plain name"} {
		g, c := captureGraphQL(t, map[string]string{"GlobalSearch": `{"data":{"globalSearch":{"hits":[{"id":"one","entityType":"user","name":null,"urn":null,"score":1,"matchedField":"pk","description":null,"memoryId":null,"organizationId":null}],"total":1,"degraded":[],"scope":null}}}`})
		f, out := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs([]string{"find", query, "--json", "--server", g.URL})
		if err := r.Execute(); err != nil {
			t.Fatal(err)
		}
		var v map[string]json.RawMessage
		if err := json.Unmarshal(c["GlobalSearch"], &v); err != nil {
			t.Fatal(err)
		}
		q, _ := json.Marshal(query)
		if string(v["query"]) != string(q) {
			t.Fatal(v)
		}
		for _, k := range []string{"fields", "entityTypes", "orgId", "scope", "appRef"} {
			if _, ok := v[k]; ok {
				t.Fatalf("sniffed search sent %s: %s", k, c["GlobalSearch"])
			}
		}
		var d struct {
			Hits []struct {
				ID   string  `json:"id"`
				Name *string `json:"name"`
				URN  *string `json:"urn"`
			}
			Total    int
			Degraded []string
		}
		if err := json.Unmarshal([]byte(out.String()), &d); err != nil {
			t.Fatal(err)
		}
		if len(d.Hits) != 1 || d.Hits[0].ID != "one" || d.Hits[0].Name != nil || d.Hits[0].URN != nil || d.Degraded == nil {
			t.Fatal(out.String())
		}
	}
}
func TestFindOverrides1345(t *testing.T) {
	g, c := captureGraphQL(t, map[string]string{"GlobalSearch": `{"data":{"globalSearch":{"hits":[],"total":0,"degraded":["content skipped"],"scope":null}}}`})
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"find", "q", "--field", "name,urn", "--entity-type", "agent,user", "--org", "acme.com", "--limit", "7", "--offset", "3", "--server", g.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	_ = json.Unmarshal(c["GlobalSearch"], &v)
	for k, w := range map[string]string{"fields": `["name","urn"]`, "entityTypes": `["agent","user"]`, "orgId": `"acme.com"`, "limit": "7", "offset": "3"} {
		if string(v[k]) != w {
			t.Errorf("%s=%s", k, v[k])
		}
	}
}
func TestMarketplace1345(t *testing.T) {
	for _, x := range []struct{ cmd, op, field string }{{"agents", "MarketplaceAgents", "marketplaceAgents"}, {"memories", "MarketplaceMemories", "marketplaceMemories"}, {"orgs", "MarketplaceOrganizations", "marketplaceOrganizations"}} {
		t.Run(x.cmd, func(t *testing.T) {
			g, c := captureGraphQL(t, map[string]string{x.op: `{"data":{"` + x.field + `":{"items":[{"id":"public1","name":"Public","urn":"hrn:mem:acme.com:kb","description":null}],"total":3}}}`})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs([]string{"marketplace", x.cmd, "--limit", "1", "--offset", "1", "--json", "--server", g.URL})
			if err := r.Execute(); err != nil {
				t.Fatal(err)
			}
			var v map[string]json.RawMessage
			_ = json.Unmarshal(c[x.op], &v)
			if string(v["limit"]) != "1" || string(v["offset"]) != "1" {
				t.Fatal(v)
			}
			var d struct {
				Items []map[string]json.RawMessage
				Total int
			}
			if err := json.Unmarshal([]byte(out.String()), &d); err != nil {
				t.Fatal(err)
			}
			if d.Total != 3 || len(d.Items) != 1 || len(d.Items[0]) != 4 {
				t.Fatal(out.String())
			}
			if strings.Contains(out.String(), "members") {
				t.Fatal(out.String())
			}
		})
	}
}
func TestDiscoveryRefusals1345(t *testing.T) {
	for _, a := range [][]string{{"find", "q", "--field", "bogus"}, {"find", "q", "--entity-type", ""}, {"find", "q", "--offset", "-1"}, {"marketplace", "agents", "--limit", "-1"}, {"marketplace", "orgs", "--offset", "-1"}} {
		g, c := captureGraphQL(t, nil)
		f, _ := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs(append(a, "--server", g.URL))
		wantExit(t, r.Execute(), exitcode.Usage)
		if len(c) > 0 {
			t.Fatal(c)
		}
	}
}

func TestFindScope1345(t *testing.T) {
	g, c := captureGraphQL(t, map[string]string{"GlobalSearch": `{"data":{"globalSearch":{"hits":[],"total":0,"degraded":[],"scope":{"kind":"named","label":"work","source":"app","ownerUrn":"hrn:app:acme.com:ops","memoryUrns":[],"droppedCount":1}}}}`})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"find", "q", "--scope", "work", "--app", "acme.com:ops", "--json", "--server", g.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(c["GlobalSearch"], &v); err != nil {
		t.Fatal(err)
	}
	if string(v["scope"]) != `"work"` || string(v["appRef"]) != `"hrn:app:acme.com:ops"` {
		t.Fatal(v)
	}
	if !strings.Contains(out.String(), `"droppedCount": 1`) || !strings.Contains(out.String(), `"memoryUrns": []`) {
		t.Fatal(out.String())
	}
}
func TestCatalogueCount1345(t *testing.T) {
	g, c := captureGraphQL(t, map[string]string{"MarketplaceAgents": `{"data":{"marketplaceAgents":{"items":[],"total":9}}}`})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"marketplace", "agents", "--limit", "0", "--json", "--server", g.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(c["MarketplaceAgents"], &v); err != nil {
		t.Fatal(err)
	}
	if string(v["limit"]) != "0" || !strings.Contains(out.String(), `"items": []`) {
		t.Fatal(out.String(), v)
	}
}

func TestDiscoverySignedOut1345(t *testing.T) {
	for _, a := range [][]string{{"marketplace", "agents"}, {"marketplace", "memories"}, {"marketplace", "orgs"}, {"find", "q"}} {
		g, c := captureGraphQL(t, map[string]string{"MarketplaceAgents": `{"data":{"marketplaceAgents":{"items":[],"total":0}}}`, "MarketplaceMemories": `{"data":{"marketplaceMemories":{"items":[],"total":0}}}`, "MarketplaceOrganizations": `{"data":{"marketplaceOrganizations":{"items":[],"total":0}}}`, "GlobalSearch": `{"data":{"globalSearch":{"hits":[],"total":0,"degraded":[],"scope":null}}}`})
		f, _ := testFactory(t)
		t.Setenv("HADRON_TOKEN", "")
		r := NewRootCmd(f)
		r.SetArgs(append(a, "--server", g.URL))
		if err := r.Execute(); err != nil {
			t.Fatal(a, err)
		}
		if len(c) != 1 {
			t.Fatal(a, c)
		}
	}
}
func TestDiscoveryFailures1345(t *testing.T) {
	for _, tt := range []struct {
		args     []string
		op, body string
		code     int
	}{
		{[]string{"find", "q"}, "GlobalSearch", `{"data":{"globalSearch":null}}`, exitcode.Unavailable},
		{[]string{"marketplace", "agents"}, "MarketplaceAgents", `{"data":{"marketplaceAgents":null}}`, exitcode.Unavailable},
		{[]string{"marketplace", "memories"}, "MarketplaceMemories", `{"data":{"marketplaceMemories":{"items":null,"total":0}}}`, exitcode.Unavailable},
		{[]string{"marketplace", "orgs"}, "MarketplaceOrganizations", `{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`, exitcode.Forbidden},
	} {
		g, c := captureGraphQL(t, map[string]string{tt.op: tt.body})
		f, out := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs(append(tt.args, "--server", g.URL))
		wantExit(t, r.Execute(), tt.code)
		if out.Len() != 0 {
			t.Fatal(out.String())
		}
		if len(c) != 1 {
			t.Fatal(c)
		}
	}
}

func TestCatalogueDescriptions1345(t *testing.T) {
	g, _ := captureGraphQL(t, map[string]string{"MarketplaceAgents": `{"data":{"marketplaceAgents":{"items":[{"id":"a","name":"A","urn":"hrn:agent:acme.com:a","description":"public description","members":[{"email":"MUST-NOT-ECHO"}]},{"id":"b","name":"B","urn":"hrn:agent:acme.com:b","description":null}],"total":2}}}`})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"marketplace", "agents", "--json", "--server", g.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var d struct{ Items []map[string]json.RawMessage }
	if err := json.Unmarshal([]byte(out.String()), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != 2 || string(d.Items[0]["description"]) != `"public description"` || string(d.Items[1]["description"]) != "null" || strings.Contains(out.String(), "MUST-NOT-ECHO") {
		t.Fatal(out.String())
	}
}
