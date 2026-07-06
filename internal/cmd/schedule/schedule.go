package schedule

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdSchedule builds the `schedule` command group.
func NewCmdSchedule(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule <command>",
		Short: "Manage recurring headless-run triggers",
		Long: `Manage recurring headless-run triggers (spec-040, cor:agt:010, D-2026-07-04-E).

A schedule fires an entry node under an App's identity on a 5-field cron
expression. The runs it spawns show up in ` + "`hadron run ls`" + `.

Note: runAsSelf (--as-self) requires an authenticated user — an App-key caller
cannot use it (UNAUTHENTICATED).`,
	}
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdRm(f))
	return cmd
}
