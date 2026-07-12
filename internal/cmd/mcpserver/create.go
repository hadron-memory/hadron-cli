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

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var (
		org, slug, name, url string
		headerPairs, allow   []string
		disabled             bool
	)
	cmd := &cobra.Command{
		Use:   "create --org <ref> --slug <slug> --name <name> --url <url>",
		Short: "Register an external MCP server (org ADMIN)",
		Long: `Register an external MCP server (org ADMIN/OWNER). The slug is immutable and
forms the run-tool prefix mcp__<slug>__<tool> (lowercase [a-z0-9-], unique per
org). --url is the streamable-HTTP MCP endpoint.

--header 'Name: value' (repeatable, curl-style) stores encrypted, WRITE-ONLY
auth headers — they are never returned by any read. --allow <tool> (repeatable)
restricts the exposed tools; omit for every tool the server advertises.
--disabled registers it inactive.

Registration grants nothing on its own — a run must still be allowed
'tool.mcp__<slug>__<tool>' by the policy chain.`,
		Example: `  hadron mcp-server create --org acme.com --slug weather --name "Weather" --url https://mcp.example.com/weather
  hadron mcp-server create --org acme.com --slug gh --name GitHub --url https://mcp.example.com/gh \
    --header 'Authorization: Bearer sk-...' --allow search_issues --allow get_pr`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			headers, err := parseHeaders(headerPairs)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var allowArg *[]string
			if cmd.Flags().Changed("allow") {
				allowArg = &allow
			}
			var enabledArg *bool
			if disabled {
				no := false
				enabledArg = &no
			}
			resp, err := gen.CreateMcpServer(cmd.Context(), client, org, slug, name, url, headers, allowArg, enabledArg)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.CreateMcpServer == nil {
				return exitcode.Newf(exitcode.Error, "server returned no MCP-server payload")
			}
			dto := dtoFromFields(resp.CreateMcpServer.McpServerFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ registered MCP server %s (id %s, %s)\n", dto.Slug, dto.ID, dto.statusLabel())
				return err
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "owning organization (ID or URN; required)")
	cmd.Flags().StringVar(&slug, "slug", "", "run-tool slug (immutable; lowercase [a-z0-9-]; required)")
	cmd.Flags().StringVar(&name, "name", "", "display name (required)")
	cmd.Flags().StringVar(&url, "url", "", "streamable-HTTP MCP endpoint URL (required)")
	cmd.Flags().StringArrayVar(&headerPairs, "header", nil, "static auth header 'Name: value' (repeatable; write-only)")
	cmd.Flags().StringArrayVar(&allow, "allow", nil, "expose only this tool (repeatable; omit for all)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "register inactive")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("slug")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}
