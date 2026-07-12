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

func newCmdDelete(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a registered MCP server (org ADMIN)",
		Long: `Hard-delete a registered MCP server (org ADMIN/OWNER). This is irreversible;
runs referencing its mcp__<slug>__<tool> tools fail loud afterwards.`,
		Example: `  hadron mcp-server delete mcp_123 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, fmt.Sprintf("MCP server %s (hard delete)", args[0])); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.DeleteMcpServer(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			// The server throws NOT_FOUND rather than returning false today, but
			// honor a false defensively instead of reporting a delete that didn't
			// happen.
			if resp == nil || !resp.DeleteMcpServer {
				return exitcode.Newf(exitcode.NotFound, "MCP server %q not found", args[0])
			}
			dto := map[string]string{"id": args[0], "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ deleted MCP server %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
