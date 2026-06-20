package org

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var name, urn string
	cmd := &cobra.Command{
		Use:     "create --name <name> --urn <urn>",
		Short:   "Create an organization",
		Example: `  hadron org create --name "Acme" --urn acme.com`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateOrganization(cmd.Context(), client, name, urn)
			if err != nil {
				return api.MapError(err)
			}
			dto := orgDTOFromFields(resp.CreateOrganization.OrgFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ created org %s (%s)\n", dto.URN, dto.ID)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "organization display name")
	cmd.Flags().StringVar(&urn, "urn", "", "organization URN (e.g. acme.com)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("urn")
	return cmd
}
