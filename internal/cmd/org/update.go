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
	var visible bool
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an organization",
		Long: `Update an organization by id. Only the fields you pass change.
Use --visible=false to hide it from listings.`,
		Example: `  hadron org update org_123 --name "Acme Inc"
  hadron org update org_123 --visible=false`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("name") && !changed("urn") && !changed("visible") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass --name, --urn, or --visible")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var nameArg, urnArg *string
			var visArg *bool
			if changed("name") {
				nameArg = &name
			}
			if changed("urn") {
				urnArg = &urn
			}
			if changed("visible") {
				visArg = &visible
			}
			resp, err := gen.UpdateOrganization(cmd.Context(), client, args[0], nameArg, urnArg, visArg)
			if err != nil {
				return api.MapError(err)
			}
			dto := orgDTOFromFields(resp.UpdateOrganization.OrgFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ updated org %s (%s)\n", dto.URN, dto.ID)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new display name")
	cmd.Flags().StringVar(&urn, "urn", "", "new URN")
	cmd.Flags().BoolVar(&visible, "visible", true, "organization visibility (e.g. --visible=false)")
	return cmd
}
