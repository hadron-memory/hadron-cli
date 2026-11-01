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
