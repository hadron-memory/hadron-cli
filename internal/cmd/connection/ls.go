package connection

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var connection string
	cmd := &cobra.Command{
		Use:     "list [--connection <ref>]",
		Aliases: []string{"ls"},
		Short:   "List connection grants",
		Long: `List connection grants on the connections you own. With --connection, narrows
to grants on that one connection. Pages to exhaustion.`,
		Example: `  hadron connection grant ls
  hadron connection grant ls --connection conn_123 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var connRef *string
			if connection != "" {
				connRef = &connection
			}
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.ConnectionGrantsConnectionGrantsConnectionGrantsPageItemsConnectionGrant, int, error) {
				resp, err := gen.ConnectionGrants(cmd.Context(), client, connRef, &limit, &offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.ConnectionGrants == nil {
					return nil, 0, nil
				}
				return resp.ConnectionGrants.Items, resp.ConnectionGrants.Total, nil
			})
			if err != nil {
				return err
			}

			grants := make([]connectionGrantDTO, 0, len(items))
			for _, g := range items {
				if g == nil {
					continue
				}
				grants = append(grants, dtoFromFields(g.ConnectionGrantFields))
			}

			return output.Write(f.IOStreams, f.JSON, grants, func(w io.Writer) error {
				table := output.NewTable(w, "ID", "CONNECTION", "GRANTEE", "SCOPES", "EXPIRY", "CREATED")
				for _, g := range grants {
					table.Row(g.ID, g.ConnectionID, g.grantee(), g.scopeList(), g.expiry(), g.CreatedAt)
				}
				return table.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&connection, "connection", "", "narrow to grants on one connection (ID or ref)")
	return cmd
}
