package webhook

import (
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var app string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List an App's webhooks (never the secret)",
		Long: `List an App's webhooks. The secret (URL path + token) is never listed — it is
only shown at create and rotate.`,
		Example: `  hadron webhook ls --app acme.com:ops --json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := cmdutil.ResolveAppRef(f, app)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.AgentWebhooksAgentWebhooksAgentWebhooksPageItemsAgentWebhook, int, error) {
				resp, err := gen.AgentWebhooks(cmd.Context(), client, appRef, &limit, &offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.AgentWebhooks == nil {
					return nil, 0, nil
				}
				return resp.AgentWebhooks.Items, resp.AgentWebhooks.Total, nil
			})
			if err != nil {
				return err
			}

			webhooks := make([]webhookDTO, 0, len(items))
			for _, wh := range items {
				if wh == nil {
					continue
				}
				webhooks = append(webhooks, dtoFromFields(wh.AgentWebhookFields))
			}

			return output.Write(f.IOStreams, f.JSON, webhooks, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "NAME", "ENABLED", "ENTRY", "LAST CALLED")
				for _, wh := range webhooks {
					t.Row(wh.ID, wh.Name, strconv.FormatBool(wh.Enabled), wh.EntryNodeURN, output.Dash(wh.LastCalledAt))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "App to list webhooks for (ID or URN; defaults to the App context)")
	return cmd
}
