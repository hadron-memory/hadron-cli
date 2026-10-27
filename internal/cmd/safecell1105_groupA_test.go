package cmd

import (
	"testing"
)

// cli#1105 group A: mcp-server, ha and ai-config text views.

func mcpServer1105A(id, cell string) string {
	return `{"id":` + q1105(id+cell) + `,"organizationId":` + q1105(cell) + `,"slug":` + q1105(cell) + `,"name":` + q1105(cell) +
		`,"url":` + q1105(cell) + `,"toolAllowlist":[` + q1105(cell) + `],"hasHeaders":true,"enabled":true,` +
		`"createdAt":` + q1105(cell) + `,"updatedAt":` + q1105(cell) + `}`
}

func haInstance1105A(id, cell string) string {
	return `{"id":` + q1105(id+cell) + `,"organizationId":` + q1105(cell) + `,"slug":` + q1105(cell) + `,"name":` + q1105(cell) +
		`,"url":` + q1105(cell) + `,"enabled":true,"opAllowlist":[` + q1105(cell) + `],` +
		`"createdAt":` + q1105(cell) + `,"updatedAt":` + q1105(cell) + `,"createdBy":` + q1105(cell) + `,"updatedBy":` + q1105(cell) + `}`
}

// aiConfig1105A is a masked config; withEndpoint adds the endpoint fields the
// WithEndpoint operations select.
func aiConfig1105A(id, cell string, withEndpoint bool) string {
	s := `{"id":` + q1105(id+cell) + `,"name":` + q1105(cell) + `,"ownerType":"APP","ownerId":` + q1105(cell) +
		`,"provider":` + q1105(cell) + `,"model":` + q1105(cell) + `,"hasApiKey":true,"apiKeyPreview":` + q1105(cell) +
		`,"params":null,"enabled":true,"createdAt":` + q1105(cell) + `,"updatedAt":null`
	if withEndpoint {
		s += `,"endpoint":` + q1105(cell) + `,"effectiveEndpoint":` + q1105(cell)
	}
	return s + `}`
}

func TestSafeCell1105AMcpServer(t *testing.T) {
	one := func(op, field string) func(string) map[string]string {
		return func(cell string) map[string]string {
			return map[string]string{op: `{"data":{"` + field + `":` + mcpServer1105A("m", cell) + `}}`}
		}
	}
	for _, c := range []safeCell1105Case{
		{name: "mcp-server ls", args: []string{"mcp-server", "ls", "--org", "acme.com"}, lines: 3,
			resp: func(cell string) map[string]string {
				return map[string]string{"McpServers": `{"data":{"mcpServers":{"items":[` + mcpServer1105A("a", cell) + `,` + mcpServer1105A("b", cell) + `],"total":2}}}`}
			}},
		{name: "mcp-server tools", args: []string{"mcp-server", "tools", "mcp_1"}, lines: 3,
			resp: func(cell string) map[string]string {
				tool := `{"name":` + q1105(cell) + `,"description":` + q1105(cell) + `,"runToolName":` + q1105(cell) + `,"inputSchema":null}`
				return map[string]string{"McpServerTools": `{"data":{"mcpServerTools":[` + tool + `,` + tool + `]}}`}
			}},
		{name: "mcp-server get", args: []string{"mcp-server", "get", "mcp_1"}, lines: 9, resp: one("McpServer", "mcpServer")},
		{name: "mcp-server create", args: []string{"mcp-server", "create", "--org", "acme.com", "--slug", "w", "--name", "W", "--url", "https://x"},
			lines: 1, resp: one("CreateMcpServer", "createMcpServer")},
		{name: "mcp-server update", args: []string{"mcp-server", "update", "mcp_1", "--url", "https://new"},
			lines: 1, resp: one("UpdateMcpServer", "updateMcpServer")},
	} {
		assertSafeCell1105(t, c)
	}
}

func TestSafeCell1105AHa(t *testing.T) {
	for _, c := range []safeCell1105Case{
		{name: "ha list", args: []string{"ha", "list", "--org", "acme.com"}, lines: 3,
			resp: func(cell string) map[string]string {
				return map[string]string{"HomeAssistantInstances": `{"data":{"homeAssistantInstances":{"items":[` + haInstance1105A("a", cell) + `,` + haInstance1105A("b", cell) + `],"total":2}}}`}
			}},
		{name: "ha get", args: []string{"ha", "get", "ha1"}, lines: 9,
			resp: func(cell string) map[string]string {
				return map[string]string{"HomeAssistantInstance": `{"data":{"homeAssistantInstance":` + haInstance1105A("a", cell) + `}}`}
			}},
		{name: "ha ops", args: []string{"ha", "ops", "ha1"}, lines: 3,
			resp: func(cell string) map[string]string {
				op := `{"op":` + q1105(cell) + `,"runToolName":` + q1105(cell) + `,"description":` + q1105(cell) + `,"inputSchema":null}`
				return map[string]string{"HomeAssistantInstanceOps": `{"data":{"homeAssistantInstanceOps":[` + op + `,` + op + `]}}`}
			}},
	} {
		assertSafeCell1105(t, c)
	}
}

func TestSafeCell1105AAiConfig(t *testing.T) {
	list := func(op string, withEndpoint bool) func(string) map[string]string {
		return func(cell string) map[string]string {
			return map[string]string{op: `{"data":{"resolveAiServiceConfigs":[` + aiConfig1105A("a", cell, withEndpoint) + `,` + aiConfig1105A("b", cell, withEndpoint) + `]}}`}
		}
	}
	one := func(op, field string, withEndpoint bool) func(string) map[string]string {
		return func(cell string) map[string]string {
			return map[string]string{op: `{"data":{"` + field + `":` + aiConfig1105A("a", cell, withEndpoint) + `}}`}
		}
	}
	for _, c := range []safeCell1105Case{
		{name: "ai-config list", args: []string{"ai-config", "list", "--app", "acme.com:juno-app"}, lines: 3,
			resp: list("ResolveAiServiceConfigs", false)},
		{name: "ai-config list --with-endpoints", args: []string{"ai-config", "list", "--app", "acme.com:juno-app", "--with-endpoints"}, lines: 3,
			resp: list("ResolveAiServiceConfigsWithEndpoint", true)},
		{name: "ai-config endpoints", args: []string{"ai-config", "endpoints", "glm"}, lines: 3,
			resp: func(cell string) map[string]string {
				e := `{"url":` + q1105(cell) + `,"label":` + q1105(cell) + `,"isDefault":true,"note":` + q1105(cell) + `}`
				return map[string]string{"AiProviderEndpoints": `{"data":{"aiProviderEndpoints":[` + e + `,` + e + `]}}`}
			}},
		{name: "ai-config get", args: []string{"ai-config", "get", "cfg1"}, lines: 4,
			resp: one("AiServiceConfigWithEndpoint", "aiServiceConfig", true)},
		{name: "ai-config create", args: []string{"ai-config", "create", "--org", "acme.com", "--name", "x", "--provider", "p", "--model", "m"}, lines: 2,
			resp: one("CreateAiServiceConfig", "createAiServiceConfig", false)},
		{name: "ai-config create --endpoint", args: []string{"ai-config", "create", "--org", "acme.com", "--name", "x", "--provider", "p", "--model", "m", "--endpoint", "https://e"}, lines: 4,
			resp: one("CreateAiServiceConfigWithEndpoint", "createAiServiceConfig", true)},
		{name: "ai-config update", args: []string{"ai-config", "update", "cfg1", "--model", "m"}, lines: 2,
			resp: one("UpdateAiServiceConfig", "updateAiServiceConfig", false)},
		{name: "ai-config update --endpoint", args: []string{"ai-config", "update", "cfg1", "--endpoint", "https://e"}, lines: 4,
			resp: one("UpdateAiServiceConfigWithEndpoint", "updateAiServiceConfig", true)},
	} {
		assertSafeCell1105(t, c)
	}
}
