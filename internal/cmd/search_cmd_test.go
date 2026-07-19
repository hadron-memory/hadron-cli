package cmd

import (
	"encoding/json"
	"strings"
	"testing"

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
	if exitcode.FromError(err) != exitcode.Usage {
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
	if err == nil || exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("malformed --where should be a usage error, got %v", err)
	}
}

func TestSearchEmptyQueryIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"search", "   "})
	err := root.Execute()
	if err == nil || exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("whitespace-only query should be a usage error, got %v", err)
	}
}

func TestSearchNegativeLimitOffsetAreUsageErrors(t *testing.T) {
	for _, arg := range [][]string{{"--limit", "-1"}, {"--offset", "-1"}} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"search", "x"}, arg...))
		err := root.Execute()
		if err == nil || exitcode.FromError(err) != exitcode.Usage {
			t.Errorf("%v should be a usage error, got %v", arg, err)
		}
	}
}
