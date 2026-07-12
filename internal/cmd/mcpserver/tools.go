package mcpserver

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// mcpToolDTO is the stable --json shape for one advertised tool.
type mcpToolDTO struct {
	Name        string           `json:"name"`
	Description *string          `json:"description"`
	RunToolName string           `json:"runToolName"`
	InputSchema *json.RawMessage `json:"inputSchema,omitempty"`
}

func newCmdTools(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools <id>",
		Short: "List the tools a registered MCP server advertises",
		Long: `List the live tools an MCP server advertises, as admitted by its registry row
(allowlist + name grammar applied). The runToolName (mcp__<slug>__<tool>) is
what you declare in a node's data.tools. This makes a live tools/list call to
the external server.`,
		Example: `  hadron mcp-server tools mcp_123 --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.McpServerTools(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			// The list itself is nullable: the server returns null for a missing
			// server or a non-member caller — distinct from a real, empty tool set.
			// Treat null as not-found, consistent with get/update/delete.
			if resp == nil || resp.McpServerTools == nil {
				return exitcode.Newf(exitcode.NotFound, "MCP server %q not found", args[0])
			}
			tools := make([]mcpToolDTO, 0, len(resp.McpServerTools))
			for _, t := range resp.McpServerTools {
				if t == nil {
					continue
				}
				tools = append(tools, mcpToolDTO{
					Name:        t.Name,
					Description: t.Description,
					RunToolName: t.RunToolName,
					InputSchema: t.InputSchema,
				})
			}
			return output.Write(f.IOStreams, f.JSON, tools, func(w io.Writer) error {
				table := output.NewTable(w, "TOOL", "RUN-TOOL-NAME", "DESCRIPTION")
				for _, t := range tools {
					table.Row(t.Name, t.RunToolName, output.Dash(t.Description))
				}
				return table.Flush()
			})
		},
	}
	return cmd
}
