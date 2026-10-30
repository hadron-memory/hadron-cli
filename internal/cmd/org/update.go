package org

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
	var name, urn string
	var marketplace bool
	cmd := &cobra.Command{
		Use:   "update <org>",
		Short: "Update an organization",
		Long: `Update an organization; only the fields you pass change. Needs ADMIN or
OWNER in the org (else exit 8). <org> is its id, URN or root.

--name changes the display name.

--urn renames the org's root, which changes its URN and the URNs of its
memories, Apps and agents. The old URNs keep working as aliases and the old
root stays reserved to the org. A root that is already taken is refused, and
so is a rename that would clash with an existing URN inside the org.

--marketplace (or --marketplace=true) lists the org on the marketplace
catalogue and --marketplace=false unlists it. Listing advertises the org; it
grants nobody access.`,
		Example: `  hadron org update acme.com --name "Acme Inc"
  hadron org update acme.com --urn acme-inc.com
  hadron org update acme.com --marketplace         # list on the marketplace
  hadron org update acme.com --marketplace=false   # unlist`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("name") && !changed("urn") && !changed("marketplace") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass --name, --urn, or --marketplace")
			}
			if changed("urn") {
				if err := cmdutil.ValidateOrgSlug("--urn", urn); err != nil {
					return err
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var nameArg, urnArg *string
			var marketplaceArg *bool
			if changed("name") {
				nameArg = &name
			}
			if changed("urn") {
				urnArg = &urn
			}
			if changed("marketplace") {
				marketplaceArg = &marketplace
			}
			resp, err := gen.UpdateOrganization(cmd.Context(), client, args[0], nameArg, urnArg, marketplaceArg)
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateOrganization == nil {
				return exitcode.Newf(exitcode.Error, "server returned no organization")
			}
			dto := orgDTOFromFields(resp.UpdateOrganization.OrgFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ updated org %s (%s)\n", dto.URN, dto.ID)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new display name")
	cmd.Flags().StringVar(&urn, "urn", "", "new URN: a lowercase slug, dotted or not (e.g. acme.com or acme)")
	cmd.Flags().BoolVar(&marketplace, "marketplace", false, "list this org on the marketplace catalogue: --marketplace to list, --marketplace=false to unlist (advertisement, not access)")
	return cmd
}
