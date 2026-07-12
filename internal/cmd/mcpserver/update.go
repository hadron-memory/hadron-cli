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

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var (
		name, url          string
		headerPairs, allow []string
		clearHeaders       bool
		clearAllow         bool
		enabled, disabled  bool
	)
	cmd := &cobra.Command{
		Use:   "update <id> [flags]",
		Short: "Update a registered MCP server (org ADMIN)",
		Long: `Update a registered MCP server (org ADMIN/OWNER). The slug is immutable — flow
nodes reference it in data.tools. Only the fields you pass change.

--header 'Name: value' (repeatable) REPLACES the stored header object;
--clear-headers removes it (pass one or the other, not both). --allow
(repeatable) replaces the tool allowlist; --clear-allow empties it (exposing
every tool the server advertises). --enabled / --disabled toggle the server's
active state.`,
		Example: `  hadron mcp-server update mcp_123 --url https://mcp.example.com/v2
  hadron mcp-server update mcp_123 --header 'Authorization: Bearer sk-new'
  hadron mcp-server update mcp_123 --clear-allow --disabled`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Gate on flag presence (Changed), not the parsed value, so an explicit
			// --enabled=false / --clear-headers=false is still recognized as "a flag
			// was passed" rather than silently read as unset.
			changed := func(n string) bool { return cmd.Flags().Changed(n) }
			if !changed("name") && !changed("url") && !changed("header") &&
				!changed("clear-headers") && !changed("allow") && !changed("clear-allow") &&
				!changed("enabled") && !changed("disabled") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}

			headers, err := parseHeaders(headerPairs)
			if err != nil {
				return err
			}
			var clearArg *bool
			if changed("clear-headers") {
				v := true
				clearArg = &v
			}
			var enabledArg *bool
			switch {
			case changed("enabled"):
				v := true
				enabledArg = &v
			case changed("disabled"):
				v := false
				enabledArg = &v
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var nameArg, urlArg *string
			if changed("name") {
				nameArg = &name
			}
			if changed("url") {
				urlArg = &url
			}
			// --allow replaces the allowlist; --clear-allow empties it (sends an
			// explicit [] = all tools). Mutually exclusive.
			var allowArg *[]string
			switch {
			case changed("allow"):
				allowArg = &allow
			case changed("clear-allow"):
				empty := []string{}
				allowArg = &empty
			}
			resp, err := gen.UpdateMcpServer(cmd.Context(), client, args[0], nameArg, urlArg, headers, clearArg, allowArg, enabledArg)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.UpdateMcpServer == nil {
				return exitcode.Newf(exitcode.NotFound, "MCP server %q not found", args[0])
			}
			dto := dtoFromFields(resp.UpdateMcpServer.McpServerFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ updated MCP server %s (id %s, %s)\n", dto.Slug, dto.ID, dto.statusLabel())
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new display name")
	cmd.Flags().StringVar(&url, "url", "", "new endpoint URL")
	cmd.Flags().StringArrayVar(&headerPairs, "header", nil, "replace auth headers with 'Name: value' (repeatable; write-only)")
	cmd.Flags().BoolVar(&clearHeaders, "clear-headers", false, "remove all stored auth headers")
	cmd.Flags().StringArrayVar(&allow, "allow", nil, "replace the exposed-tool allowlist (repeatable)")
	cmd.Flags().BoolVar(&clearAllow, "clear-allow", false, "empty the allowlist (expose all tools)")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "enable the server")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "disable the server")
	cmd.MarkFlagsMutuallyExclusive("header", "clear-headers")
	cmd.MarkFlagsMutuallyExclusive("allow", "clear-allow")
	cmd.MarkFlagsMutuallyExclusive("enabled", "disabled")
	return cmd
}
