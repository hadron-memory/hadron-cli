package grant

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var org, user string
	cmd := &cobra.Command{
		Use:     "list [--org <ref>] [--user <ref>]",
		Aliases: []string{"ls"},
		Short:   "List action grants (default: your own)",
		Long: `List action grants. With no flags, lists YOUR OWN grants across orgs
(self-audit is never gated). An org ADMIN passing --org sees that whole
org's grants, optionally narrowed by --user; a non-admin passing --org stays
pinned to their own grants there. Pages to exhaustion.`,
		Example: `  hadron grant ls
  hadron grant ls --org acme.com
  hadron grant ls --org acme.com --user jane --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var orgRef, userRef *string
			if org != "" {
				orgRef = &org
			}
			if user != "" {
				userRef = &user
			}
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.PrincipalGrantsPrincipalGrantsPrincipalGrantsPageItemsPrincipalGrant, int, error) {
				resp, err := gen.PrincipalGrants(cmd.Context(), client, orgRef, userRef, &limit, &offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.PrincipalGrants == nil {
					return nil, 0, nil
				}
				return resp.PrincipalGrants.Items, resp.PrincipalGrants.Total, nil
			})
			if err != nil {
				return err
			}

			grants := make([]grantDTO, 0, len(items))
			for _, g := range items {
				if g == nil {
					continue
				}
				grants = append(grants, dtoFromFields(g.PrincipalGrantFields))
			}

			return output.Write(f.IOStreams, f.JSON, grants, func(w io.Writer) error {
				table := output.NewTable(w, "ID", "GRANTEE", "ORG", "ACTIONS", "EXPIRY", "CREATED")
				for _, g := range grants {
					table.Row(g.ID, g.grantee(), output.Dash(g.OrganizationURN), g.actionList(), g.expiry(), g.CreatedAt)
				}
				return table.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization to list (ID or URN; org ADMINs see the whole org)")
	cmd.Flags().StringVar(&user, "user", "", "narrow an org listing to one grantee (org ADMIN only)")
	return cmd
}
