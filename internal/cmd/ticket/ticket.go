package ticket

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdTicket builds the `ticket` command group.
func NewCmdTicket(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ticket <command>",
		Aliases: []string{"tickets"},
		Short:   "Mint and inspect action tickets",
		Long: `Budget what agent runs may do on their own. A ticket allows one action;
the run that performs the action uses it up.

The one action today is comm.outbound: sending an email or SMS from a run, or
a run calling a webhook. Each send uses one ticket, App-scoped tickets first,
then org-wide ones. With none left, the send is skipped, not retried.

Org ADMINs and OWNERs mint tickets (hadron ticket mint) and read the ledger of
what was minted and used (hadron ticket list); other members cannot read it.
A ticket is different from a grant (hadron grant), which is a standing
permission for a person.`,
		Example: `  hadron ticket mint --org acme.com --count 100 --note "nightly digest"
  hadron ticket list --org acme.com`,
	}
	cmd.AddCommand(newCmdMint(f))
	cmd.AddCommand(newCmdLs(f))
	return cmd
}
