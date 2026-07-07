package org

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type orgListItem = gen.OrganizationsOrganizationsOrganizationsPageItemsOrganization

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var mine bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List organizations",
		Long: `List organizations.

By default this spans every organization you can see (for a platform admin, all
live orgs). Pass --mine to restrict to organizations you're a member of.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var filter *gen.OrganizationFilter
			if mine {
				t := true
				filter = &gen.OrganizationFilter{MemberOnly: &t}
			}
			// Paged to exhaustion — the contract is "all matching orgs".
			items, err := api.CollectAll(func(limit, offset int) ([]*orgListItem, int, error) {
				off := offset
				resp, err := gen.Organizations(cmd.Context(), client, filter, &limit, &off)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.Organizations == nil {
					return nil, 0, nil
				}
				return resp.Organizations.Items, resp.Organizations.Total, nil
			})
			if err != nil {
				return err
			}
			orgs := make([]orgDTO, 0, len(items))
			for _, o := range items {
				if o == nil {
					continue
				}
				orgs = append(orgs, orgDTOFromFields(o.OrgFields))
			}
			return output.Write(f.IOStreams, f.JSON, orgs, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "URN", "NAME")
				for _, o := range orgs {
					t.Row(o.ID, o.URN, o.Name)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&mine, "mine", false, "only organizations you're a member of")
	return cmd
}
