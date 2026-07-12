// Package mcpserver implements `hadron mcp-server ...` — the external MCP
// server registry (hadrontool-mcp conduit; hadron-server #634). Org-owned
// McpServer rows whose tools headless runs invoke as `mcp__<slug>__<tool>` in
// a node's `data.tools`.
//
// Registration grants nothing by itself: every run-time call still walks the
// policy chain as `tool.mcp__<slug>__<tool>` plus the run's action budget.
// Static auth headers are encrypted and WRITE-ONLY — readers only ever see
// `hasHeaders`, never the stored values.
package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// NewCmdMcpServer builds the `mcp-server` command group.
func NewCmdMcpServer(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mcp-server <command>",
		Aliases: []string{"mcp-servers"},
		Short:   "Manage the external MCP server registry",
		Long: `Manage registered external MCP servers (hadrontool-mcp conduit).

Each row exposes its tools to headless runs as mcp__<slug>__<tool>, which an
author declares in a node's data.tools. Registration alone grants nothing — a
run must still be allowed 'tool.mcp__<slug>__<tool>' by the policy chain.`,
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdTools(f))
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdDelete(f))
	return cmd
}

// mcpServerDTO is the stable --json shape for an McpServer. Header values are
// never included — the registry only exposes hasHeaders.
type mcpServerDTO struct {
	ID             string   `json:"id"`
	OrganizationID string   `json:"organizationId"`
	Slug           string   `json:"slug"`
	Name           string   `json:"name"`
	URL            string   `json:"url"`
	ToolAllowlist  []string `json:"toolAllowlist"`
	HasHeaders     bool     `json:"hasHeaders"`
	Enabled        bool     `json:"enabled"`
	CreatedAt      string   `json:"createdAt"`
	UpdatedAt      string   `json:"updatedAt"`
}

func dtoFromFields(s gen.McpServerFields) mcpServerDTO {
	dto := mcpServerDTO{
		ID:             s.Id,
		OrganizationID: s.OrganizationId,
		Slug:           s.Slug,
		Name:           s.Name,
		URL:            s.Url,
		ToolAllowlist:  []string{},
		HasHeaders:     s.HasHeaders,
		Enabled:        s.Enabled,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
	dto.ToolAllowlist = append(dto.ToolAllowlist, s.ToolAllowlist...)
	return dto
}

func (s mcpServerDTO) statusLabel() string {
	if s.Enabled {
		return "enabled"
	}
	return "disabled"
}

// allowlistLabel renders the allowlist for a table: the join, or "all" when
// empty (empty allowlist = every tool the server advertises).
func (s mcpServerDTO) allowlistLabel() string {
	if len(s.ToolAllowlist) == 0 {
		return "all"
	}
	return strings.Join(s.ToolAllowlist, ",")
}

// parseHeaders turns repeated `Name: value` flags (curl-style) into a JSON
// object for the write-only headers input. Returns nil when there are none.
// The value may contain colons (e.g. a URL), so it splits on the FIRST colon
// only.
func parseHeaders(pairs []string) (*json.RawMessage, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	headers := map[string]string{}
	for _, p := range pairs {
		name, value, ok := strings.Cut(p, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, exitcode.Newf(exitcode.Usage, "malformed --header %q (want 'Name: value')", p)
		}
		headers[name] = strings.TrimSpace(value)
	}
	raw, err := json.Marshal(headers)
	if err != nil {
		return nil, fmt.Errorf("encoding headers: %w", err)
	}
	msg := json.RawMessage(raw)
	return &msg, nil
}
