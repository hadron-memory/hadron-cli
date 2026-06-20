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
		Use:     "get <id>",
		Short:   "Show an organization",
		Example: `  hadron org get org_123 --json`,
		Args:    cobra.ExactArgs(1),
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
				vis := "visible"
				if dto.IsVisible != nil && !*dto.IsVisible {
					vis = "hidden"
				}
				fmt.Fprintf(w, "%s\n  urn: %s\n  id: %s\n  %s\n  updated: %s\n", dto.Name, dto.URN, dto.ID, vis, dto.UpdatedAt)
				return nil
			})
		},
	}
}
