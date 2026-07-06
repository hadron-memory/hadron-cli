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
		Long: `Mint and inspect action tickets (spec-040, cor:acl:050:04 tier 2).

A ticket is a consumable grant (v1: comm.outbound) an org ADMIN mints into the
org ledger; a headless run consumes one per gated action. The ledger records
which run consumed each ticket, and expiries.`,
	}
	cmd.AddCommand(newCmdMint(f))
	cmd.AddCommand(newCmdLs(f))
	return cmd
}
