package app

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// appDTO is the stable --json shape for an App.
type appDTO struct {
	ID          string  `json:"id"`
	URN         string  `json:"urn"`
	Name        string  `json:"name"`
	AppType     string  `json:"appType"`
	AgentID     *string `json:"agentId"`
	MemberCount int     `json:"memberCount"`
	CreatedAt   string  `json:"createdAt"`
}

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var org string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Apps in an organization (requires org membership)",
		Example: `  hadron app ls --org acme.com`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Paged { items, total } envelope (hadron-server#473), drained to
			// exhaustion — this command's contract is "all Apps in the org".
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.AppsAppsAppsPageItemsApp, int, error) {
				resp, err := gen.Apps(cmd.Context(), client, org, &limit, &offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.Apps == nil {
					return nil, 0, nil
				}
				return resp.Apps.Items, resp.Apps.Total, nil
			})
			if err != nil {
				return err
			}

			apps := make([]appDTO, 0, len(items))
			for _, a := range items {
				if a == nil {
					continue
				}
				apps = append(apps, appDTO{
					ID:          a.Id,
					URN:         a.Urn,
					Name:        a.Name,
					AppType:     string(a.AppType),
					AgentID:     a.AgentId,
					MemberCount: a.MemberCount,
					CreatedAt:   a.CreatedAt,
				})
			}

			return output.Write(f.IOStreams, f.JSON, apps, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "NAME", "TYPE", "URN")
				for _, a := range apps {
					t.Row(a.ID, a.Name, a.AppType, a.URN)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization ID or URN (required)")
	_ = cmd.MarkFlagRequired("org")
	return cmd
}
