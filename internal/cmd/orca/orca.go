// Package orca is the `hadron orca` group: integrations that read the Orca
// terminal app (cli#1187). Everything under it needs the Orca CLI/app on THIS
// host and says so; the output contracts it emits (terminal-status@1) carry
// nothing Orca-specific, so another terminal environment could ship its own
// command with the same contract.
package orca

import (
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdOrca builds `hadron orca`.
func NewCmdOrca(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orca",
		Short: "Orca integration: read this host's Orca terminals",
		Long: `Orca integration commands. They read terminals of the Orca app, so they work only
on the host that runs Orca and need the Orca CLI (` + "`orca`" + `) on PATH; off that host
they exit 10. They never type into a terminal.`,
	}
	cmd.AddCommand(newCmdTerminal(f))
	return cmd
}

func newCmdTerminal(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "terminal",
		Short: "Inspect Orca terminals (needs Orca on this host)",
	}
	cmd.AddCommand(newCmdStatus(f, defaultEnv(f)))
	return cmd
}
