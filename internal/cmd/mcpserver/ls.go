package mcpserver

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var org string
	cmd := &cobra.Command{
		Use:     "list [--org <ref>]",
		Aliases: []string{"ls"},
		Short:   "List registered MCP servers",
		Long:    `List registered external MCP servers, optionally scoped to one org. Pages to exhaustion.`,
		Example: `  hadron mcp-server ls --org acme.com --json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var orgRef *string
			if org != "" {
				orgRef = &org
			}
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.McpServersMcpServersMcpServersPageItemsMcpServer, int, error) {
				resp, err := gen.McpServers(cmd.Context(), client, orgRef, &limit, &offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.McpServers == nil {
					return nil, 0, nil
				}
				return resp.McpServers.Items, resp.McpServers.Total, nil
			})
			if err != nil {
				return err
			}

			servers := make([]mcpServerDTO, 0, len(items))
			for _, s := range items {
				if s == nil {
					continue
				}
				servers = append(servers, dtoFromFields(s.McpServerFields))
			}

			return output.Write(f.IOStreams, f.JSON, servers, func(w io.Writer) error {
				table := output.NewTable(w, "ID", "SLUG", "NAME", "URL", "ALLOW", "HEADERS", "STATUS")
				for _, s := range servers {
					headers := "-"
					if s.HasHeaders {
						headers = "✓"
					}
					table.Row(s.ID, s.Slug, s.Name, s.URL, s.allowlistLabel(), headers, s.statusLabel())
				}
				return table.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization to scope the list to (ID or URN)")
	return cmd
}
