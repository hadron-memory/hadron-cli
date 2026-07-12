package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const mcpServerJSON = `{"id":"mcp_1","organizationId":"o1","slug":"weather","name":"Weather",
	"url":"https://mcp.example.com/weather","toolAllowlist":["get_forecast"],"hasHeaders":true,
	"enabled":true,"createdAt":"2026-07-01T00:00:00Z","updatedAt":"2026-07-02T00:00:00Z"}`

func TestMcpServerLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"McpServers": `{"data":{"mcpServers":{"items":[` + mcpServerJSON + `],"total":1}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "ls", "--org", "acme.com", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("--json invalid: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0]["slug"] != "weather" {
		t.Errorf("unexpected ls output: %s", out.String())
	}
	var vars struct {
		OrgId *string `json:"orgId"`
	}
	_ = json.Unmarshal(captured["McpServers"], &vars)
	if vars.OrgId == nil || *vars.OrgId != "acme.com" {
		t.Errorf("--org should forward as orgId, got %v", vars.OrgId)
	}
}

func TestMcpServerGet(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"McpServer": `{"data":{"mcpServer":` + mcpServerJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "get", "mcp_1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// hasHeaders is surfaced, but never the values.
	for _, want := range []string{"weather", "write-only", "get_forecast"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestMcpServerGetNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"McpServer": `{"data":{"mcpServer":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "get", "missing", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a not-found error for a null mcpServer")
	}
}

func TestMcpServerTools(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"McpServerTools": `{"data":{"mcpServerTools":[
			{"name":"get_forecast","description":"Weather","runToolName":"mcp__weather__get_forecast","inputSchema":{"type":"object"}}]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "tools", "mcp_1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "mcp__weather__get_forecast") {
		t.Errorf("tools output must show the runToolName:\n%s", out.String())
	}
}

// A null tools list means the server is missing / not visible — surfaced as
// NotFound, not a successful empty list.
func TestMcpServerToolsNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"McpServerTools": `{"data":{"mcpServerTools":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "tools", "missing", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a not-found error when mcpServerTools is null")
	}
}

// create forwards required fields; --header builds the write-only JSON object,
// --allow the allowlist, --disabled sends enabled:false.
func TestMcpServerCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMcpServer": `{"data":{"createMcpServer":` + mcpServerJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "create",
		"--org", "acme.com", "--slug", "gh", "--name", "GitHub", "--url", "https://mcp.example.com/gh",
		"--header", "Authorization: Bearer sk-x", "--allow", "search", "--allow", "get_pr",
		"--disabled", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "registered MCP server") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars struct {
		OrgRef        string            `json:"orgRef"`
		Slug          string            `json:"slug"`
		Name          string            `json:"name"`
		Url           string            `json:"url"`
		Headers       map[string]string `json:"headers"`
		ToolAllowlist []string          `json:"toolAllowlist"`
		Enabled       *bool             `json:"enabled"`
	}
	_ = json.Unmarshal(captured["CreateMcpServer"], &vars)
	if vars.OrgRef != "acme.com" || vars.Slug != "gh" || vars.Name != "GitHub" || vars.Url != "https://mcp.example.com/gh" {
		t.Errorf("unexpected create vars: %+v", vars)
	}
	// The value contains a colon (Bearer ...) — only the first colon splits.
	if vars.Headers["Authorization"] != "Bearer sk-x" {
		t.Errorf("--header should build {Authorization: Bearer sk-x}, got %v", vars.Headers)
	}
	if strings.Join(vars.ToolAllowlist, ",") != "search,get_pr" {
		t.Errorf("--allow should build the allowlist, got %v", vars.ToolAllowlist)
	}
	if vars.Enabled == nil || *vars.Enabled != false {
		t.Errorf("--disabled should send enabled:false, got %v", vars.Enabled)
	}
}

// With no --header/--allow/--disabled, those inputs are omitted (server default).
func TestMcpServerCreateOmitsUnset(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMcpServer": `{"data":{"createMcpServer":` + mcpServerJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "create", "--org", "acme.com", "--slug", "w", "--name", "W", "--url", "https://x", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateMcpServer"], &vars)
	for _, key := range []string{"headers", "toolAllowlist", "enabled"} {
		if v, present := vars[key]; present {
			t.Errorf("unset %q must be omitted from create variables, got %v", key, v)
		}
	}
}

func TestMcpServerUpdate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMcpServer": `{"data":{"updateMcpServer":` + mcpServerJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "update", "mcp_1", "--url", "https://new", "--clear-headers", "--disabled", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Ref          string  `json:"ref"`
		Url          *string `json:"url"`
		ClearHeaders *bool   `json:"clearHeaders"`
		Enabled      *bool   `json:"enabled"`
		Name         *string `json:"name"`
	}
	_ = json.Unmarshal(captured["UpdateMcpServer"], &vars)
	if vars.Ref != "mcp_1" || vars.Url == nil || *vars.Url != "https://new" {
		t.Errorf("unexpected update vars: %+v", vars)
	}
	if vars.ClearHeaders == nil || !*vars.ClearHeaders {
		t.Errorf("--clear-headers should send clearHeaders:true, got %v", vars.ClearHeaders)
	}
	if vars.Enabled == nil || *vars.Enabled != false {
		t.Errorf("--disabled should send enabled:false, got %v", vars.Enabled)
	}
	if vars.Name != nil {
		t.Errorf("unset --name must be omitted, got %v", *vars.Name)
	}
}

func TestMcpServerUpdateNothingToUpdate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMcpServer": `{"data":{"updateMcpServer":` + mcpServerJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "update", "mcp_1", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a usage error when no field flags are passed")
	}
	if _, called := captured["UpdateMcpServer"]; called {
		t.Error("mutation must not be sent when there is nothing to update")
	}
}

// --header and --clear-headers are mutually exclusive.
func TestMcpServerUpdateHeaderClearMutuallyExclusive(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"UpdateMcpServer": `{"data":{"updateMcpServer":` + mcpServerJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "update", "mcp_1", "--header", "A: b", "--clear-headers", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error passing both --header and --clear-headers")
	}
}

// --clear-allow sends an explicit empty allowlist ([] = all tools) — the only
// way to reset a restricted allowlist.
func TestMcpServerUpdateClearAllow(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMcpServer": `{"data":{"updateMcpServer":` + mcpServerJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "update", "mcp_1", "--clear-allow", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		ToolAllowlist *[]string `json:"toolAllowlist"`
	}
	_ = json.Unmarshal(captured["UpdateMcpServer"], &vars)
	if vars.ToolAllowlist == nil {
		t.Fatal("--clear-allow must send toolAllowlist (as []), not omit it")
	}
	if len(*vars.ToolAllowlist) != 0 {
		t.Errorf("--clear-allow should send an empty allowlist, got %v", *vars.ToolAllowlist)
	}
}

// --allow and --clear-allow are mutually exclusive.
func TestMcpServerUpdateAllowClearMutuallyExclusive(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"UpdateMcpServer": `{"data":{"updateMcpServer":` + mcpServerJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "update", "mcp_1", "--allow", "x", "--clear-allow", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error passing both --allow and --clear-allow")
	}
}

func TestMcpServerDelete(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteMcpServer": `{"data":{"deleteMcpServer":true}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "delete", "mcp_1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "deleted MCP server mcp_1") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(captured["DeleteMcpServer"], &vars)
	if vars.Ref != "mcp_1" {
		t.Errorf("ref should be mcp_1, got %q", vars.Ref)
	}
}

func TestMcpServerDeleteRequiresYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteMcpServer": `{"data":{"deleteMcpServer":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "delete", "mcp_1", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected refusal without --yes")
	}
	if _, called := captured["DeleteMcpServer"]; called {
		t.Error("mutation must not be sent when confirmation is refused")
	}
}

func TestMcpServerDeleteFalseIsNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"DeleteMcpServer": `{"data":{"deleteMcpServer":false}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"mcp-server", "delete", "gone", "--yes", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a not-found error when the server returns false")
	}
}
