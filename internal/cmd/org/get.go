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

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <org>",
		Short: "Show an organization",
		Long: `Show one organization: name, URN, id, whether it is listed on the
marketplace, and when it was last updated. <org> is its id, URN or root
(acme.com); hadron org list shows the orgs you can see. An unknown org exits 4.`,
		Example: `  hadron org get acme.com
  hadron org get hrn:org:acme.com --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GetOrganization(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.Organization == nil {
				return exitcode.Newf(exitcode.NotFound, "organization %q not found", args[0])
			}
			dto := orgDTOFromFields(resp.Organization.OrgFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				listing := "not listed on marketplace"
				if dto.ListedOnMarketplace {
					listing = "listed on marketplace"
				}
				fmt.Fprintf(w, "%s\n  urn: %s\n  id: %s\n  %s\n  updated: %s\n", output.SafeCell(dto.Name), output.SafeCell(dto.URN), output.SafeCell(dto.ID), listing, output.SafeCell(dto.UpdatedAt))
				return nil
			})
		},
	}
}
