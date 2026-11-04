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
		Long: `List action grants.

  (no flags)                   your own grants in every org
  --org <org>                  as an org ADMIN or OWNER: every grant in it;
                               otherwise: only your own grants there
  --org <org> --user <user>    an ADMIN's listing narrowed to one grantee

--user is ignored without --org. Every page is fetched. Revoked grants are not
listed.
The table shows ID (what grant revoke takes), GRANTEE, ORG, ACTIONS, EXPIRY
("perpetual" when none) and CREATED. --json gives an array of id,
principalType, principalId, principalHandle, organizationId, organizationUrn,
actions, expiresAt and createdAt; an empty list is [].`,
		Example: `  hadron grant list
  hadron grant list --org acme.com
  hadron grant list --org acme.com --user jane --json`,
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
					table.Row(output.SafeCell(g.ID), output.SafeCell(g.grantee()), output.SafeCell(output.Dash(g.OrganizationURN)), output.SafeCell(g.actionList()), output.SafeCell(g.expiry()), output.SafeCell(g.CreatedAt))
				}
				return table.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization to list (ID or URN; org ADMINs see the whole org)")
	cmd.Flags().StringVar(&user, "user", "", "narrow an org listing to one grantee (org ADMIN only)")
	return cmd
}
