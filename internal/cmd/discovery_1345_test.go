package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestFind1345(t *testing.T) {
	for _, query := range []string{"0123456789abcdef0123456789abcdef", "hrn:mem:acme.com:kb", "plain name", "  x y  "} {
		g, c := captureGraphQL(t, map[string]string{"GlobalSearch": `{"data":{"globalSearch":{"hits":[{"id":"one","entityType":"user","name":null,"urn":null,"score":1,"matchedField":"pk","description":null,"memoryId":null,"organizationId":null}],"total":1,"degraded":[],"scope":null}}}`})
		f, out := testFactory(t)
		cfg, err := f.Config()
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range map[string]string{"app": "hrn:app:ambient.test:app", "org": "hrn:org:ambient.test", "scope": "ambient-scope"} {
			if err := cfg.Set(k, v); err != nil {
				t.Fatal(err)
			}
		}
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
		if string(v["limit"]) != "20" || string(v["offset"]) != "0" {
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
	r.SetArgs([]string{"find", "q", "--field", "name,urn", "--entity-type", "agent,user", "--org", "hrn:org:acme.com", "--limit", "7", "--offset", "3", "--server", g.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(c["GlobalSearch"], &v); err != nil {
		t.Fatal(err)
	}
	for k, w := range map[string]string{"fields": `["name","urn"]`, "entityTypes": `["agent","user"]`, "orgId": `"hrn:org:acme.com"`, "limit": "7", "offset": "3"} {
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
			if err := json.Unmarshal(c[x.op], &v); err != nil {
				t.Fatal(err)
			}
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
	for _, a := range [][]string{{"find", "q", "--field", "bogus"},
		{"find", "q", "--scope", ""},
		{"find", "q", "--org", ""},
		{"find", "q", "--app", ""}, {"find", "q", "--entity-type", ""}, {"find", "q", "--offset", "-1"}, {"marketplace", "agents", "--limit", "-1"}, {"marketplace", "orgs", "--offset", "-1"}} {
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
	g, c := captureGraphQL(t, map[string]string{"GlobalSearch": `{"data":{"globalSearch":{"hits":[],"total":0,"degraded":[],"scope":{"kind":"scope","label":"work","source":"call","ownerUrn":"hrn:app:acme.com:ops","memoryUrns":[],"droppedCount":1}}}}`})
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

func TestDiscoveryHuman1345(t *testing.T) {
	g, _ := captureGraphQL(t, map[string]string{"GlobalSearch": `{"data":{"globalSearch":{"hits":[{"id":"one","entityType":"user","name":null,"urn":null,"score":1,"matchedField":"pk","description":null,"memoryId":null,"organizationId":null}],"total":10,"degraded":["reduced fidelity"],"scope":{"kind":"scope","label":"Work","source":"session","ownerUrn":null,"memoryUrns":[],"droppedCount":2}}}}`})
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	r := NewRootCmd(f)
	r.SetArgs([]string{"find", "query", "--limit", "5", "--server", g.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "source: session") || strings.Contains(out.String(), "from --scope") || !strings.Contains(out.String(), "Ranked matches (bounded): 10") {
		t.Fatal(out.String())
	}
	if !strings.Contains(errOut.String(), "degraded: reduced fidelity") || !strings.Contains(errOut.String(), "--offset 1 --limit 5") {
		t.Fatal(errOut.String())
	}
}

func TestDiscoveryDegradedJSON1345(t *testing.T) {
	g, _ := captureGraphQL(t, map[string]string{"GlobalSearch": `{"data":{"globalSearch":{"hits":[],"total":0,"degraded":["content skipped"],"scope":null}}}`})
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	r := NewRootCmd(f)
	r.SetArgs([]string{"find", "q", "--json", "--server", g.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var d struct{ Degraded []string }
	if err := json.Unmarshal([]byte(out.String()), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Degraded) != 1 || d.Degraded[0] != "content skipped" || !strings.Contains(errOut.String(), "degraded: content skipped") {
		t.Fatal(out.String(), errOut.String())
	}
}

func TestDiscoveryTerminalCells1345(t *testing.T) {
	for _, tt := range []struct {
		args     []string
		op, body string
	}{
		{[]string{"find", "q"}, "GlobalSearch", `{"data":{"globalSearch":{"hits":[{"id":"one","entityType":"user","name":"A\u001b[31mB","urn":null,"score":1,"matchedField":"pk","description":null,"memoryId":null,"organizationId":null}],"total":1,"degraded":[],"scope":null}}}`},
		{[]string{"marketplace", "agents"}, "MarketplaceAgents", `{"data":{"marketplaceAgents":{"items":[{"id":"a","name":"A\u001b[31mB","urn":"hrn:agent:acme.com:a","description":null}],"total":1}}}`},
	} {
		g, _ := captureGraphQL(t, map[string]string{tt.op: tt.body})
		f, out := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs(append(tt.args, "--server", g.URL))
		if err := r.Execute(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "31mB") {
			t.Fatal(out.String())
		}
	}
}

// agentTaskHit is a task hit exactly as the server serves it (cli#1897,
// hadron-server#3945): the bare stored handle in the common optional `handle`
// field, the title in `name`, `urn` null in this slice, the title or handle
// match reported as matchedField `name`.
const agentTaskHit = `{"id":"01a2093fa3447c9bbd2e57b15dd8d67d","entityType":"agentTask","name":"Wire up the nightly digest","urn":null,"handle":"AT0.8313","score":2.5,"matchedField":"name","description":null,"memoryId":null,"organizationId":null}`

func findAgentTask(t *testing.T, query, body string, extra ...string) (string, string, map[string]json.RawMessage) {
	t.Helper()
	g, c := captureGraphQL(t, map[string]string{"GlobalSearch": body})
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	r := NewRootCmd(f)
	r.SetArgs(append([]string{"find", query, "--entity-type", "agentTask", "--server", g.URL}, extra...))
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(c["GlobalSearch"], &v); err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), v
}

// cli#1897: the eighth kind is accepted and sent, and the query selects the
// ruled common `handle` field (a hit field the CLI drops unless selected).
func TestFindAgentTaskRequest1897(t *testing.T) {
	_, _, v := findAgentTask(t, "AT0.8313", `{"data":{"globalSearch":{"hits":[],"total":0,"degraded":[],"scope":null}}}`, "--json")
	if string(v["entityTypes"]) != `["agentTask"]` {
		t.Fatalf("entityTypes = %s", v["entityTypes"])
	}
	if !strings.Contains(strings.Join(strings.Fields(gen.GlobalSearch_Operation), " "), "organizationId handle }") {
		t.Fatalf("the hits selection does not carry handle:\n%s", gen.GlobalSearch_Operation)
	}
}

// cli#1897: a task hit renders the server's handle, title, matched field and
// score as given, in --json and in the table; nothing is composed or parsed.
func TestFindAgentTaskRendering1897(t *testing.T) {
	body := `{"data":{"globalSearch":{"hits":[` + agentTaskHit + `],"total":1,"degraded":[],"scope":null}}}`
	js, _, _ := findAgentTask(t, "nightly digest", body, "--json")
	var d struct {
		Hits []struct {
			ID           string  `json:"id"`
			EntityType   string  `json:"entityType"`
			Handle       *string `json:"handle"`
			Name         *string `json:"name"`
			URN          *string `json:"urn"`
			MatchedField string  `json:"matchedField"`
			Score        float64 `json:"score"`
		}
		Total int
	}
	if err := json.Unmarshal([]byte(js), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Hits) != 1 || d.Total != 1 {
		t.Fatal(js)
	}
	h := d.Hits[0]
	if h.EntityType != "agentTask" || h.Handle == nil || *h.Handle != "AT0.8313" || h.Name == nil || *h.Name != "Wire up the nightly digest" || h.URN != nil || h.MatchedField != "name" || h.Score != 2.5 {
		t.Fatalf("hit = %+v\n%s", h, js)
	}
	// The key is always present (null for kinds without a handle), never omitted.
	if !strings.Contains(js, `"handle": "AT0.8313"`) && !strings.Contains(js, `"handle":"AT0.8313"`) {
		t.Fatal(js)
	}
	text, _, _ := findAgentTask(t, "nightly digest", body)
	for _, want := range []string{"HANDLE", "AT0.8313", "Wire up the nightly digest", "agentTask", "name", "Ranked matches (bounded): 1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("table lacks %q:\n%s", want, text)
		}
	}
	// urn is null in this slice: rendered as absent, never as a composed URN.
	if strings.Contains(text, "hrn:agenttask") {
		t.Fatal(text)
	}
}

// cli#1897: kinds without a handle keep a null handle (JSON) and a dash (table).
func TestFindNonTaskHandleNull1897(t *testing.T) {
	g, _ := captureGraphQL(t, map[string]string{"GlobalSearch": `{"data":{"globalSearch":{"hits":[{"id":"m1","entityType":"memory","name":"KB","urn":"hrn:mem:acme.com:kb","handle":null,"score":1,"matchedField":"name","description":null,"memoryId":null,"organizationId":null}],"total":1,"degraded":[],"scope":null}}}`})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"find", "kb", "--json", "--server", g.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var d struct{ Hits []map[string]json.RawMessage }
	if err := json.Unmarshal([]byte(out.String()), &d); err != nil {
		t.Fatal(err)
	}
	if got, ok := d.Hits[0]["handle"]; !ok || string(got) != "null" {
		t.Fatalf("handle = %s (present %v)", got, ok)
	}
}

// cli#1897 (sharpened, cor:api:050:02/:03): an outsider's, a userless caller's
// and a nonexistent handle's answers all arrive as the server's empty page; the
// CLI prints that page and nothing else, in both renderings, and computes no
// total or next-page hint of its own. It also runs no per-hit enrichment (only
// GlobalSearch is stubbed: any other operation would fail the run).
func TestFindAgentTaskNoHitIndistinguishable1897(t *testing.T) {
	empty := `{"data":{"globalSearch":{"hits":[],"total":0,"degraded":[],"scope":null}}}`
	for _, q := range []string{"AT0.404404", "hrn:agenttask:hadronmemory.com:hadron-dev-team:at0.8313", "nonexistent handle"} {
		js, errOut, v := findAgentTask(t, q, empty, "--json")
		want, _ := json.Marshal(q)
		if string(v["query"]) != string(want) {
			t.Fatalf("query was rewritten: %s", v["query"])
		}
		var d struct {
			Hits  []json.RawMessage
			Total int
		}
		if err := json.Unmarshal([]byte(js), &d); err != nil || d.Hits == nil || len(d.Hits) != 0 || d.Total != 0 {
			t.Fatalf("%v\n%s", err, js)
		}
		if errOut != "" {
			t.Fatalf("stderr = %q", errOut)
		}
		text, errOut, _ := findAgentTask(t, q, empty)
		if !strings.Contains(text, "Ranked matches (bounded): 0") || strings.Contains(text, "agentTask") || errOut != "" {
			t.Fatalf("%q\n%s", errOut, text)
		}
	}
}

// cli#1897: the printed total and the next-page hint are the server's, even
// when the page shows fewer hits than the total (the server may hide some).
func TestFindAgentTaskTotalAsGiven1897(t *testing.T) {
	body := `{"data":{"globalSearch":{"hits":[` + agentTaskHit + `],"total":7,"degraded":[],"scope":null}}}`
	text, errOut, _ := findAgentTask(t, "digest", body, "--limit", "1")
	if !strings.Contains(text, "Ranked matches (bounded): 7") || !strings.Contains(errOut, "showing 1 of 7") || !strings.Contains(errOut, "--offset 1 --limit 1") {
		t.Fatalf("%s\n%s", text, errOut)
	}
}

// cli#1897: agentTask is listed in the flag help, find --help and the agentic
// contract beside the other kinds, and the refusal still names bad values.
func TestFindAgentTaskHelp1897(t *testing.T) {
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"find", "--help"})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "aiServiceConfig,user,agentTask") {
		t.Fatalf("--entity-type help omits agentTask:\n%s", out.String())
	}
	b, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "[--entity-type organization,memory,node,agent,app,aiServiceConfig,user,agentTask]") {
		t.Fatal("agentic-usage.md find synopsis omits agentTask")
	}
	g, c := captureGraphQL(t, map[string]string{})
	f2, _ := testFactory(t)
	r2 := NewRootCmd(f2)
	r2.SetArgs([]string{"find", "x", "--entity-type", "agentTasks", "--server", g.URL})
	wantExit(t, r2.Execute(), exitcode.Usage)
	if len(c) > 0 {
		t.Fatal(c)
	}
}
