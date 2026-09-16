package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const searchEnvelope = `{"data":{"findNodes":{"total":2,"degraded":null,"reason":null,"hits":[
	{"score":0.91,"vector":{"abstractStale":true},"node":{"id":"n1","memoryId":"mem1","loc":"services:secureid:user-reporting",
		"name":"Reporting a user","nodeType":"finding","tags":["moderation"],
		"description":"How users report abuse","abstract":"Full flow…","updatedAt":"2026-07-05T00:00:00Z"}},
	{"score":null,"vector":null,"node":{"id":"n2","memoryId":"mem2","loc":"report-user-flow",
		"name":"Report user (client flow)","nodeType":"info","tags":[],
		"description":null,"abstract":null,"updatedAt":"2026-07-04T00:00:00Z"}}
]}}}`

type searchVars struct {
	Query  *string `json:"query"`
	Mode   *string `json:"mode"`
	Limit  *int    `json:"limit"`
	Filter struct {
		MemoryIds  []string       `json:"memoryIds"`
		LocPrefix  string         `json:"locPrefix"`
		ObjectType *string        `json:"objectType"`
		Where      map[string]any `json:"where"`
	} `json:"filter"`
	SortProperty map[string]any `json:"sortProperty"`
}

func TestSearchRankedJSON(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SearchNodes": searchEnvelope,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "report a bad actor", "-m", "micromentor.org::mmdata", "-m", "micromentor.org::mm-app", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var result struct {
		Hits []struct {
			Score         *float64 `json:"score"`
			Loc           string   `json:"loc"`
			Abstract      *string  `json:"abstract"`
			AbstractStale bool     `json:"abstractStale"`
		} `json:"hits"`
		Total *int `json:"total"`
	}
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(result.Hits) != 2 || result.Total == nil || *result.Total != 2 {
		t.Fatalf("unexpected envelope: %s", out.String())
	}
	if result.Hits[0].Score == nil || *result.Hits[0].Score != 0.91 || !result.Hits[0].AbstractStale {
		t.Errorf("first hit should keep score + abstractStale: %s", out.String())
	}
	if result.Hits[0].Abstract == nil || *result.Hits[0].Abstract == "" {
		t.Errorf("abstract should be included in --json: %s", out.String())
	}
	if result.Hits[1].Score != nil {
		t.Errorf("nil score must stay null, got %v", *result.Hits[1].Score)
	}

	var vars searchVars
	_ = json.Unmarshal(captured["SearchNodes"], &vars)
	if vars.Query == nil || *vars.Query != "report a bad actor" {
		t.Errorf("query not sent, got %v", vars.Query)
	}
	if vars.Mode == nil || *vars.Mode != "hybrid" {
		t.Errorf("default mode should be hybrid, got %v", vars.Mode)
	}
	if len(vars.Filter.MemoryIds) != 2 {
		t.Errorf("repeatable -m should map to filter.memoryIds, got %v", vars.Filter.MemoryIds)
	}
	if vars.Limit == nil || *vars.Limit != 15 {
		t.Errorf("default limit should be 15, got %v", vars.Limit)
	}
}

func TestSearchTableOutput(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"SearchNodes": searchEnvelope,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "report a bad actor", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "0.910") || !strings.Contains(text, "services:secureid:user-reporting") {
		t.Errorf("table should show score + loc:\n%s", text)
	}
	if !strings.Contains(text, "-") {
		t.Errorf("nil score should render as '-':\n%s", text)
	}
}

func TestSearchLongIncludesAbstract(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"SearchNodes": searchEnvelope,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "report a bad actor", "--long", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Full flow…") || !strings.Contains(out.String(), "abstract may be stale") {
		t.Errorf("--long should print abstract + staleness note:\n%s", out.String())
	}
}

func TestSearchDegradedNote(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"SearchNodes": `{"data":{"findNodes":{"total":0,"degraded":"no_vector_index","reason":"memory has no vector index","hits":[]}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "anything", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	errOut, ok := f.IOStreams.ErrOut.(*strings.Builder)
	if !ok {
		t.Fatalf("test factory ErrOut is not a strings.Builder")
	}
	if !strings.Contains(errOut.String(), "no_vector_index") {
		t.Errorf("degraded note should go to stderr, got: %q", errOut.String())
	}
}

func TestSearchInvalidModeIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "anything", "--mode", "psychic"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected usage error")
	}
	if exitCodeFor(err) != exitcode.Usage {
		t.Errorf("invalid --mode should be a usage error, got %v", err)
	}
}

// --mode maps to the wire enum (lowercase) for every non-default mode.
func TestSearchModeMapping(t *testing.T) {
	for _, mode := range []string{"keyword", "vector", "regex"} {
		gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchEnvelope})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"search", "x", "--mode", mode, "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("mode %s: execute: %v", mode, err)
		}
		var vars searchVars
		_ = json.Unmarshal(captured["SearchNodes"], &vars)
		if vars.Mode == nil || *vars.Mode != mode {
			t.Errorf("--mode %s should send %q, got %v", mode, mode, vars.Mode)
		}
	}
}

// #265: search --where / --object-type / --sort-property compose on top of a
// ranked query (parity with the server #719/#725/#739 surfaces). The predicate
// reaches filter.where, the facet lands on filter.objectType, sortProperty is a
// top-level arg, and unset leaf operators are omitted (never null).
func TestSearchWherePredicate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchEnvelope})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"search", "pricing",
		"--object-type", "insight",
		"--where", `{"path":["source"],"eq":"substack"}`,
		"--sort-property", `{"path":["rank"],"as":"number","direction":"desc"}`,
		"--server", gql.URL,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if raw := string(captured["SearchNodes"]); strings.Contains(raw, `"ne":null`) || strings.Contains(raw, `"lt":null`) {
		t.Fatalf("unset operators leaked as null — omitempty broken:\n%s", raw)
	}
	var vars searchVars
	if err := json.Unmarshal(captured["SearchNodes"], &vars); err != nil {
		t.Fatalf("unmarshal vars: %v", err)
	}
	if vars.Filter.ObjectType == nil || *vars.Filter.ObjectType != "insight" {
		t.Errorf("--object-type should map to filter.objectType, got %v", vars.Filter.ObjectType)
	}
	if vars.Filter.Where["eq"] != "substack" {
		t.Errorf("--where should reach filter.where, got %v", vars.Filter.Where)
	}
	if _, present := vars.Filter.Where["ne"]; present {
		t.Errorf("unset operators must be omitted, got %v", vars.Filter.Where)
	}
	if vars.SortProperty["direction"] != "desc" {
		t.Errorf("--sort-property should reach the wire, got %v", vars.SortProperty)
	}
}

func TestSearchWhereMalformedIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "x", "--where", `{"path":["x"],`})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("malformed --where should be a usage error, got %v", err)
	}
}

func TestSearchEmptyQueryIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "   "})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("whitespace-only query should be a usage error, got %v", err)
	}
}

func TestSearchNegativeLimitOffsetAreUsageErrors(t *testing.T) {
	for _, arg := range [][]string{{"--limit", "-1"}, {"--offset", "-1"}} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"search", "x"}, arg...))
		err := root.Execute()
		if err == nil || exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v should be a usage error, got %v", arg, err)
		}
	}
}

// searchScopedJSON is a findNodes result that RAN UNDER a scope and could not
// read part of it — the shape every disclosure assertion below turns on.
const searchScopedJSON = `{"data":{"findNodes":{
	"total":1,"degraded":null,"reason":null,
	"scope":{"kind":"SCOPE","label":"research","source":"APP",
		"ownerUrn":"hrn:app:acme.com:dev","memoryUrns":["hrn:mem:acme.com:papers"],
		"droppedCount":2},
	"hits":[{"score":0.9,"vector":{"abstractStale":false},"node":{
		"id":"n1","memoryId":"m1","loc":"findings:x","name":"X","nodeType":"info",
		"tags":[],"description":null,"abstract":null,"updatedAt":"2026-09-16T00:00:00Z"}}]}}}`

// TestSearchScopePassesTheStringThrough — the CLI must not interpret --scope.
//
// The server accepts a scope id, a bare name, `app` or `global` and resolves
// all four itself. Anything the CLI did to the string first would be a second
// implementation of the resolution ladder, so the assertion is that the value
// arrives verbatim.
func TestSearchScopePassesTheStringThrough(t *testing.T) {
	for _, want := range []string{"research", "global", "app", "0123456789abcdef0123456789abcdef"} {
		t.Run(want, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"search", "q", "--scope", want, "--app", "hrn:app:acme.com:dev", "--json", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var vars map[string]any
			_ = json.Unmarshal(captured["SearchNodes"], &vars)
			if vars["scope"] != want {
				t.Errorf("scope reached the wire as %v, want %q verbatim", vars["scope"], want)
			}
		})
	}
}

// TestSearchOmitsScopeWhenUnset — an omitted scope is the pre-049 behaviour
// (your whole accessible set). Sending an explicit null is a different request,
// so the key must be ABSENT, which a decode cannot distinguish from null.
func TestSearchOmitsScopeWhenUnset(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "q", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(string(captured["SearchNodes"]), `"scope"`) {
		t.Errorf("an unset --scope must be omitted, not sent: %s", captured["SearchNodes"])
	}
}

// TestSearchDisclosesTheScopeItRanUnder.
//
// Under a lens, "1 hit" means something different than it does over everything
// the caller can read, and droppedCount says how much of the lens was
// invisible. All three output branches are asserted separately: --json, the
// table, and --long, because they are three code paths and the header must
// precede the hits in each.
func TestSearchDisclosesTheScopeItRanUnder(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"search", "q", "--scope", "research", "--app", "hrn:app:acme.com:dev", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var dto struct {
			Scope *struct {
				Kind         string   `json:"kind"`
				Label        string   `json:"label"`
				Source       string   `json:"source"`
				MemoryURNs   []string `json:"memoryUrns"`
				DroppedCount int      `json:"droppedCount"`
			} `json:"scope"`
		}
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
			t.Fatalf("decode: %v — %s", err, out.String())
		}
		if dto.Scope == nil {
			t.Fatal("--json must carry the scope the search ran under")
		}
		if dto.Scope.Source != "APP" {
			t.Errorf("source = %q, want APP — the server reports which rung it used", dto.Scope.Source)
		}
		if dto.Scope.DroppedCount != 2 {
			t.Errorf("droppedCount = %d, want 2", dto.Scope.DroppedCount)
		}
	})

	for _, mode := range []string{"table", "long"} {
		t.Run(mode, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"search", "q", "--scope", "research", "--app", "hrn:app:acme.com:dev", "--server", gql.URL}
			if mode == "long" {
				args = append(args, "--long")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			got := out.String()
			if !strings.Contains(got, "scope: research") {
				t.Errorf("%s branch must name the scope, got:\n%s", mode, got)
			}
			if !strings.Contains(got, "via APP") {
				t.Errorf("%s branch must name the rung that chose it, got:\n%s", mode, got)
			}
			if !strings.Contains(got, "not readable by you") {
				t.Errorf("%s branch must disclose the dropped memories, got:\n%s", mode, got)
			}
		})
	}
}

// TestSearchUnscopedPrintsNoScopeLine — an unscoped search must look exactly as
// it did before #578, or every existing agent parsing this output breaks.
func TestSearchUnscopedPrintsNoScopeLine(t *testing.T) {
	unscoped := strings.Replace(searchScopedJSON, `"scope":{"kind":"SCOPE","label":"research","source":"APP",
		"ownerUrn":"hrn:app:acme.com:dev","memoryUrns":["hrn:mem:acme.com:papers"],
		"droppedCount":2},`, `"scope":null,`, 1)
	gql, _ := captureGraphQL(t, map[string]string{"SearchNodes": unscoped})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "q", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out.String(), "scope:") {
		t.Errorf("an unscoped search must print no scope line, got:\n%s", out.String())
	}
}

// TestSearchScopeNeedingAppContextNamesAFlag.
//
// The server refuses these itself — in the vocabulary of other surfaces:
// "pass appRef (GraphQL) or select an App (MCP)". A CLI reader can type
// neither. Found by running the command, not reading it; same class as the
// `appRef` leak fixed on `hadron scope get` in #594.
//
// `global` is deliberately NOT guarded: it keys off the active organization,
// which the CLI cannot select until #578 slice 3.
func TestSearchScopeNeedingAppContextNamesAFlag(t *testing.T) {
	for _, scope := range []string{"research", "app"} {
		t.Run(scope, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"search", "q", "--scope", scope, "--server", gql.URL})
			err := root.Execute()
			if err == nil {
				t.Fatal("expected a refusal with no App context")
			}
			if !strings.Contains(err.Error(), "--app") {
				t.Errorf("the message must name a flag the reader can type, got: %v", err)
			}
			for _, leaked := range []string{"appRef", "MCP", "GraphQL"} {
				if strings.Contains(err.Error(), leaked) {
					t.Errorf("the message must not name %s on this surface, got: %v", leaked, err)
				}
			}
			if _, ok := captured["SearchNodes"]; ok {
				t.Error("the refusal should precede the round trip")
			}
		})
	}
}

// TestSearchScopeIDAndGlobalNeedNoAppContext — an id is self-contained and
// `global` keys off the organization, so neither may be blocked by the guard.
func TestSearchScopeIDAndGlobalNeedNoAppContext(t *testing.T) {
	for _, scope := range []string{"global", "0123456789abcdef0123456789abcdef"} {
		t.Run(scope, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"search", "q", "--scope", scope, "--json", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("must reach the server without an App context: %v", err)
			}
			if _, ok := captured["SearchNodes"]; !ok {
				t.Error("the search should have been issued")
			}
		})
	}
}

// TestSearchSendsTheAppContextWithTheScope — @codex P1 on #595.
//
// `findNodes` REQUIRES appRef for `scope: "app"` and for a bare scope NAME (a
// name is unique only per owner). An earlier version validated that an App
// context existed and then discarded it, so those two forms reached the server
// without the thing needed to resolve them.
//
// My existing tests could not see it: they assert the scope string reaches the
// wire, and the guard asserts a refusal when no App is set. Neither looks at
// whether the validated App went WITH the scope — which is why this asserts the
// pair, not either half.
func TestSearchSendsTheAppContextWithTheScope(t *testing.T) {
	for _, scope := range []string{"research", "app"} {
		t.Run(scope, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{
				"search", "q", "--scope", scope,
				"--app", "hrn:app:acme.com:dev", "--json", "--server", gql.URL,
			})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var vars map[string]any
			_ = json.Unmarshal(captured["SearchNodes"], &vars)
			if vars["scope"] != scope {
				t.Fatalf("scope = %v, want %q", vars["scope"], scope)
			}
			if vars["appRef"] != "hrn:app:acme.com:dev" {
				t.Errorf("appRef = %v — the App context must travel WITH the scope, not merely be validated", vars["appRef"])
			}
		})
	}
}

// TestSearchOmitsTheAppContextWhenTheScopeDoesNotNeedIt — a scope id is
// self-contained and `global` keys off the organization, so neither should
// carry an App context it does not use.
func TestSearchOmitsTheAppContextWhenTheScopeDoesNotNeedIt(t *testing.T) {
	for _, scope := range []string{"global", "0123456789abcdef0123456789abcdef"} {
		t.Run(scope, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{
				"search", "q", "--scope", scope,
				"--app", "hrn:app:acme.com:dev", "--json", "--server", gql.URL,
			})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if strings.Contains(string(captured["SearchNodes"]), `"appRef"`) {
				t.Errorf("scope %q needs no App context: %s", scope, captured["SearchNodes"])
			}
		})
	}
}

// TestSearchGlobalSendsTheActiveOrg — #578 slice 3.
//
// `global` is defined by the server as the member's active-organization view,
// so it is the one scope that needs an orgId. Asserted on the wire because the
// output is identical either way: without it, the server falls back to a single
// membership and refuses for anyone in more than one organization.
func TestSearchGlobalSendsTheActiveOrg(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
	f, _ := testFactory(t)
	cfg, err := f.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if err := cfg.Set("org", "acme.com"); err != nil {
		t.Fatalf("set org: %v", err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "q", "--scope", "global", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["SearchNodes"], &vars)
	if vars["orgId"] != "acme.com" {
		t.Errorf("orgId = %v, want the active organization", vars["orgId"])
	}
}

// TestSearchSendsTheActiveOrgOnlyForGlobal.
//
// An active organization must not narrow an UNSCOPED search: that would change
// what a flagless `hadron search` returns the moment someone runs `org use`,
// silently and for every later invocation. `global` is the one scope defined in
// terms of the active org, so it is the only one that sends it.
func TestSearchSendsTheActiveOrgOnlyForGlobal(t *testing.T) {
	for _, args := range [][]string{
		{"search", "q"},
		{"search", "q", "--scope", "research", "--app", "hrn:app:acme.com:dev"},
		{"search", "q", "--scope", "0123456789abcdef0123456789abcdef"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
			f, _ := testFactory(t)
			cfg, err := f.Config()
			if err != nil {
				t.Fatalf("config: %v", err)
			}
			if err := cfg.Set("org", "acme.com"); err != nil {
				t.Fatalf("set org: %v", err)
			}
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, args...), "--json", "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if strings.Contains(string(captured["SearchNodes"]), `"orgId"`) {
				t.Errorf("an active org must not narrow this search: %s", captured["SearchNodes"])
			}
		})
	}
}

// TestSearchAppliesTheDefaultScopeAndSaysSo — #578 slice 4.
//
// This is the only slice that changes what a FLAGLESS `hadron search` returns,
// so the disclosure is the feature, not decoration: a narrowing the reader did
// not ask for and cannot see is indistinguishable from missing data. Asserted
// in --json (selectedBy) and in the human header, which are separate paths.
func TestSearchAppliesTheDefaultScopeAndSaysSo(t *testing.T) {
	seed := func(t *testing.T) (*cmdutil.Factory, *strings.Builder) {
		t.Helper()
		f, out := testFactory(t)
		cfg, err := f.Config()
		if err != nil {
			t.Fatalf("config: %v", err)
		}
		if err := cfg.Set("scope", "research"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return f, out
	}

	t.Run("reaches the wire", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
		f, _ := seed(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"search", "q", "--app", "hrn:app:acme.com:dev", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var vars map[string]any
		_ = json.Unmarshal(captured["SearchNodes"], &vars)
		if vars["scope"] != "research" {
			t.Errorf("scope = %v, want the configured default", vars["scope"])
		}
	})

	t.Run("json says it came from config", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
		f, out := seed(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"search", "q", "--app", "hrn:app:acme.com:dev", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var dto struct {
			Scope *struct {
				SelectedBy string `json:"selectedBy"`
			} `json:"scope"`
		}
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if dto.Scope == nil || dto.Scope.SelectedBy != "config" {
			t.Errorf("selectedBy = %v, want \"config\" — an agent must be able to tell", dto.Scope)
		}
	})

	t.Run("human output says it and names the remedy", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
		f, out := seed(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"search", "q", "--app", "hrn:app:acme.com:dev", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "your default scope") {
			t.Errorf("the header must say the scope was not asked for, got:\n%s", got)
		}
		if !strings.Contains(got, "--scope") {
			t.Errorf("the header must name the override, got:\n%s", got)
		}
	})

	t.Run("an explicit --scope wins and is marked as a flag", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
		f, out := seed(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"search", "q", "--scope", "global", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var vars map[string]any
		_ = json.Unmarshal(captured["SearchNodes"], &vars)
		if vars["scope"] != "global" {
			t.Errorf("the flag must win over the default, got %v", vars["scope"])
		}
		var dto struct {
			Scope *struct {
				SelectedBy string `json:"selectedBy"`
			} `json:"scope"`
		}
		_ = json.Unmarshal([]byte(out.String()), &dto)
		if dto.Scope == nil || dto.Scope.SelectedBy != "flag" {
			t.Errorf("selectedBy = %v, want \"flag\"", dto.Scope)
		}
	})
}

// TestSearchWithNoDefaultScopeIsUnchanged — with nothing configured, a flagless
// search must behave exactly as it did before this slice.
func TestSearchWithNoDefaultScopeIsUnchanged(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchScopedJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "q", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(string(captured["SearchNodes"]), `"scope"`) {
		t.Errorf("no configured default must mean no scope on the wire: %s", captured["SearchNodes"])
	}
}
