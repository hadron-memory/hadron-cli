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
		if strings.HasPrefix(e.Label, "p1:") && e.Target == "msg:010" {
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

func TestSpecDescribeProduct(t *testing.T) {
	nodes := strings.Join([]string{
		specNodeList("cli", `["spec","p0"]`),
		specNodeList("cli:gen", `["spec","p0"]`),
		specNodeList("cli:cha", `["spec","p1"]`),
		specNodeList("cli:cha:010", `["spec","p1"]`),
		specNodeList("cli:cha:010:01", `["spec","p1"]`),
	}, ",")
	gql, _ := captureGraphQL(t, map[string]string{
		"Nodes": `{"data":{"nodes":[` + nodes + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "describe", "-m", specProductMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Scheme   string   `json:"scheme"`
		Products []string `json:"products"`
		Modules  []string `json:"modules"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Scheme != "product" {
		t.Errorf("scheme = %q, want product", dto.Scheme)
	}
	if len(dto.Products) != 1 || dto.Products[0] != "cli" {
		t.Errorf("products = %v, want [cli]", dto.Products)
	}
	if len(dto.Modules) != 1 || dto.Modules[0] != "cli:cha" {
		t.Errorf("modules = %v, want [cli:cha]", dto.Modules)
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
