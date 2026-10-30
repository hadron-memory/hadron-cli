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

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var name, urn string
	cmd := &cobra.Command{
		Use:   "create --name <name> --urn <urn>",
		Short: "Create an organization",
		Long: `Create an organization. Any signed-in user can; you become its OWNER.
It does not become your active org (run hadron org use <urn> for that).

--name is the display name. --urn is its root: a lowercase name, dotted or not
(acme.com or acme), which forms its URN hrn:org:<root> and the URNs of
everything in it. Roots share one pool with user handles, and a root once
used stays reserved, even after the org is deleted: a taken root is refused
with "The name "<root>" is already taken."

Prints "✓ created org hrn:org:<root> (<id>)"; --json gives id, name, urn and
the org's other fields.`,
		Example: `  hadron org create --name "Acme" --urn acme.com
  hadron org use acme.com`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateOrgSlug("--urn", urn); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateOrganization(cmd.Context(), client, name, urn)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateOrganization == nil {
				return exitcode.Newf(exitcode.Error, "server returned no organization")
			}
			dto := orgDTOFromFields(resp.CreateOrganization.OrgFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ created org %s (%s)\n", dto.URN, dto.ID)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "organization display name (required)")
	cmd.Flags().StringVar(&urn, "urn", "", "organization URN: a lowercase slug, dotted or not (e.g. acme.com or acme) (required)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("urn")
	return cmd
}
