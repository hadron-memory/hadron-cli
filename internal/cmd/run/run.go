package run

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdRun builds the `run` command group.
func NewCmdRun(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <command>",
		Short: "Trigger and inspect headless App runs",
		Long: `Trigger and inspect headless App runs (spec-040, cor:agt:010:02).

A run executes an entry (prompt) node under an App's identity, off any
interactive session — the same kernel a schedule or webhook drives. This group
is the manual trigger plus the audit and control surface: what ran, why, what it
cost, and the kill switch.`,
	}
	cmd.AddCommand(newCmdTrigger(f))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdCancel(f))
	return cmd
}
