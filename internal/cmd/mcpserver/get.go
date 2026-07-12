package mcpserver

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "get <id>",
		Aliases: []string{"show"},
		Short:   "Show one registered MCP server",
		Example: `  hadron mcp-server get mcp_123 --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.McpServer(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.McpServer == nil {
				return exitcode.Newf(exitcode.NotFound, "MCP server %q not found", args[0])
			}
			dto := dtoFromFields(resp.McpServer.McpServerFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "%s\n  id: %s\n  slug: %s\n  url: %s\n", dto.Name, dto.ID, dto.Slug, dto.URL)
				fmt.Fprintf(w, "  org: %s\n  status: %s\n", dto.OrganizationID, dto.statusLabel())
				fmt.Fprintf(w, "  allowlist: %s\n", dto.allowlistLabel())
				headers := "none"
				if dto.HasHeaders {
					headers = "set (write-only)"
				}
				fmt.Fprintf(w, "  headers: %s\n", headers)
				fmt.Fprintf(w, "  updated: %s\n", dto.UpdatedAt)
				return nil
			})
		},
	}
	return cmd
}
