package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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
	return fmt.Sprintf(`{"id":"id-%s","memoryId":"mem1","loc":%q,"name":%q,"alias":null,"nodeType":"info",`+
		`"description":null,"abstract":"Win back users who never engaged after signup.","abstractOriginHash":null,`+
		`"tags":["spec","p1"],"seq":null,"data":{"version":"0.0.1"},"properties":null,`+
		`"content":"# spec\n\n## Definition\nx\n\n## Rule\nx\n\n## Durable vs tunable\nx\n\n## What invalidates this spec\nChanges.\n",`+
		`"updatedAt":"2026-06-14T00:00:00Z",`+
		`"outgoingEdges":[{"label":"p1: W2","priority":0,"condition":null,"target":{"id":"f1","loc":"msg:010","memoryId":"mem1"}}],`+
		`"incomingEdges":[]}`,
		loc, loc, loc+" — W2")
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

// A clean product-rooted module header (cor:acl). Its parent (cor, the
// product root) lives above a --product/--module scope, so a scoped lint
// must not raise parent-exists for it (issue #21).
const corAclModuleDetail = `{"id":"id-cor:acl","memoryId":"mem1","loc":"cor:acl","name":"cor:acl — Access control",` +
	`"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"info","tags":["spec","p0"],` +
	`"content":"# cor:acl — Access control\n","data":null,"seq":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z",` +
	`"outgoingEdges":[],"incomingEdges":[]}`

func TestSpecLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Nodes": `{"data":{"nodes":[` + specNodeList("msg:010:01", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
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
	var vars struct {
		Prefix string   `json:"prefix"`
		Tags   []string `json:"tags"`
	}
	_ = json.Unmarshal(captured["Nodes"], &vars)
	if vars.Prefix != "msg:010" {
		t.Errorf("prefix = %q", vars.Prefix)
	}
	if len(vars.Tags) != 1 || vars.Tags[0] != "spec" {
		t.Errorf("ls should filter to spec tag, got %v", vars.Tags)
	}
}

func TestSpecGet(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":  resolveSpecJSON,
		"GetNodeById": `{"data":{"nodeById":` + cleanSpecDetail + `}}`,
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

func TestSpecGetPrefix(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Nodes":     `{"data":{"nodes":[` + specNodeList("msg:010:01", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
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
	var nodesVars struct {
		Prefix string   `json:"prefix"`
		Tags   []string `json:"tags"`
		Limit  int      `json:"limit"`
	}
	_ = json.Unmarshal(captured["Nodes"], &nodesVars)
	if nodesVars.Prefix != "msg:010" || len(nodesVars.Tags) != 1 || nodesVars.Tags[0] != "spec" {
		t.Errorf("prefix/tags wrong: %+v", nodesVars)
	}
	if nodesVars.Limit != 500 {
		t.Errorf("default prefix mode should page by 500 (exhaustive), got limit=%d", nodesVars.Limit)
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
		"Nodes":     `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
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
	var vars struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal(captured["Nodes"], &vars)
	if vars.Limit != 1 {
		t.Errorf("explicit --limit should pass through verbatim, got %d", vars.Limit)
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
		"NodeSearch": `{"data":{"nodeSearch":{"degraded":null,"reason":null,"nodes":[` +
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
	var vars struct {
		Mode      string `json:"mode"`
		MemoryUrn string `json:"memoryUrn"`
	}
	_ = json.Unmarshal(captured["NodeSearch"], &vars)
	if vars.Mode != "hybrid" {
		t.Errorf("default find should use hybrid mode, got %q", vars.Mode)
	}
	if vars.MemoryUrn != specMem {
		t.Errorf("memoryUrn = %q", vars.MemoryUrn)
	}
}

func TestSpecFindMatchExactly(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Nodes": `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "find", "msg:010", "-m", specMem, "--match-exactly", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Search string   `json:"search"`
		Tags   []string `json:"tags"`
	}
	_ = json.Unmarshal(captured["Nodes"], &vars)
	if vars.Search != "msg:010" {
		t.Errorf("search = %q", vars.Search)
	}
	found := false
	for _, tag := range vars.Tags {
		if tag == "spec" {
			found = true
		}
	}
	if !found {
		t.Errorf("--match-exactly should filter to spec tag, got %v", vars.Tags)
	}
}

func TestSpecRegisterCheckDrift(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"Nodes":      `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"reg1","kind":"node","memoryId":"mem1"}}}`,
		"GetNodeById": `{"data":{"nodeById":{"id":"reg1","memoryId":"mem1","loc":"register","name":"register — R",` +
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
		"Nodes":      scan,
		"UpsertNode": `{"data":{"upsertNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:01","name":"msg:010:01 — Test","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
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
			Loc        string          `json:"loc"`
			Name       string          `json:"name"`
			Tags       []string        `json:"tags"`
			NodeType   *string         `json:"nodeType"`
			CreateOnly *bool           `json:"createOnly"`
			Abstract   *string         `json:"abstract"`
			Data       json.RawMessage `json:"data"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured["UpsertNode"], &up); err != nil {
		t.Fatalf("UpsertNode vars: %v", err)
	}
	if up.Input.Loc != "msg:010:01" {
		t.Errorf("allocated loc = %q, want msg:010:01", up.Input.Loc)
	}
	if up.Input.CreateOnly == nil || !*up.Input.CreateOnly {
		t.Error("createOnly must be true")
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

func TestSpecNewDryRun(t *testing.T) {
	// Only the scan is mocked: any mutation would be an unexpected op and fail.
	scan := `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `]}}`
	gql, captured := captureGraphQL(t, map[string]string{"Nodes": scan})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specMem, "--module", "msg", "--feature", "010", "--title", "Test", "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["UpsertNode"]; ok {
		t.Error("dry-run must not call UpsertNode")
	}
	if !strings.Contains(out.String(), "would create") {
		t.Errorf("unexpected dry-run output:\n%s", out.String())
	}
}

func TestSpecNewMissingParent(t *testing.T) {
	// Module exists but the feature does not.
	gql, _ := captureGraphQL(t, map[string]string{
		"Nodes": `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `]}}`,
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

// memListJSON is a MyMemories response whose urn matches specProductMem
// normalized to a single-colon memory urn.
const memListJSON = `{"data":{"myMemories":[{"id":"mem1","urn":"hadronmemory.com:platform-specs","name":"Platform Specs","shortDescription":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org1","isEncrypted":false,"updatedAt":"2026-06-14T00:00:00Z"}]}}`

// memGetJSON is a GetMemory response with the given data bag (raw JSON, e.g.
// `null` or `{"spec":{"scheme":"product"}}`).
func memGetJSON(data string) string {
	return `{"data":{"memory":{"id":"mem1","urn":"hadronmemory.com:platform-specs","name":"Platform Specs","shortDescription":null,"description":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"OK","vectorIndexEnabled":false,"data":` + data + `,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z"}}}`
}

func TestSpecDescribeProduct(t *testing.T) {
	nodes := strings.Join([]string{
		specNodeList("cli", `["spec","p0"]`),
		specNodeList("cli:gen", `["spec","p0"]`),
		specNodeList("cli:cha", `["spec","p1"]`),
		specNodeList("cli:cha:010", `["spec","p1"]`),
		specNodeList("cli:cha:010:01", `["spec","p1"]`),
	}, ",")
	gql, _ := captureGraphQL(t, map[string]string{
		"MyMemories": memListJSON,
		"GetMemory":  memGetJSON(`null`),
		"Nodes":      `{"data":{"nodes":[` + nodes + `]}}`,
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
		"MyMemories":   memListJSON,
		"GetMemory":    memGetJSON(`null`),
		"UpdateMemory": `{"data":{"updateMemory":{"id":"mem1","urn":"hadronmemory.com:platform-specs","name":"P","shortDescription":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org1","isEncrypted":false,"data":{"spec":{"scheme":"product"}},"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"Nodes":        `{"data":{"nodes":[]}}`, // empty corpus: the declared scheme is all there is
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

func TestSpecNewProduct(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Nodes":      `{"data":{"nodes":[]}}`,
		"UpsertNode": `{"data":{"upsertNode":{"id":"new1","memoryId":"mem1","loc":"cli","name":"cli — Hadron CLI","nodeType":"info","tags":["spec","p0"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specProductMem, "--new-product", "--product", "cli", "--title", "Hadron CLI", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var up struct {
		Input struct {
			Loc string `json:"loc"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["UpsertNode"], &up)
	if up.Input.Loc != "cli" {
		t.Errorf("product root loc = %q, want cli", up.Input.Loc)
	}
	var dto struct {
		Citation string           `json:"citation"`
		Edges    []map[string]any `json:"edges"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Citation != "cli" {
		t.Errorf("citation = %q, want cli", dto.Citation)
	}
	if len(dto.Edges) != 0 {
		t.Errorf("a product root has no ToC/inherit edges, got %v", dto.Edges)
	}
}

func TestSpecNewProductModule(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("cli", `["spec","p0"]`) + `,` + specNodeList("cli:gen", `["spec","p0"]`) + `]}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"Nodes":      scan,
		"UpsertNode": `{"data":{"upsertNode":{"id":"new1","memoryId":"mem1","loc":"cli:cha","name":"cli:cha — chat","nodeType":"info","tags":["spec","p0"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"t1","kind":"node","memoryId":"mem1"}}}`,
		"CreateEdge": `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"new1","loc":"cli:cha"},"target":{"id":"t1","loc":"cli"}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "-m", specProductMem, "--product", "cli", "--new-module", "--module", "cha", "--title", "chat command group", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var up struct {
		Input struct {
			Loc string `json:"loc"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["UpsertNode"], &up)
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

func TestSpecNewProductContract(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("cli", `["spec","p0"]`) + `]}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"Nodes":      scan,
		"UpsertNode": `{"data":{"upsertNode":{"id":"new1","memoryId":"mem1","loc":"cli:gen","name":"cli:gen — provisions","nodeType":"info","tags":["spec","p0"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
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
	_ = json.Unmarshal(captured["UpsertNode"], &up)
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
		"Nodes":      scan,
		"UpsertNode": `{"data":{"upsertNode":{"id":"new1","memoryId":"mem1","loc":"msg:000","name":"msg:000 — provisions","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
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
	_ = json.Unmarshal(captured["UpsertNode"], &up)
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
	// --module cha matches nothing in a product corpus (modules are cli:cha):
	// fail loudly instead of a misleading "0 OK".
	gql, _ := captureGraphQL(t, map[string]string{
		"Nodes": `{"data":{"nodes":[` + specNodeList("cli:cha", `["spec","p1"]`) + `]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--module", "cha", "-m", specProductMem, "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.NotFound {
		t.Fatalf("a --module scope matching nothing should be NotFound, got %d", got)
	}
}

func TestSpecLintScopedRootParentAboveScope(t *testing.T) {
	// Regression for #21: a --product/--module scoped lint must not raise a
	// false parent-exists error for the scope root (cor:acl) whose parent (cor)
	// lives above the scoped scan. --strict makes any finding fatal, so a clean
	// run proves the false positive is gone.
	gql, _ := captureGraphQL(t, map[string]string{
		"Nodes":       `{"data":{"nodes":[` + specNodeList("cor:acl", `["spec","p0"]`) + `]}}`,
		"GetNodeById": `{"data":{"nodeById":` + corAclModuleDetail + `}}`,
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
		"ResolveUrn":  resolveSpecJSON,
		"GetNodeById": `{"data":{"nodeById":` + badSpecDetail + `}}`,
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

func TestSpecSupersedeRequiresYes(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":  resolveSpecJSON,
		"GetNodeById": `{"data":{"nodeById":` + cleanSpecDetail + `}}`,
		"Nodes":       `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--server", gql.URL})
	err := root.Execute()
	if exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("non-interactive supersede without --yes should be Usage, got err=%v code=%d", err, exitcode.FromError(err))
	}
}

func TestSpecSupersede(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":  resolveSpecJSON,
		"GetNodeById": `{"data":{"nodeById":` + cleanSpecDetail + `}}`,
		"Nodes":       `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:00", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
		"UpsertNode":  `{"data":{"upsertNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:03","name":"msg:010:03 — W2 v2","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"CreateEdge":  `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"sp1","loc":"msg:010:02"},"target":{"id":"new1","loc":"msg:010:03"}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Last CreateEdge is the superseded-by link old -> new.
	var edge struct {
		Label string `json:"label"`
	}
	_ = json.Unmarshal(captured["CreateEdge"], &edge)
	if edge.Label != "superseded-by" {
		t.Errorf("final edge label = %q, want superseded-by", edge.Label)
	}
	// Last UpsertNode is the retire of the old spec (same loc + superseded tag).
	var retire struct {
		Input struct {
			Loc  string   `json:"loc"`
			Tags []string `json:"tags"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["UpsertNode"], &retire)
	if retire.Input.Loc != "msg:010:02" {
		t.Errorf("retire loc = %q (must keep the old loc — no renumber)", retire.Input.Loc)
	}
	if !contains(retire.Input.Tags, "superseded") {
		t.Errorf("retired spec must be tagged superseded, got %v", retire.Input.Tags)
	}

	var dto struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Old != "msg:010:02" || dto.New != "msg:010:03" {
		t.Errorf("supersede dto = %+v", dto)
	}
}

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
