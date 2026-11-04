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
		Long: `Orca integration commands. Orca is the terminal app this team runs its workers
in. These commands read its terminals, so they work only on the host that runs
Orca and need the Orca CLI (` + "`orca`" + `) on PATH; off that host they exit 10. They
never type into a terminal.

To check that Orca is reachable from this host, run
  hadron orca terminal status no-such-worker
Exit 10 means Orca is not reachable; exit 4 means Orca answered.

Start with ` + "`hadron orca terminal status --help`" + `: it is the only command in the group.`,
	}
	cmd.AddCommand(newCmdTerminal(f))
	return cmd
}

func newCmdTerminal(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "terminal",
		Short: "Inspect Orca terminals (needs Orca on this host)",
		Long: `Orca is the terminal app this team runs its workers in. These commands inspect
its terminals on this host. This group reads only: it types nothing into a
terminal and acknowledges nothing.

  status   classify a worker's terminal as busy, idle, locked or dark, and print
           the terminal-status@1 contract (--json for scripts)

Orca must be running on this host with its CLI on PATH; otherwise exit 10.`,
	}
	cmd.AddCommand(newCmdStatus(f, defaultEnv(f)))
	return cmd
}
