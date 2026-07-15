package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/spec"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const specMem = "micromentor.org::platform-specs"

func specNodeList(loc, tags string) string {
	return fmt.Sprintf(`{"id":"id-%s","memoryId":"mem1","loc":%q,"name":%q,"nodeType":"info","tags":%s,"updatedAt":"2026-06-14T00:00:00Z"}`,
		loc, loc, loc+" — T", tags)
}

// specBatchNode is one node in a nodeBatch response — the projection the
// --prefix path reads. Rubric-clean (passes lintNode with no findings) for a
// level-3 loc under msg:010: name prefix, spec+p1 tags, abstract, an
// invalidates section, data.version, and a toc edge to the parent msg:010.
func specBatchNode(loc string) string {
	return specBatchNodeWithTags(loc, `["spec","p1"]`)
}

func specBatchNodeWithTags(loc, tags string) string {
	return fmt.Sprintf(`{"id":"id-%s","memoryId":"mem1","loc":%q,"name":%q,"alias":null,"nodeType":"info",`+
		`"description":null,"abstract":"Win back users who never engaged after signup.","abstractOriginHash":null,`+
		`"tags":%s,"seq":null,"data":{"version":"0.0.1"},"properties":null,`+
		`"content":"# spec\n\n## Definition\nx\n\n## Rule\nx\n\n## Durable vs tunable\nx\n\n## What invalidates this spec\nChanges.\n",`+
		`"updatedAt":"2026-06-14T00:00:00Z",`+
		`"outgoingEdges":[{"label":"p1: W2","priority":0,"condition":null,"target":{"id":"f1","loc":"msg:010","memoryId":"mem1"}}],`+
		`"incomingEdges":[]}`,
		loc, loc, loc+" — W2", tags)
}

func specBatchHeaderNode(loc, title, tags string) string {
	return fmt.Sprintf(`{"id":"id-%s","memoryId":"mem1","loc":%q,"name":%q,"alias":null,"nodeType":"info",`+
		`"description":null,"abstract":null,"abstractOriginHash":null,`+
		`"tags":%s,"seq":null,"data":null,"properties":null,`+
		`"content":"# %s — %s\n","updatedAt":"2026-06-14T00:00:00Z",`+
		`"outgoingEdges":[],"incomingEdges":[]}`,
		loc, loc, loc+" — "+title, tags, loc, title)
}

func specBatchResp(locs ...string) string {
	nodes := make([]string, len(locs))
	for i, loc := range locs {
		nodes[i] = specBatchNode(loc)
	}
	return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` +
		strings.Join(nodes, ",") + `]}}}`
}

// A rubric-clean spec detail (msg:010:02) — passes lintNode with no findings.
const cleanSpecDetail = `{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2",` +
	`"description":null,"abstract":"Win back users who never engaged after signup.","abstractOriginHash":null,` +
	`"nodeType":"info","tags":["spec","p1","messaging"],` +
	`"content":"# msg:010:02 — W2\n\n## Definition\nThe nudge.\n\n## Rule & examples\nDetails.\n\n## Durable vs tunable\nx\n\n## What invalidates this spec\nChanges.\n",` +
	`"data":{"version":"0.0.1"},"seq":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z",` +
	`"outgoingEdges":[{"id":"e1","label":"p1: W2","priority":0,"target":{"id":"f1","loc":"msg:010","memoryId":"mem1"}}],` +
	`"incomingEdges":[]}`

// A spec detail missing abstract and the "what invalidates" section.
const badSpecDetail = `{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2",` +
	`"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"info","tags":["spec","p1"],` +
	`"content":"# msg:010:02 — W2\n","data":null,"seq":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z",` +
	`"outgoingEdges":[],"incomingEdges":[]}`

const resolveSpecJSON = `{"data":{"resolveUrn":{"id":"sp1","kind":"node","memoryId":"mem1"}}}`

// A clean product-rooted module header (cli:cha), used to prove --module product
// inference lints the right node (#99 item 4).
const cliChaModuleDetail = `{"id":"id-cli:cha","memoryId":"mem1","loc":"cli:cha","name":"cli:cha — Chat",` +
	`"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"info","tags":["spec","p0"],` +
	`"content":"# cli:cha — Chat\n","data":null,"seq":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z",` +
	`"outgoingEdges":[],"incomingEdges":[]}`

func TestSpecLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg:010:01", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "ls", "-m", specMem, "--prefix", "msg:010", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "msg:010:01") || !strings.Contains(out.String(), "msg:010:02") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars findNodesVars
	_ = json.Unmarshal(captured["FindNodes"], &vars)
	if vars.Filter.LocPrefix != "msg:010" {
		t.Errorf("prefix = %q", vars.Filter.LocPrefix)
	}
	if len(vars.Filter.Tags) != 1 || vars.Filter.Tags[0] != "spec" {
		t.Errorf("ls should filter to spec tag, got %v", vars.Filter.Tags)
	}
}

func TestSpecGet(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "get", "msg:010:02", "-m", specMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Win back users") || !strings.Contains(text, "Lint: ✓ ok") {
		t.Errorf("unexpected get output:\n%s", text)
	}
	var vars struct {
		Urn string `json:"urn"`
	}
	_ = json.Unmarshal(captured["ResolveUrn"], &vars)
	if vars.Urn != "hrn:node:"+specMem+"::msg:010:02" {
		t.Errorf("resolveUrn got %q", vars.Urn)
	}
}

func TestSpecGetRejectsMalformedCitation(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "get", "register", "-m", specMem, "--server", "http://127.0.0.1:1"})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("malformed spec citation should be Usage, got %d", got)
	}
}

func TestSpecGetRejectsNonSpecNode(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    linkNonSpecDetail,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "get", "msg:010:02", "-m", specMem, "--server", gql.URL})
	err := root.Execute()
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Fatalf("non-spec node through spec get should be Usage, got %d", got)
	}
	if strings.Contains(err.Error(), "edge add") {
		t.Fatalf("spec get non-spec error should be generic, got %q", err)
	}
}

// #69 item 5: spec get surfaces the node's `data` block in the text view.
func TestSpecGetSurfacesData(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "get", "msg:010:02", "-m", specMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Data:") || !strings.Contains(text, "0.0.1") {
		t.Errorf("spec get should surface the data block:\n%s", text)
	}
}

func TestSpecGetPrefix(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg:010:01", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
		"NodeBatch": specBatchResp("msg:010:01", "msg:010:02"),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "get", "--prefix", "msg:010", "-m", specMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "2 spec(s) under msg:010") {
		t.Errorf("missing prefix count header:\n%s", text)
	}
	if !strings.Contains(text, "Win back users") || !strings.Contains(text, "Lint: ✓ ok") {
		t.Errorf("prefix dump should render each node's detail:\n%s", text)
	}
	// Default prefix mode pages the listing to exhaustion (#23) via scanAllNodes
	// — a 500-wide page, not the old single capped Nodes call.
	var nodesVars findNodesVars
	_ = json.Unmarshal(captured["FindNodes"], &nodesVars)
	if nodesVars.Filter.LocPrefix != "msg:010" || len(nodesVars.Filter.Tags) != 1 || nodesVars.Filter.Tags[0] != "spec" {
		t.Errorf("prefix/tags wrong: %+v", nodesVars.Filter)
	}
	if nodesVars.Limit == nil || *nodesVars.Limit != 500 {
		t.Errorf("default prefix mode should page by 500 (exhaustive), got limit=%v", nodesVars.Limit)
	}
	// Details come from one bulk nodeBatch call, not a GetNodeById per spec.
	var batchVars struct {
		Ids []string `json:"ids"`
	}
	_ = json.Unmarshal(captured["NodeBatch"], &batchVars)
	if len(batchVars.Ids) != 2 {
		t.Errorf("expected 2 ids in the bulk read, got %v", batchVars.Ids)
	}
}

func TestSpecGetPrefixExplicitPage(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
		"NodeBatch": specBatchResp("msg:010:02"),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "get", "--prefix", "msg:010", "--limit", "1", "-m", specMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "1 spec(s) under msg:010") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
	// An explicit --limit is honored verbatim as a single page, not the
	// 500-wide exhaustive scan.
	var vars findNodesVars
	_ = json.Unmarshal(captured["FindNodes"], &vars)
	if vars.Limit == nil || *vars.Limit != 1 {
		t.Errorf("explicit --limit should pass through verbatim, got %v", vars.Limit)
	}
}

func TestSpecGetCitationXorPrefix(t *testing.T) {
	// Exactly one of <citation> / --prefix is required: neither and both error.
	for _, args := range [][]string{
		{"spec", "get", "-m", specMem},
		{"spec", "get", "msg:010:02", "--prefix", "msg:010", "-m", specMem},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Errorf("args %v: expected a usage error (need exactly one of <citation>/--prefix)", args)
		}
	}
}

func TestSpecFindSemanticDefault(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodeSearch":{"degraded":null,"reason":null,"nodes":[` +
			specNodeList("msg:010:02", `["spec","p1"]`) + `,` +
			specNodeList("register", `["index"]`) + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "find", "win back users", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "msg:010:02") {
		t.Errorf("missing spec hit: %s", out.String())
	}
	if strings.Contains(out.String(), "register") {
		t.Errorf("non-spec hit should be filtered out: %s", out.String())
	}
	var vars findNodesVars
	_ = json.Unmarshal(captured["FindNodes"], &vars)
	if vars.Query == nil || *vars.Query != "win back users" {
		t.Errorf("find should pass the query, got %v", vars.Query)
	}
	if vars.Mode == nil || *vars.Mode != "hybrid" {
		t.Errorf("default find should use hybrid mode, got %v", vars.Mode)
	}
	const wantSpecFindPageSize = 50
	if vars.Limit == nil || *vars.Limit != wantSpecFindPageSize {
		t.Errorf("default find should oversample raw hits with page size %d, got %v", wantSpecFindPageSize, vars.Limit)
	}
	if vars.Offset == nil || *vars.Offset != 0 {
		t.Errorf("default find should start at offset 0, got %v", vars.Offset)
	}
	if len(vars.Filter.MemoryIds) != 1 || vars.Filter.MemoryIds[0] != specMem {
		t.Errorf("memory scope should map to filter.memoryIds, got %v", vars.Filter.MemoryIds)
	}
}

func TestSpecFindMatchExactly(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "find", "msg:010", "-m", specMem, "--match-exactly", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars findNodesVars
	_ = json.Unmarshal(captured["FindNodes"], &vars)
	if vars.Query == nil || *vars.Query != "msg:010" {
		t.Errorf("query = %v", vars.Query)
	}
	if vars.Mode == nil || *vars.Mode != "regex" {
		t.Errorf("--match-exactly should use regex mode, got %v", vars.Mode)
	}
	found := false
	for _, tag := range vars.Filter.Tags {
		if tag == "spec" {
			found = true
		}
	}
	if !found {
		t.Errorf("--match-exactly should filter to spec tag, got %v", vars.Filter.Tags)
	}
}

func TestSpecRegisterCheckDrift(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":  `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"reg1","kind":"node","memoryId":"mem1"}}}`,
		"GetNode": `{"data":{"node":{"id":"reg1","memoryId":"mem1","loc":"register","name":"register — R",` +
			`"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"info","tags":["index"],` +
			`"content":"## Service codes\n\n| Code | Service | Status |\n|---|---|---|\n| mat | matching | allocated |\n",` +
			`"data":null,"seq":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z","outgoingEdges":[],"incomingEdges":[]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "register", "-m", specMem, "--check", "--server", gql.URL})
	err := root.Execute()
	if exitcode.FromError(err) != exitcode.Conflict {
		t.Fatalf("expected Conflict exit on drift, got err=%v code=%d", err, exitcode.FromError(err))
	}
	if !strings.Contains(out.String(), "Drift") {
		t.Errorf("expected drift report:\n%s", out.String())
	}
}

func TestSpecNew(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:00", `["spec","p1"]`) + `]}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes":  scan,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:01","name":"msg:010:01 — Test","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"t1","kind":"node","memoryId":"mem1"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"new1","loc":"msg:010:01"},"target":{"id":"t1","loc":"msg:010"}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specMem, "--module", "msg", "--feature", "010", "--title", "Test", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var up struct {
		Input struct {
			Loc      string          `json:"loc"`
			Name     string          `json:"name"`
			Tags     []string        `json:"tags"`
			NodeType *string         `json:"nodeType"`
			Abstract *string         `json:"abstract"`
			Data     json.RawMessage `json:"data"`
			Seq      *int            `json:"seq"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured["CreateNode"], &up); err != nil {
		t.Fatalf("CreateNode vars: %v", err)
	}
	if up.Input.Loc != "msg:010:01" {
		t.Errorf("allocated loc = %q, want msg:010:01", up.Input.Loc)
	}
	// seq comes from the citation's numeric leaf (rule 01) so siblings sort (#40).
	if up.Input.Seq == nil || *up.Input.Seq != 1 {
		t.Errorf("seq = %v, want 1 (from rule leaf 01)", up.Input.Seq)
	}
	if up.Input.NodeType == nil || *up.Input.NodeType != "info" {
		t.Errorf("nodeType = %v", up.Input.NodeType)
	}
	if !strings.Contains(string(up.Input.Data), "version") {
		t.Errorf("data must carry a version, got %s", up.Input.Data)
	}
	if up.Input.Abstract == nil || *up.Input.Abstract == "" {
		t.Error("abstract must be set")
	}
	if len(up.Input.Tags) != 1 || up.Input.Tags[0] != "spec" {
		t.Errorf("new spec tags = %v, want [spec] (no read-priority p-level)", up.Input.Tags)
	}

	var dto struct {
		Citation string `json:"citation"`
		Edges    []struct {
			Label  string `json:"label"`
			Target string `json:"target"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Citation != "msg:010:01" {
		t.Errorf("citation = %q", dto.Citation)
	}
	var sawToC, sawInherit bool
	for _, e := range dto.Edges {
		if e.Label == "Test" && e.Target == "msg:010" {
			sawToC = true
		}
		if strings.Contains(e.Label, "inherits") && e.Target == "msg:010:00" {
			sawInherit = true
		}
	}
	if !sawToC || !sawInherit {
		t.Errorf("expected ToC + inheritance edges, got %v", dto.Edges)
	}
}

// #91 Bug 2: spec new with the memory given by PK must resolve it to the
// canonical <org>::<memory> so edge targets are valid FQNs. Previously it built
// "<pk>::<loc>", which failed FQN validation and left the node orphaned.
func TestSpecNewResolvesPKForEdgeTargets(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:010", `["spec"]`) + `,` + specNodeList("msg:010:00", `["spec"]`) + `]}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"Memories":   memListMicromentorJSON,
		"FindNodes":  scan,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:01","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"t1","kind":"node","memoryId":"mem1"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"new1","loc":"msg:010:01"},"target":{"id":"t1","loc":"msg:010"}}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// -m mem1 is the memory PK (matches memListMicromentorJSON's id).
	root.SetArgs([]string{"spec", "new", "-m", "mem1", "--module", "msg", "--feature", "010", "--title", "Test", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Urn string `json:"urn"`
	}
	if err := json.Unmarshal(captured["ResolveUrn"], &vars); err != nil {
		t.Fatalf("ResolveUrn vars: %v", err)
	}
	if strings.Contains(vars.Urn, "mem1::") {
		t.Errorf("edge target built from the raw PK, not the resolved memory: %q", vars.Urn)
	}
	if !strings.HasPrefix(vars.Urn, "hrn:node:micromentor.org::platform-specs::") {
		t.Errorf("edge target urn = %q, want the resolved <org>::<memory> FQN", vars.Urn)
	}
}

// #91 ask 3: a required ToC/inheritance edge that can't be wired makes spec new
// fail loudly (non-zero exit) instead of reporting success on a node it left
// silently orphaned.
func TestSpecNewFailsLoudOnSkippedEdge(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:010", `["spec"]`) + `,` + specNodeList("msg:010:00", `["spec"]`) + `]}}`
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":  scan,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:01","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"ResolveUrn": `{"data":{"resolveUrn":null}}`, // edge target won't resolve
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specMem, "--module", "msg", "--feature", "010", "--title", "Test", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "orphaned") {
		t.Fatalf("a skipped required edge must fail loudly, got %v", err)
	}
}

// #91 Bug 1: spec describe resolves a memory given by PK. Previously describe
// matched only a hand-normalized urn and reported "not found" for every form
// (URN, bare, PK, name).
func TestSpecDescribeResolvesByPK(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"Memories":  memListJSON,
		"GetMemory": memGetJSON(`null`),
		"FindNodes": `{"data":{"nodes":[` + specNodeList("cli", `["spec"]`) + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "describe", "-m", "mem1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("describe by PK should resolve, got %v", err)
	}
	if !strings.Contains(out.String(), "scheme") {
		t.Errorf("expected a scheme report, got %s", out.String())
	}
}

// #45 review: --abstract and --abstract-file are mutually exclusive, guarded
// on Changed() (so an explicit empty --abstract is caught too); fires before
// any GraphQL call.
func TestSpecNewRejectsAbstractAndAbstractFile(t *testing.T) {
	for _, abstract := range []string{"", "inline"} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"spec", "new", "-m", specMem, "--module", "msg", "--feature", "010", "--title", "T",
			"--abstract", abstract, "--abstract-file", "/tmp/x.md", "--server", "http://127.0.0.1:1"})
		if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
			t.Fatalf("--abstract %q + --abstract-file should be Usage, got %d", abstract, got)
		}
	}
}

func TestSpecNewDryRun(t *testing.T) {
	// Only the scan is mocked: any mutation would be an unexpected op and fail.
	scan := `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `]}}`
	gql, captured := captureGraphQL(t, map[string]string{"FindNodes": scan})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specMem, "--module", "msg", "--feature", "010", "--title", "Test", "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["CreateNode"]; ok {
		t.Error("dry-run must not call CreateNode")
	}
	if !strings.Contains(out.String(), "would create") {
		t.Errorf("unexpected dry-run output:\n%s", out.String())
	}
}

// #69 item 2: a scaffolded module root is a Features index, not the rule rubric.
func TestSpecNewModuleScaffoldsFeaturesIndex(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"FindNodes": `{"data":{"nodes":[]}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "--new-module", "--module", "brd",
		"--title", "Brand", "-m", specMem, "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "## Features") {
		t.Errorf("module-root scaffold should be a Features index:\n%s", got)
	}
	if strings.Contains(got, "## Definition") {
		t.Errorf("module root should not get the rule rubric:\n%s", got)
	}
}

// A scaffolded feature root leads with its load-bearing point + a rule list.
func TestSpecNewFeatureScaffoldsRuleList(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("msg", `["spec"]`) + `]}}`
	gql, _ := captureGraphQL(t, map[string]string{"FindNodes": scan})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "--new-feature", "--module", "msg",
		"--title", "Color palette", "-m", specMem, "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "load-bearing point") || !strings.Contains(got, "## Rules") {
		t.Errorf("feature-root scaffold should lead with the load-bearing point + a Rules list:\n%s", got)
	}
}

func TestSpecNewMissingParent(t *testing.T) {
	// Module exists but the feature does not.
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specMem, "--module", "msg", "--feature", "010", "--title", "Test", "--server", gql.URL})
	err := root.Execute()
	if exitcode.FromError(err) != exitcode.NotFound {
		t.Fatalf("expected NotFound for missing feature, got err=%v code=%d", err, exitcode.FromError(err))
	}
}

const specProductMem = "hadronmemory.com::platform-specs"

// memListJSON is a MyMemories response whose urn matches specProductMem. The
// urn is the server's real fully-qualified hrn:memory: form (issue #91: the
// resolver must match against this, not a hand-normalized single-colon urn).
const memListJSON = `{"data":{"memories":{"total":1,"items":[{"id":"mem1","urn":"hrn:memory:hadronmemory.com::platform-specs","name":"Platform Specs","shortDescription":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org1","isEncrypted":false,"updatedAt":"2026-06-14T00:00:00Z"}]}}}`

// memGetJSON is a GetMemory response with the given data bag (raw JSON, e.g.
// `null` or `{"spec":{"scheme":"product"}}`). vectorIndexEnabled is false, so it
// also exercises the spec-lint no-vector-index warning (#42).
func memGetJSON(data string) string {
	return `{"data":{"memory":{"id":"mem1","urn":"hadronmemory.com:platform-specs","name":"Platform Specs","shortDescription":null,"description":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"OK","vectorIndexEnabled":false,"data":` + data + `,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z"}}}`
}

// memGetVectorEnabledJSON / memGetNoVectorJSON are GetMemory responses that
// vary vectorIndexEnabled for the spec-lint vector-index probe (#42). The urn
// is irrelevant (GetMemory is looked up by id); resolveSpecMemoryID does the
// urn match against MyMemories.
const memGetVectorEnabledJSON = `{"data":{"memory":{"id":"mem1","urn":"x:y","name":"P","shortDescription":null,"description":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"OK","vectorIndexEnabled":true,"data":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z"}}}`

const memGetNoVectorJSON = `{"data":{"memory":{"id":"mem1","urn":"x:y","name":"P","shortDescription":null,"description":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"OK","vectorIndexEnabled":false,"data":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z"}}}`

// memListMicromentorJSON is a MyMemories response whose urn matches specMem
// (micromentor.org::platform-specs), in the server's real hrn:memory: form.
const memListMicromentorJSON = `{"data":{"memories":{"total":1,"items":[{"id":"mem1","urn":"hrn:memory:micromentor.org::platform-specs","name":"Platform Specs","shortDescription":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org1","isEncrypted":false,"updatedAt":"2026-06-14T00:00:00Z"}]}}}`

func TestSpecDescribeProduct(t *testing.T) {
	nodes := strings.Join([]string{
		specNodeList("cli", `["spec","p0"]`),
		specNodeList("cli:gen", `["spec","p0"]`),
		specNodeList("cli:cha", `["spec","p1"]`),
		specNodeList("cli:cha:010", `["spec","p1"]`),
		specNodeList("cli:cha:010:01", `["spec","p1"]`),
	}, ",")
	gql, _ := captureGraphQL(t, map[string]string{
		"Memories":  memListJSON,
		"GetMemory": memGetJSON(`null`),
		"FindNodes": `{"data":{"nodes":[` + nodes + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "describe", "-m", specProductMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Scheme   string   `json:"scheme"`
		Source   string   `json:"source"`
		Products []string `json:"products"`
		Modules  []string `json:"modules"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Scheme != "product" {
		t.Errorf("scheme = %q, want product", dto.Scheme)
	}
	if dto.Source != "derived" {
		t.Errorf("source = %q, want derived (memory data was null)", dto.Source)
	}
	if len(dto.Products) != 1 || dto.Products[0] != "cli" {
		t.Errorf("products = %v, want [cli]", dto.Products)
	}
	if len(dto.Modules) != 1 || dto.Modules[0] != "cli:cha" {
		t.Errorf("modules = %v, want [cli:cha]", dto.Modules)
	}
}

func TestSpecDescribeDeclare(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Memories":     memListJSON,
		"GetMemory":    memGetJSON(`null`),
		"UpdateMemory": `{"data":{"updateMemory":{"id":"mem1","urn":"hadronmemory.com:platform-specs","name":"P","shortDescription":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org1","isEncrypted":false,"data":{"spec":{"scheme":"product"}},"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"FindNodes":    `{"data":{"nodes":[]}}`, // empty corpus: the declared scheme is all there is
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "describe", "-m", specProductMem, "--declare", "product", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// UpdateMemory was called with a data bag declaring the scheme.
	var vars struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(captured["UpdateMemory"], &vars)
	if !strings.Contains(string(vars.Data), `"scheme":"product"`) {
		t.Errorf("UpdateMemory data must declare scheme product, got %s", vars.Data)
	}
	// Output reflects the declaration even though the corpus is empty.
	var dto struct {
		Scheme   string `json:"scheme"`
		Source   string `json:"source"`
		Declared string `json:"declared"`
		Derived  string `json:"derived"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Scheme != "product" || dto.Source != "declared" || dto.Declared != "product" || dto.Derived != "empty" {
		t.Errorf("declared describe = %+v", dto)
	}
}

// #69 item 1 (tail): --new-path scaffolds the whole chain in one call. No
// ResolveUrn is mocked — every fresh node's edges must wire by id.
func TestSpecNewPath(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":  `{"data":{"nodes":[]}}`,
		"CreateNode": `{"data":{"createNode":{"id":"n1","memoryId":"mem1","loc":"x","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"n1","loc":"x"},"target":{"id":"n1","loc":"y"}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "msg:010:01", "--new-path", "--title", "Send", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Citation string `json:"citation"`
		Also     []struct {
			Citation string `json:"citation"`
		} `json:"also"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Citation != "msg:010:01" {
		t.Errorf("primary should be the target, got %q", dto.Citation)
	}
	got := map[string]bool{}
	for _, a := range dto.Also {
		got[a.Citation] = true
	}
	for _, want := range []string{"msg", "msg:000", "msg:010", "msg:010:00"} {
		if !got[want] {
			t.Errorf("--new-path should also create %s; also=%v", want, dto.Also)
		}
	}
}

func TestSpecNewPathNoContract(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":  `{"data":{"nodes":[]}}`,
		"CreateNode": `{"data":{"createNode":{"id":"n1","memoryId":"mem1","loc":"x","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"n1","loc":"x"},"target":{"id":"n1","loc":"y"}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "msg:010:01", "--new-path", "--no-contract", "--title", "Send", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Also []struct {
			Citation string `json:"citation"`
		} `json:"also"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	got := map[string]bool{}
	for _, a := range dto.Also {
		if strings.HasSuffix(a.Citation, ":000") || strings.HasSuffix(a.Citation, ":00") {
			t.Errorf("--no-contract should not create contract %s", a.Citation)
		}
		got[a.Citation] = true
	}
	if !got["msg"] || !got["msg:010"] {
		t.Errorf("expected roots msg + msg:010, got %v", dto.Also)
	}
}

func TestSpecNewPathRejectsTierFlags(t *testing.T) {
	for _, extra := range [][]string{{"--module", "msg"}, {"--inherit", "msg:010:00"}, {"--new-module"}, {"--contract"}} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		args := append([]string{"spec", "new", "msg:010:01", "--new-path", "--title", "x", "-m", specMem, "--server", "http://127.0.0.1:1"}, extra...)
		root.SetArgs(args)
		if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
			t.Errorf("--new-path + %v should be Usage, got %d", extra, got)
		}
	}
}

func TestSpecNewPathTargetExists(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("msg:010:01", `["spec"]`) + `]}}`
	gql, _ := captureGraphQL(t, map[string]string{"FindNodes": scan})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "msg:010:01", "--new-path", "--title", "x", "-m", specMem, "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Conflict {
		t.Fatalf("an existing target should be Conflict, got %d", got)
	}
}

func TestSpecNewProduct(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes":  `{"data":{"nodes":[]}}`,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"cli","name":"cli — Hadron CLI","nodeType":"info","tags":["spec","p0"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specProductMem, "--new-product", "--product", "cli", "--title", "Hadron CLI", "--no-contract", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var up struct {
		Input struct {
			Loc string `json:"loc"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &up)
	if up.Input.Loc != "cli" {
		t.Errorf("product root loc = %q, want cli", up.Input.Loc)
	}
	var dto struct {
		Citation string          `json:"citation"`
		Edges    json.RawMessage `json:"edges"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Citation != "cli" {
		t.Errorf("citation = %q, want cli", dto.Citation)
	}
	if string(dto.Edges) != "[]" {
		t.Errorf("a product root has no ToC/inherit edges and must render edges: [], got %s", dto.Edges)
	}
}

func TestSpecNewProductModule(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("cli", `["spec","p0"]`) + `,` + specNodeList("cli:gen", `["spec","p0"]`) + `]}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes":  scan,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"cli:cha","name":"cli:cha — chat","nodeType":"info","tags":["spec","p0"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"t1","kind":"node","memoryId":"mem1"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"new1","loc":"cli:cha"},"target":{"id":"t1","loc":"cli"}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specProductMem, "--product", "cli", "--new-module", "--module", "cha", "--title", "chat command group", "--no-contract", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var up struct {
		Input struct {
			Loc string `json:"loc"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &up)
	if up.Input.Loc != "cli:cha" {
		t.Errorf("module loc = %q, want cli:cha", up.Input.Loc)
	}
	var dto struct {
		Edges []struct {
			Label  string `json:"label"`
			Target string `json:"target"`
		} `json:"edges"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	var sawToC, sawInherit bool
	for _, e := range dto.Edges {
		if e.Target == "cli" {
			sawToC = true
		}
		if strings.Contains(e.Label, "inherits") && e.Target == "cli:gen" {
			sawInherit = true
		}
	}
	if !sawToC {
		t.Errorf("expected ToC edge to product root cli, got %v", dto.Edges)
	}
	if !sawInherit {
		t.Errorf("expected inherit edge to product contract cli:gen, got %v", dto.Edges)
	}
}

// #69 item 1: --new-module also scaffolds the module's :000 contract (so
// features can inherit it) and wires its ToC edge to the new root.
func TestSpecNewModuleAutoContract(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes":  `{"data":{"nodes":[]}}`,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"brd","name":"brd — Brand","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"c1","loc":"brd:000"},"target":{"id":"new1","loc":"brd"}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "--new-module", "--module", "brd", "--title", "Brand", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Citation string `json:"citation"`
		Also     []struct {
			Citation string `json:"citation"`
			Edges    []struct {
				Target string `json:"target"`
			} `json:"edges"`
		} `json:"also"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Citation != "brd" {
		t.Errorf("primary citation = %q, want brd", dto.Citation)
	}
	if len(dto.Also) != 1 || dto.Also[0].Citation != "brd:000" {
		t.Fatalf("expected co-created contract brd:000 in .also, got %+v", dto.Also)
	}
	if len(dto.Also[0].Edges) != 1 || dto.Also[0].Edges[0].Target != "brd" {
		t.Errorf("contract should carry a ToC edge to brd, got %v", dto.Also[0].Edges)
	}
	// The contract's ToC edge is wired by id (no ResolveUrn for the fresh root).
	if _, ok := captured["ResolveUrn"]; ok {
		t.Error("the contract→root edge must be wired by id, not via resolveUrn")
	}
	var up struct {
		Input struct {
			Loc string `json:"loc"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &up)
	if up.Input.Loc != "brd:000" {
		t.Errorf("the last create should be the contract brd:000, got %q", up.Input.Loc)
	}
}

func TestSpecNewNoContract(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":  `{"data":{"nodes":[]}}`,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"brd","name":"brd — Brand","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "--new-module", "--module", "brd", "--title", "Brand", "--no-contract", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Also []json.RawMessage `json:"also"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if len(dto.Also) != 0 {
		t.Errorf("--no-contract should not co-create a contract, got %d", len(dto.Also))
	}
}

// --new-feature scaffolds the feature root and its :00 contract; the feature
// itself still inherits the module's :000.
func TestSpecNewFeatureAutoContract(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:000", `["spec"]`) + `]}}`
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":  scan,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"msg:010","name":"msg:010 — Palette","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"t1","kind":"node","memoryId":"mem1"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"new1","loc":"msg:010"},"target":{"id":"t1","loc":"msg"}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "--new-feature", "--module", "msg", "--title", "Palette", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Citation string `json:"citation"`
		Also     []struct {
			Citation string `json:"citation"`
		} `json:"also"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Citation != "msg:010" {
		t.Errorf("primary citation = %q, want msg:010", dto.Citation)
	}
	if len(dto.Also) != 1 || dto.Also[0].Citation != "msg:010:00" {
		t.Errorf("expected co-created feature contract msg:010:00, got %+v", dto.Also)
	}
}

func TestSpecNewProductContract(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("cli", `["spec","p0"]`) + `]}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes":  scan,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"cli:gen","name":"cli:gen — provisions","nodeType":"info","tags":["spec","p0"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"t1","kind":"node","memoryId":"mem1"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"new1","loc":"cli:gen"},"target":{"id":"t1","loc":"cli"}}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specProductMem, "--product", "cli", "--contract", "--title", "general provisions", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var up struct {
		Input struct {
			Loc string `json:"loc"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &up)
	if up.Input.Loc != "cli:gen" {
		t.Errorf("product contract loc = %q, want cli:gen", up.Input.Loc)
	}
}

func TestSpecNewReservedGenModule(t *testing.T) {
	// "gen" is reserved for the product contract — it can't be a module in a
	// product corpus. The guard fires before any GraphQL call.
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specProductMem, "--product", "cli", "--new-module", "--module", "gen", "--title", "nope"})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("--module gen in a product corpus should be Usage, got %d", got)
	}
}

func TestSpecNewModuleContract(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("msg", `["spec","p0"]`) + `]}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes":  scan,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"msg:000","name":"msg:000 — provisions","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"t1","kind":"node","memoryId":"mem1"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"new1","loc":"msg:000"},"target":{"id":"t1","loc":"msg"}}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specMem, "--module", "msg", "--contract", "--title", "messaging provisions", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var up struct {
		Input struct {
			Loc string `json:"loc"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &up)
	if up.Input.Loc != "msg:000" {
		t.Errorf("module contract loc = %q, want msg:000", up.Input.Loc)
	}
}

func TestSpecLintCitationAndFlags(t *testing.T) {
	// A <citation> argument and a scope flag are mutually exclusive (the guard
	// fires before any GraphQL call).
	gql, _ := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "msg:010:02", "--all", "-m", specMem, "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("citation + --all should be Usage, got %d", got)
	}
}

func TestSpecLintScopeNoMatch(t *testing.T) {
	// --module zzz matches nothing even after the sole product (cli) is
	// inferred: fail loudly (NotFound) instead of a misleading "0 OK".
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("cli:cha", `["spec","p1"]`) + `]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--module", "zzz", "-m", specProductMem, "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.NotFound {
		t.Fatalf("a --module scope matching nothing should be NotFound, got %d", got)
	}
}

func TestSpecLintInfersSoleProduct(t *testing.T) {
	// --module cha with no --product: in a corpus with exactly one product the
	// product (cli) is inferred and cli:cha is linted, instead of dead-ending
	// (#99 item 4).
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("cli:cha", `["spec","p0"]`) + `]}}`,
		"NodeBatch": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` +
			specBatchHeaderNode("cli:cha", "Chat", `["spec","p0"]`) + `]}}}`,
		"Memories":  memListJSON,
		"GetMemory": memGetVectorEnabledJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--module", "cha", "-m", specProductMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("inferring the sole product should lint cli:cha cleanly, got err=%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "OK") {
		t.Errorf("expected a clean lint after product inference; got:\n%s", out.String())
	}
}

func TestSpecLintScopedRootParentAboveScope(t *testing.T) {
	// Regression for #21: a --product/--module scoped lint must not raise a
	// false parent-exists error for the scope root (cor:acl) whose parent (cor)
	// lives above the scoped scan. --strict makes any finding fatal, so a clean
	// run proves the false positive is gone.
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("cor:acl", `["spec","p0"]`) + `]}}`,
		"NodeBatch": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` +
			specBatchHeaderNode("cor:acl", "Access control", `["spec","p0"]`) + `]}}}`,
		// lint now also probes the memory's vector index (#42); an indexed
		// memory keeps this --strict run clean.
		"Memories":  memListJSON,
		"GetMemory": memGetVectorEnabledJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--product", "cor", "--module", "acl", "-m", specProductMem, "--strict", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("scoped lint should pass, got err=%v code=%d\n%s", err, exitcode.FromError(err), out.String())
	}
	if strings.Contains(out.String(), "parent-exists") {
		t.Errorf("must not emit a parent-exists finding for the scope root; got:\n%s", out.String())
	}
}

func TestSpecLintErrorsExitConflict(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + badSpecDetail + `}}`,
		// lint also probes the vector index (#42); an indexed memory keeps the
		// failing findings here about the rubric, not the index.
		"Memories":  memListMicromentorJSON,
		"GetMemory": memGetVectorEnabledJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "msg:010:02", "-m", specMem, "--json", "--server", gql.URL})
	err := root.Execute()
	if exitcode.FromError(err) != exitcode.Conflict {
		t.Fatalf("expected Conflict for a non-compliant spec, got err=%v code=%d", err, exitcode.FromError(err))
	}
	if !strings.Contains(out.String(), "invalidates") {
		t.Errorf("expected the invalidates finding in output:\n%s", out.String())
	}
}

func TestSpecLintAllReportsUntaggedCitation(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg:010:02", `["p1"]`) + `]}}`,
		"NodeBatch": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` +
			specBatchNodeWithTags("msg:010:02", `["p1"]`) + `]}}}`,
		"Memories":  memListMicromentorJSON,
		"GetMemory": memGetVectorEnabledJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--all", "-m", specMem, "--json", "--server", gql.URL})
	err := root.Execute()
	if got := exitcode.FromError(err); got != exitcode.Conflict {
		t.Fatalf("expected Conflict for missing spec tag, got err=%v code=%d\n%s", err, got, out.String())
	}
	if !strings.Contains(out.String(), `"rule": "tag-spec"`) {
		t.Fatalf("expected tag-spec finding in JSON output:\n%s", out.String())
	}
	var vars findNodesVars
	_ = json.Unmarshal(captured["FindNodes"], &vars)
	if len(vars.Filter.Tags) != 0 {
		t.Fatalf("lint --all must not pre-filter by spec tag, got %v", vars.Filter.Tags)
	}
}

func TestSpecLintAllUnavailableListedNode(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
		"NodeBatch": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":["id-msg:010:02"],"nodes":[]}}}`,
		"Memories":  memListMicromentorJSON,
		"GetMemory": memGetVectorEnabledJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--all", "-m", specMem, "--json", "--server", gql.URL})
	err := root.Execute()
	if got := exitcode.FromError(err); got != exitcode.Conflict {
		t.Fatalf("expected Conflict for unavailable listed node, got err=%v code=%d\n%s", err, got, out.String())
	}
	if !strings.Contains(out.String(), `"citation": "msg:010:02"`) || !strings.Contains(out.String(), `"rule": "unavailable"`) {
		t.Fatalf("expected unavailable finding naming the listed citation:\n%s", out.String())
	}
}

func TestSpecLintScopeConflictsCommand(t *testing.T) {
	for _, args := range [][]string{
		{"spec", "lint", "--all", "--product", "cor", "-m", specMem},
		{"spec", "lint", "--all", "--module", "api", "-m", specMem},
		{"spec", "lint", "--all", "--product", "cor", "--module", "api", "-m", specMem},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
			t.Fatalf("args %v: expected usage error, got %d", args, got)
		}
	}
}

func TestSpecLintCleanJSONEmptyArray(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
		"Memories":   memListMicromentorJSON,
		"GetMemory":  memGetVectorEnabledJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "msg:010:02", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("clean lint JSON must be [], got:\n%s", out.String())
	}
}

// #42: a memory with no vector index gets a warning (not an error) — an
// otherwise-clean spec still lints OK (exit 0) but surfaces the index gap.
func TestSpecLintWarnsNoVectorIndex(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
		"Memories":   memListJSON,
		"GetMemory":  memGetNoVectorJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "msg:010:02", "-m", specProductMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a no-vector-index warning must not fail lint: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "vector-index") || !strings.Contains(out.String(), "no vector index") {
		t.Errorf("expected a vector-index warning:\n%s", out.String())
	}
}

// #42: --strict promotes the vector-index warning to an error (exit Conflict).
func TestSpecLintNoVectorIndexStrictFails(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
		"Memories":   memListJSON,
		"GetMemory":  memGetNoVectorJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "msg:010:02", "-m", specProductMem, "--strict", "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Conflict {
		t.Fatalf("--strict should promote the vector-index warning to an error, got %d", got)
	}
}

// #42: an indexed memory stays silent — the clean spec lints OK with no
// vector-index finding.
func TestSpecLintVectorIndexEnabledNoWarning(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
		"Memories":   memListJSON,
		"GetMemory":  memGetVectorEnabledJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "msg:010:02", "-m", specProductMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out.String(), "vector-index") {
		t.Errorf("an indexed memory must not warn:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "OK") {
		t.Errorf("expected a clean OK result:\n%s", out.String())
	}
}

// #41: --body-only prints just the raw markdown body — no metadata, edges, or
// lint — so it pipes cleanly into `node update --content -`.
func TestSpecGetBodyOnly(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "get", "msg:010:02", "-m", specMem, "--body-only", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "## Definition") || !strings.Contains(text, "The nudge.") {
		t.Errorf("body-only output should be the raw markdown body:\n%s", text)
	}
	for _, metadata := range []string{"Lint:", "Edges:", "Abstract:", "Tags:"} {
		if strings.Contains(text, metadata) {
			t.Errorf("body-only output must omit %q metadata:\n%s", metadata, text)
		}
	}
}

func TestSpecGetJSONEmptyEdgesAndLint(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cliChaModuleDetail + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "get", "cli:cha", "-m", specProductMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Edges json.RawMessage `json:"edges"`
		Lint  json.RawMessage `json:"lint"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	if string(dto.Edges) != "[]" || string(dto.Lint) != "[]" {
		t.Fatalf("edge-free clean spec get JSON must render edges/lint as [], got edges=%s lint=%s", dto.Edges, dto.Lint)
	}
}

func TestSpecRegisterEmptyJSONModulesArray(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":  `{"data":{"nodes":[]}}`,
		"ResolveUrn": `{"data":{"resolveUrn":null}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "register", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}
	var dto struct {
		Modules json.RawMessage `json:"modules"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	if string(dto.Modules) != "[]" {
		t.Fatalf("empty register must render modules: [], got %s", dto.Modules)
	}
}

func TestSpecGetBodyOnlyJSON(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "get", "msg:010:02", "-m", specMem, "--body-only", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Citation string `json:"citation"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Citation != "msg:010:02" || !strings.Contains(dto.Content, "## Definition") {
		t.Errorf("unexpected body-only JSON: %+v", dto)
	}
}

func TestSpecGetBodyOnlyRejectsPrefixAndAbstractOnly(t *testing.T) {
	for _, args := range [][]string{
		{"spec", "get", "--prefix", "msg:010", "-m", specMem, "--body-only"},
		{"spec", "get", "msg:010:02", "-m", specMem, "--body-only", "--abstract-only"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", "http://127.0.0.1:1"))
		if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
			t.Errorf("args %v: expected a Usage error, got %d", args, got)
		}
	}
}

func TestSpecSupersedeRequiresYes(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
		"FindNodes":  `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--server", gql.URL})
	err := root.Execute()
	if exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("non-interactive supersede without --yes should be Usage, got err=%v code=%d", err, exitcode.FromError(err))
	}
}

func TestSpecSupersedeRejectsNonSpecSource(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    linkNonSpecDetail,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--yes", "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("non-spec supersede source should be Usage, got %d", got)
	}
}

func TestSpecSupersede(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
		"FindNodes":  `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:00", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:03","name":"msg:010:03 — W2 v2","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"UpdateNode": `{"data":{"updateNode":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2","nodeType":"info","tags":["spec","p1","superseded"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"sp1","loc":"msg:010:02"},"target":{"id":"new1","loc":"msg:010:03"}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Last CreateEdge is the superseded-by link old -> new.
	var edge struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(captured["CreateEdge"], &edge)
	if edge.Name != "superseded-by" {
		t.Errorf("final edge label = %q, want superseded-by", edge.Name)
	}
	// The UpdateNode is the retire of the old spec (same loc + superseded tag).
	var retire struct {
		Input struct {
			Loc  string   `json:"loc"`
			Tags []string `json:"tags"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateNode"], &retire)
	if retire.Input.Loc != "msg:010:02" {
		t.Errorf("retire loc = %q (must keep the old loc — no renumber)", retire.Input.Loc)
	}
	if !contains(retire.Input.Tags, "superseded") {
		t.Errorf("retired spec must be tagged superseded, got %v", retire.Input.Tags)
	}

	var dto struct {
		Old   string `json:"old"`
		New   string `json:"new"`
		Edges []struct {
			Status string `json:"status"`
		} `json:"edges"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Old != "msg:010:02" || dto.New != "msg:010:03" {
		t.Errorf("supersede dto = %+v", dto)
	}
	// Every edge actually wired on the happy path reports status "created" — not
	// the plan echoed back regardless of outcome (#128).
	if len(dto.Edges) == 0 {
		t.Fatal("supersede JSON must carry the wired edges")
	}
	for _, e := range dto.Edges {
		if e.Status != "created" {
			t.Errorf("happy-path edge status = %q, want created", e.Status)
		}
	}
}

// #127/#128: when a ToC/inheritance target can't be resolved, supersede skips
// that edge (not silently — it's tagged "skipped" and warned), still emits the
// JSON, and exits non-zero so the orphaned replacement isn't read as a clean
// supersede.
func TestSpecSupersedeOrphanedEdgeFailsLoud(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:00", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`
	responses := map[string]string{
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
		"FindNodes":  scan,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:03","name":"msg:010:03 — W2 v2","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"UpdateNode": `{"data":{"updateNode":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2","nodeType":"info","tags":["spec","p1","superseded"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"sp1","loc":"msg:010:02"},"target":{"id":"new1","loc":"msg:010:03"}}}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Urn string `json:"urn"`
			} `json:"variables"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName == "ResolveUrn" {
			// Only the OLD spec resolves; every ToC/inheritance target misses, so
			// those edges are skipped and the replacement is left orphaned.
			if strings.HasSuffix(body.Variables.Urn, "::msg:010:02") {
				_, _ = w.Write([]byte(resolveSpecJSON))
			} else {
				_, _ = w.Write([]byte(`{"data":{"resolveUrn":null}}`))
			}
			return
		}
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		_, _ = w.Write([]byte(translateFindNodes(body.OperationName, resp)))
	}))
	defer srv.Close()

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--yes", "--json", "--server", srv.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "orphaned") {
		t.Fatalf("a supersede that can't wire its ToC edge must fail loudly, got %v", err)
	}
	if code := exitcode.FromError(err); code != exitcode.Error {
		t.Errorf("orphaned-edge exit code = %d, want %d (Error)", code, exitcode.Error)
	}
	// The JSON still reports each edge's REAL status: ToC/inheritance skipped, the
	// superseded-by link created.
	var dto struct {
		Edges []struct {
			Label  string `json:"label"`
			Status string `json:"status"`
		} `json:"edges"`
	}
	if uerr := json.Unmarshal([]byte(out.String()), &dto); uerr != nil {
		t.Fatalf("supersede JSON must still be emitted: %v\n%s", uerr, out.String())
	}
	created, skipped := 0, 0
	for _, e := range dto.Edges {
		switch e.Status {
		case edgeStatusCreatedTest:
			created++
		case edgeStatusSkippedTest:
			skipped++
		}
	}
	if created != 1 || skipped < 1 {
		t.Errorf("edge statuses = %+v; want superseded-by created and ToC edge(s) skipped", dto.Edges)
	}
	if errStr := f.IOStreams.ErrOut.(*strings.Builder).String(); !strings.Contains(errStr, "skipped edge") {
		t.Errorf("a skipped edge must warn on stderr, got: %q", errStr)
	}
}

// Codex #155: a --title of literally "superseded-by" collides with the
// retirement edge's label. The old label-keyed logic would skip creating the
// ToC edge (mistaking it for the special edge) yet still mark it "created" —
// a false success on an orphaned replacement. The retirement edge is now keyed
// by position, so the ToC edge is wired and reported honestly.
func TestSpecSupersedeTitleCollidesWithSpecialLabel(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:00", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`
	var createdEdgeLabels []string
	responses := map[string]string{
		"ResolveUrn": resolveSpecJSON, // every target resolves
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
		"FindNodes":  scan,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:03","name":"msg:010:03 — superseded-by","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"UpdateNode": `{"data":{"updateNode":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2","nodeType":"info","tags":["spec","p1","superseded"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"a","loc":"x"},"target":{"id":"b","loc":"y"}}}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Name string `json:"name"`
			} `json:"variables"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if body.OperationName == "CreateEdge" {
			createdEdgeLabels = append(createdEdgeLabels, body.Variables.Name)
		}
		w.Header().Set("Content-Type", "application/json")
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		_, _ = w.Write([]byte(translateFindNodes(body.OperationName, resp)))
	}))
	defer srv.Close()

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "superseded-by", "--yes", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute should succeed — every target resolves: %v", err)
	}
	var dto struct {
		Edges []struct {
			Status string `json:"status"`
		} `json:"edges"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	// Every planned edge — including the ToC edge whose label collides with the
	// --title — must actually be wired (one CreateEdge each) and reported created.
	// Before the fix the colliding ToC edge was skipped yet marked created, so
	// CreateEdge fired fewer times than there were edges.
	if len(createdEdgeLabels) != len(dto.Edges) {
		t.Errorf("wired %d edge(s) but planned/reported %d (a colliding ToC edge was skipped): calls=%v", len(createdEdgeLabels), len(dto.Edges), createdEdgeLabels)
	}
	for _, e := range dto.Edges {
		if e.Status != edgeStatusCreatedTest {
			t.Errorf("with all targets resolving, every edge must be created, got %q in %+v", e.Status, dto.Edges)
		}
	}
}

// String forms of the edge-status constants, kept local to the cmd-package test
// (the constants themselves live in the unexported spec package).
const (
	edgeStatusCreatedTest = "created"
	edgeStatusSkippedTest = "skipped"
)

func TestSpecImportStub(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "import", "spec-kit", "/tmp/specs", "-m", specMem})
	err := root.Execute()
	if exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("import stub should exit Usage (not yet implemented), got err=%v code=%d", err, exitcode.FromError(err))
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// ---- spec extract (#41 item 2) ----

// The source entity GetNodeById returns: a body with a clearly delimited
// "Node type" chunk to extract, the Node name (for the default ref-label), and
// a tail section so a strip leaves something behind.
const extractSrcDetail = `{"data":{"node":{"id":"src1","memoryId":"mem1","loc":"cor:dmo:060:02","name":"cor:dmo:060:02 — Node",` +
	`"description":null,"abstract":"The Node entity.","abstractOriginHash":null,"nodeType":"info","tags":["spec"],` +
	`"content":"# cor:dmo:060:02 — Node\n\nIntro para.\n\n## Node type\n\nThe nodeType chunk.\n\n## Tail\n\nend.\n",` +
	`"data":{"version":"0.0.1"},"seq":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z",` +
	`"outgoingEdges":[],"incomingEdges":[]}}}`

// extractChunk is the moved chunk — present verbatim in extractSrcDetail.
const extractChunk = "## Node type\n\nThe nodeType chunk.\n"

// extractScan is the product/module subtree: feature 020 holds rules 00..03, so
// the next allocated rule is 04.
func extractScan() string {
	return `{"data":{"nodes":[` +
		specNodeList("cor", `["spec"]`) + `,` +
		specNodeList("cor:dmo", `["spec"]`) + `,` +
		specNodeList("cor:dmo:020", `["spec"]`) + `,` +
		specNodeList("cor:dmo:020:00", `["spec"]`) + `,` +
		specNodeList("cor:dmo:020:01", `["spec"]`) + `,` +
		specNodeList("cor:dmo:020:02", `["spec"]`) + `,` +
		specNodeList("cor:dmo:020:03", `["spec"]`) + `,` +
		specNodeList("cor:dmo:060", `["spec"]`) + `,` +
		specNodeList("cor:dmo:060:02", `["spec"]`) +
		`]}}`
}

func extractMocks() map[string]string {
	return map[string]string{
		"FindNodes":  extractScan(),
		"GetNode":    extractSrcDetail,
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"t1","kind":"node","memoryId":"mem1"}}}`,
		"CreateNode": `{"data":{"createNode":{"id":"new1","memoryId":"mem1","loc":"cor:dmo:020:04","name":"cor:dmo:020:04 — Node type","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"UpdateNode": `{"data":{"updateNode":{"id":"src1","memoryId":"mem1","loc":"cor:dmo:060:02","name":"cor:dmo:060:02 — Node","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"new1","loc":"cor:dmo:020:04"},"target":{"id":"t1","loc":"cor:dmo:020"}}}}`,
	}
}

type extractInput struct {
	Input struct {
		Loc      string          `json:"loc"`
		Name     string          `json:"name"`
		Tags     []string        `json:"tags"`
		NodeType *string         `json:"nodeType"`
		Abstract *string         `json:"abstract"`
		Content  *string         `json:"content"`
		Data     json.RawMessage `json:"data"`
	} `json:"input"`
}

type extractDTO struct {
	Citation     string `json:"citation"`
	Source       string `json:"source"`
	StripSource  bool   `json:"stripSource"`
	StripMatched bool   `json:"stripMatched"`
	Edges        []struct {
		Label  string `json:"label"`
		Target string `json:"target"`
	} `json:"edges"`
}

func (d extractDTO) edgeTo(target string) (string, bool) {
	for _, e := range d.Edges {
		if e.Target == target {
			return e.Label, true
		}
	}
	return "", false
}

func TestSpecExtract(t *testing.T) {
	gql, captured := captureGraphQL(t, extractMocks())
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(extractChunk)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "extract", "cor:dmo:060:02", "-m", specMem,
		"--to-feature", "020", "--title", "Node type", "--content", "-", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Only the create (no --strip-source) — no source update.
	var up extractInput
	if err := json.Unmarshal(captured["CreateNode"], &up); err != nil {
		t.Fatalf("CreateNode vars: %v", err)
	}
	if up.Input.Loc != "cor:dmo:020:04" {
		t.Errorf("new loc = %q, want cor:dmo:020:04", up.Input.Loc)
	}
	if up.Input.Name != "cor:dmo:020:04 — Node type" {
		t.Errorf("new name = %q", up.Input.Name)
	}
	if _, ok := captured["UpdateNode"]; ok {
		t.Error("without --strip-source no UpdateNode must be sent")
	}
	if up.Input.NodeType == nil || *up.Input.NodeType != "info" {
		t.Errorf("nodeType = %v", up.Input.NodeType)
	}
	if up.Input.Content == nil || !strings.Contains(*up.Input.Content, "nodeType chunk") {
		t.Errorf("new body should be the moved chunk, got %v", up.Input.Content)
	}
	if !strings.Contains(string(up.Input.Data), "version") {
		t.Errorf("data must carry a version, got %s", up.Input.Data)
	}

	var dto extractDTO
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Citation != "cor:dmo:020:04" || dto.Source != "cor:dmo:060:02" {
		t.Errorf("dto citation/source = %q/%q", dto.Citation, dto.Source)
	}
	if dto.StripSource {
		t.Error("stripSource should be false without --strip-source")
	}
	if lbl, ok := dto.edgeTo("cor:dmo:020"); !ok || lbl != "Node type" {
		t.Errorf("ToC edge = %q/%v", lbl, ok)
	}
	if _, ok := dto.edgeTo("cor:dmo:020:00"); !ok {
		t.Errorf("missing inheritance edge, edges=%v", dto.Edges)
	}
	if lbl, ok := dto.edgeTo("cor:dmo:060:02"); !ok || lbl != "documents Node type on the Node entity" {
		t.Errorf("cross-ref edge = %q/%v", lbl, ok)
	}
}

// An edge that can't be wired (ToC, inheritance, or the cross-ref to the source)
// is a partial write. The spec is still created (the JSON is emitted), but the
// command exits non-zero so the gap isn't read as a clean extract — matching
// `spec new`/`spec supersede` (#127).
func TestSpecExtractFailsLoudOnEdgeFailure(t *testing.T) {
	mocks := extractMocks()
	// Every target resolves, but CreateEdge is rejected — so all planned edges
	// fail (extract has no must-succeed edge to worry about).
	mocks["CreateEdge"] = `{"errors":[{"message":"createEdge operator 'flag' is not in the v1 allowlist"}]}`
	gql, _ := captureGraphQL(t, mocks)
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(extractChunk)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "extract", "cor:dmo:060:02", "-m", specMem,
		"--to-feature", "020", "--title", "Node type", "--content", "-", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "could not be wired") {
		t.Fatalf("an extract that can't wire its edge(s) must fail loudly, got %v", err)
	}
	if code := exitcode.FromError(err); code != exitcode.Error {
		t.Errorf("orphaned-edge exit code = %d, want %d (Error)", code, exitcode.Error)
	}
	// The created spec is still reported on stdout before the error.
	var dto extractDTO
	if uerr := json.Unmarshal([]byte(out.String()), &dto); uerr != nil {
		t.Fatalf("extract JSON must still be emitted: %v\n%s", uerr, out.String())
	}
	if dto.Citation != "cor:dmo:020:04" {
		t.Errorf("the created spec must still be reported, got %+v", dto)
	}
}

func TestSpecExtractStripSourceHit(t *testing.T) {
	gql, captured := captureGraphQL(t, extractMocks())
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(extractChunk)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "extract", "cor:dmo:060:02", "-m", specMem,
		"--to-feature", "020", "--title", "Node type", "--content", "-", "--strip-source", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// The UpdateNode is the source trim (create new → edges → strip source).
	var up extractInput
	if err := json.Unmarshal(captured["UpdateNode"], &up); err != nil {
		t.Fatalf("UpdateNode vars: %v", err)
	}
	if up.Input.Loc != "cor:dmo:060:02" {
		t.Fatalf("UpdateNode loc = %q, want the source cor:dmo:060:02", up.Input.Loc)
	}
	if up.Input.Content == nil {
		t.Fatal("source trim must send content")
	}
	if strings.Contains(*up.Input.Content, "nodeType chunk") {
		t.Errorf("trimmed source still contains the chunk:\n%s", *up.Input.Content)
	}
	if !strings.Contains(*up.Input.Content, "## Tail") || !strings.Contains(*up.Input.Content, "Intro para.") {
		t.Errorf("trimmed source lost surrounding content:\n%s", *up.Input.Content)
	}

	var dto extractDTO
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if !dto.StripSource || !dto.StripMatched {
		t.Errorf("expected stripSource+stripMatched true, got %+v", dto)
	}
}

func TestSpecExtractStripSourceMiss(t *testing.T) {
	gql, captured := captureGraphQL(t, extractMocks())
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("## Absent\n\nnot in the source body.\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "extract", "cor:dmo:060:02", "-m", specMem,
		"--to-feature", "020", "--title", "Ghost", "--content", "-", "--strip-source", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// A miss leaves the source untouched: the create happens, no UpdateNode.
	var up extractInput
	if err := json.Unmarshal(captured["CreateNode"], &up); err != nil {
		t.Fatalf("CreateNode vars: %v", err)
	}
	if up.Input.Loc != "cor:dmo:020:04" {
		t.Errorf("new loc = %q, want cor:dmo:020:04", up.Input.Loc)
	}
	if _, ok := captured["UpdateNode"]; ok {
		t.Error("source must not be updated on a miss")
	}
	var dto extractDTO
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if !dto.StripSource || dto.StripMatched {
		t.Errorf("expected stripSource true, stripMatched false, got %+v", dto)
	}
}

func TestSpecExtractDryRun(t *testing.T) {
	// No mutation ops mocked — any CreateNode/UpdateNode/CreateEdge would be an
	// unexpected op.
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes":  extractScan(),
		"GetNode":    extractSrcDetail,
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"src1","kind":"node","memoryId":"mem1"}}}`,
	})
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(extractChunk)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "extract", "cor:dmo:060:02", "-m", specMem,
		"--to-feature", "020", "--title", "Node type", "--content", "-", "--strip-source", "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["CreateNode"]; ok {
		t.Error("dry-run must not call CreateNode")
	}
	if _, ok := captured["UpdateNode"]; ok {
		t.Error("dry-run must not call UpdateNode")
	}
	if _, ok := captured["CreateEdge"]; ok {
		t.Error("dry-run must not call CreateEdge")
	}
	if !strings.Contains(out.String(), "would extract") || !strings.Contains(out.String(), "would trim") {
		t.Errorf("unexpected dry-run output:\n%s", out.String())
	}
}

func TestSpecExtractStripNeedsChunk(t *testing.T) {
	// --strip-source with no chunk supplied is rejected before any network call.
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "extract", "cor:dmo:060:02", "-m", specMem,
		"--to-feature", "020", "--title", "T", "--strip-source", "--server", "http://127.0.0.1:1"})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("--strip-source without a chunk should be Usage, got %d", got)
	}
}

func TestSpecExtractSourceNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"src1","kind":"node","memoryId":"mem1"}}}`,
		"GetNode":    `{"data":{"node":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "extract", "cor:dmo:060:99", "-m", specMem,
		"--to-feature", "020", "--title", "T", "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.NotFound {
		t.Fatalf("missing source should be NotFound, got %d", got)
	}
}

func TestSpecExtractRejectsNonSpecSource(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    linkNonSpecDetail,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "extract", "cor:dmo:060:02", "-m", specMem,
		"--to-feature", "020", "--title", "T", "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("non-spec extract source should be Usage, got %d", got)
	}
}

func TestSpecExtractRejectsAbstractAndAbstractFile(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "extract", "cor:dmo:060:02", "-m", specMem, "--to-feature", "020", "--title", "T",
		"--abstract", "x", "--abstract-file", "/tmp/x.md", "--server", "http://127.0.0.1:1"})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("--abstract + --abstract-file should be Usage, got %d", got)
	}
}

// ---- spec link (#41 item 4) ----

// linkSpecDetail is a spec-tagged node returned for both endpoints of a
// `spec link` (the fake GraphQL keys on operation name, so both fetches get the
// same node — the citations in the DTO are what distinguish from/to).
const linkSpecDetail = `{"data":{"node":{"id":"sp1","memoryId":"mem1","loc":"cor:dmo:060:02","name":"cor:dmo:060:02 — Node",` +
	`"description":null,"abstract":"The Node entity.","abstractOriginHash":null,"nodeType":"info","tags":["spec"],` +
	`"content":"# cor:dmo:060:02 — Node\n","data":{"version":"0.0.1"},"seq":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z",` +
	`"outgoingEdges":[],"incomingEdges":[]}}}`

// linkNonSpecDetail is a node WITHOUT the "spec" tag — `spec link` must refuse it.
const linkNonSpecDetail = `{"data":{"node":{"id":"x1","memoryId":"mem1","loc":"register","name":"register — R",` +
	`"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"info","tags":["index"],` +
	`"content":"# register\n","data":null,"seq":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z",` +
	`"outgoingEdges":[],"incomingEdges":[]}}}`

const linkEdgeResp = `{"data":{"createEdge":{"id":"le1","label":"x","priority":0,"source":{"id":"sp1","loc":"cor:dmo:020:04"},"target":{"id":"sp1","loc":"cor:dmo:060:02"}}}}`

type linkDTO struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Label  string `json:"label"`
	EdgeID string `json:"edgeId"`
	DryRun bool   `json:"dryRun"`
}

func TestSpecLink(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    linkSpecDetail,
		"CreateEdge": linkEdgeResp,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:020:04", "cor:dmo:060:02", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto linkDTO
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.From != "cor:dmo:020:04" || dto.To != "cor:dmo:060:02" {
		t.Errorf("from/to = %q/%q", dto.From, dto.To)
	}
	// No --label: synthesized in the corpus convention from the two titles
	// (both fetch the "— Node" node).
	if dto.Label != "documents Node on the Node entity" {
		t.Errorf("default label = %q", dto.Label)
	}
	if dto.EdgeID != "le1" {
		t.Errorf("edgeId = %q", dto.EdgeID)
	}
	// CreateEdge was called with the synthesized label.
	var edge struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(captured["CreateEdge"], &edge)
	if edge.Name != "documents Node on the Node entity" {
		t.Errorf("CreateEdge label = %q", edge.Name)
	}
}

func TestSpecLinkExplicitLabel(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    linkSpecDetail,
		"CreateEdge": linkEdgeResp,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:020:04", "cor:dmo:060:02", "-m", specMem,
		"--label", "documents the nodeType field of Node", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto linkDTO
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Label != "documents the nodeType field of Node" {
		t.Errorf("explicit label = %q", dto.Label)
	}
	var edge struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(captured["CreateEdge"], &edge)
	if edge.Name != "documents the nodeType field of Node" {
		t.Errorf("CreateEdge label = %q (explicit --label must pass through)", edge.Name)
	}
}

func TestSpecLinkDryRun(t *testing.T) {
	// CreateEdge is not mocked — a dry-run that called it would be an unexpected op.
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    linkSpecDetail,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:020:04", "cor:dmo:060:02", "-m", specMem, "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["CreateEdge"]; ok {
		t.Error("dry-run must not call CreateEdge")
	}
	if !strings.Contains(out.String(), "would link") {
		t.Errorf("unexpected dry-run output:\n%s", out.String())
	}
}

func TestSpecLinkNonSpecEndpoint(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    linkNonSpecDetail,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:020:04", "cor:dmo:060:02", "-m", specMem, "--server", gql.URL})
	err := root.Execute()
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Fatalf("a non-spec endpoint should be Usage, got %d", got)
	}
	if !strings.Contains(err.Error(), "hadron edge add") {
		t.Fatalf("spec link non-spec error should suggest edge add, got %q", err)
	}
}

func TestSpecLinkSelf(t *testing.T) {
	// Linking a citation to itself is rejected before any network round-trip.
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:060:02", "cor:dmo:060:02", "-m", specMem, "--server", "http://127.0.0.1:1"})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("self-link should be Usage, got %d", got)
	}
}

func TestSpecLinkEndpointNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:020:04", "cor:dmo:060:02", "-m", specMem, "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.NotFound {
		t.Fatalf("missing endpoint should be NotFound, got %d", got)
	}
}

// ---- spec edit (#41 item 1) ----

// editNonSpecDetail is a node WITHOUT the "spec" tag — `spec edit` must refuse it.
const editNonSpecDetail = `{"data":{"node":{"id":"x1","memoryId":"mem1","loc":"register","name":"register — R",` +
	`"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"info","tags":["index"],` +
	`"content":"# register\n","data":null,"seq":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z",` +
	`"outgoingEdges":[],"incomingEdges":[]}}}`

func editMocks() map[string]string {
	return map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
		"UpdateNode": `{"data":{"updateNode":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2","nodeType":"info","tags":["spec","p1","messaging"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
	}
}

type editUpdateInput struct {
	Input struct {
		Loc      string  `json:"loc"`
		Name     string  `json:"name"`
		Content  *string `json:"content"`
		Abstract *string `json:"abstract"`
	} `json:"input"`
}

// TestSpecEditInteractive drives the default (editor) path with the seam faked:
// a changed body is written as a content-only update, preserving the abstract.
func TestSpecEditInteractive(t *testing.T) {
	restore := spec.SetEditorFuncForTest(func(_ *output.IOStreams, current string) (string, error) {
		return current + "\n## New\n\nAdded.\n", nil
	})
	defer restore()

	gql, captured := captureGraphQL(t, editMocks())
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var up editUpdateInput
	if err := json.Unmarshal(captured["UpdateNode"], &up); err != nil {
		t.Fatalf("UpdateNode vars: %v", err)
	}
	if up.Input.Loc != "msg:010:02" {
		t.Errorf("loc = %q, want msg:010:02 (no renumber)", up.Input.Loc)
	}
	if up.Input.Content == nil || !strings.Contains(*up.Input.Content, "## New") {
		t.Errorf("edited body not sent: %v", up.Input.Content)
	}
	if up.Input.Abstract != nil {
		t.Errorf("content-only update must not send an abstract (preserve it), got %q", *up.Input.Abstract)
	}

	var dto struct {
		Changed bool `json:"changed"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if !dto.Changed {
		t.Error("expected changed=true")
	}
}

// TestSpecEditNoOp: an editor that saves without changes writes nothing.
func TestSpecEditNoOp(t *testing.T) {
	restore := spec.SetEditorFuncForTest(func(_ *output.IOStreams, current string) (string, error) {
		return current, nil
	})
	defer restore()

	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["UpdateNode"]; ok {
		t.Error("an unchanged body must not call UpdateNode")
	}
	if !strings.Contains(out.String(), "no changes") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
}

// TestSpecEditCRLFNoOp: an editor that rewrites LF to CRLF without changing the
// text is still a no-op (CRLF is normalized to LF before the change check).
func TestSpecEditCRLFNoOp(t *testing.T) {
	restore := spec.SetEditorFuncForTest(func(_ *output.IOStreams, current string) (string, error) {
		return strings.ReplaceAll(current, "\n", "\r\n"), nil
	})
	defer restore()

	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["UpdateNode"]; ok {
		t.Error("a CRLF-only rewrite must not call UpdateNode")
	}
	if !strings.Contains(out.String(), "no changes") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
}

// TestSpecEditContentStdin: --content - replaces the body non-interactively.
func TestSpecEditContentStdin(t *testing.T) {
	gql, captured := captureGraphQL(t, editMocks())
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("# replaced body\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem, "--content", "-", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var up editUpdateInput
	_ = json.Unmarshal(captured["UpdateNode"], &up)
	if up.Input.Content == nil || *up.Input.Content != "# replaced body\n" {
		t.Errorf("stdin body not sent verbatim: %v", up.Input.Content)
	}
}

// TestSpecEditDryRun: --dry-run previews without writing.
func TestSpecEditDryRun(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
	})
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("# replaced body\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem, "--content", "-", "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["CreateNode"]; ok {
		t.Error("dry-run must not call CreateNode")
	}
	if !strings.Contains(out.String(), "would update") {
		t.Errorf("unexpected dry-run output:\n%s", out.String())
	}
}

func TestSpecEditContentAndFileExclusive(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem,
		"--content", "x", "--content-file", "/tmp/x.md", "--server", "http://127.0.0.1:1"})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("--content + --content-file should be Usage, got %d", got)
	}
}

// TestSpecEditAbstractFile: --abstract-file updates the abstract and preserves
// the body (no content sent).
func TestSpecEditAbstractFile(t *testing.T) {
	dir := t.TempDir()
	absPath := filepath.Join(dir, "abstract.md")
	if err := os.WriteFile(absPath, []byte("A sharper retrieval surface.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, editMocks())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem, "--abstract-file", absPath, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var up editUpdateInput
	if err := json.Unmarshal(captured["UpdateNode"], &up); err != nil {
		t.Fatalf("UpdateNode vars: %v", err)
	}
	if up.Input.Abstract == nil || *up.Input.Abstract != "A sharper retrieval surface.\n" {
		t.Errorf("abstract not sent verbatim: %v", up.Input.Abstract)
	}
	if up.Input.Content != nil {
		t.Errorf("abstract-only update must not send the body (preserve it), got %q", *up.Input.Content)
	}
}

// TestSpecEditBodyAndAbstract: body (stdin) and abstract (file) update together
// in one call.
func TestSpecEditBodyAndAbstract(t *testing.T) {
	dir := t.TempDir()
	absPath := filepath.Join(dir, "abstract.md")
	if err := os.WriteFile(absPath, []byte("New abstract.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, editMocks())
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("# new body\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem,
		"--content", "-", "--abstract-file", absPath, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var up editUpdateInput
	_ = json.Unmarshal(captured["UpdateNode"], &up)
	if up.Input.Content == nil || *up.Input.Content != "# new body\n" {
		t.Errorf("body not sent: %v", up.Input.Content)
	}
	if up.Input.Abstract == nil || *up.Input.Abstract != "New abstract.\n" {
		t.Errorf("abstract not sent: %v", up.Input.Abstract)
	}
}

// TestSpecEditAbstractNoOp: passing the current abstract back changes nothing.
func TestSpecEditAbstractNoOp(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem,
		"--abstract", "Win back users who never engaged after signup.", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["UpdateNode"]; ok {
		t.Error("an unchanged abstract must not call UpdateNode")
	}
	if !strings.Contains(out.String(), "no changes") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
}

// TestSpecEditInteractiveAbstract: editing the abstract region of the combined
// buffer sends the abstract and preserves the body.
func TestSpecEditInteractiveAbstract(t *testing.T) {
	restore := spec.SetEditorFuncForTest(func(_ *output.IOStreams, current string) (string, error) {
		return strings.Replace(current, "Win back users who never engaged after signup.", "Reworded abstract.", 1), nil
	})
	defer restore()

	gql, captured := captureGraphQL(t, editMocks())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var up editUpdateInput
	_ = json.Unmarshal(captured["UpdateNode"], &up)
	if up.Input.Abstract == nil || *up.Input.Abstract != "Reworded abstract." {
		t.Errorf("edited abstract not sent: %v", up.Input.Abstract)
	}
	if up.Input.Content != nil {
		t.Errorf("body unchanged must not be sent, got %q", *up.Input.Content)
	}
}

// TestSpecEditInteractiveDividerRemoved: deleting the body divider aborts
// without writing.
func TestSpecEditInteractiveDividerRemoved(t *testing.T) {
	restore := spec.SetEditorFuncForTest(func(_ *output.IOStreams, _ string) (string, error) {
		return "I deleted the dividers and just typed this.\n", nil
	})
	defer restore()

	gql, captured := captureGraphQL(t, editMocks())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem, "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("a removed body divider should be Usage, got %d", got)
	}
	if _, ok := captured["UpdateNode"]; ok {
		t.Error("a removed divider must not call UpdateNode")
	}
}

func TestSpecEditAbstractAndFileExclusive(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem,
		"--abstract", "x", "--abstract-file", "/tmp/x.md", "--server", "http://127.0.0.1:1"})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("--abstract + --abstract-file should be Usage, got %d", got)
	}
}

func TestSpecEditDualStdin(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem,
		"--content", "-", "--abstract", "-", "--server", "http://127.0.0.1:1"})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("--content - with --abstract - should be Usage, got %d", got)
	}
}

func TestSpecEditNonSpec(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    editNonSpecDetail,
	})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("anything\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "msg:010:02", "-m", specMem, "--content", "-", "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
		t.Fatalf("editing a non-spec node should be Usage, got %d", got)
	}
}
