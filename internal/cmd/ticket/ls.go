package ticket

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var org string
	cmd := &cobra.Command{
		Use:     "list --org <ref>",
		Aliases: []string{"ls"},
		Short:   "List the org's action-ticket ledger",
		Long: `List the org's action-ticket ledger — minted, consumed-by-which-run, and
expiries (cor:acl:050:04). Pages to exhaustion.`,
		Example: `  hadron ticket ls --org acme.com --json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			items, err := api.CollectAll(func(limit, offset int) ([]*gen.ActionTicketsActionTicketsActionTicketsPageItemsActionTicket, int, error) {
				resp, err := gen.ActionTickets(cmd.Context(), client, org, &limit, &offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.ActionTickets == nil {
					return nil, 0, nil
				}
				return resp.ActionTickets.Items, resp.ActionTickets.Total, nil
			})
			if err != nil {
				return err
			}

			tickets := make([]ticketDTO, 0, len(items))
			for _, t := range items {
				if t == nil {
					continue
				}
				tickets = append(tickets, dtoFromFields(t.ActionTicketFields))
			}

			return output.Write(f.IOStreams, f.JSON, tickets, func(w io.Writer) error {
				table := output.NewTable(w, "ID", "ACTION", "STATUS", "RUN", "CREATED")
				for _, t := range tickets {
					table.Row(t.ID, t.Action, t.status(), output.Dash(t.ConsumedByRunID), t.CreatedAt)
				}
				return table.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization whose ledger to list (ID or URN; required)")
	_ = cmd.MarkFlagRequired("org")
	return cmd
}
