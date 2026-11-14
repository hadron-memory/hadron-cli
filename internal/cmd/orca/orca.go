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
		Short: "Orca integration: inspect and repair this host's Orca terminals",
		Long: `Orca integration commands. Orca is the terminal app this team runs its workers
in. These commands read its terminals, so they work only on the host that runs
Orca and need the Orca CLI (` + "`orca`" + `) on PATH; off that host they exit 10. They
never type into a terminal.

To check that Orca is reachable from this host, run
  hadron orca terminal status no-such-worker
Exit 10 means Orca is not reachable; exit 4 means Orca answered. A usage
error (exit 2 or 3) says nothing about Orca.

Start with ` + "`hadron orca terminal status --help`" + ` for terminal observations.
For a read-only roster inspection, use ` + "`hadron orca host plan --help`" + `.
The planner reads no screens or wakes and never executes its proposed actions.`,
	}
	cmd.AddCommand(newCmdTerminal(f), newCmdHost(f))
	return cmd
}

func newCmdTerminal(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "terminal",
		Short: "Inspect and recover Orca terminals (needs Orca on this host)",
		Long: `Orca is the terminal app this team runs its workers in. These commands act on
its terminals on this host.

  status    classify a worker's terminal as busy, idle, locked or dark, and print
            the terminal-status@1 contract (--json for scripts). Reads only: it
            types nothing into a terminal and acknowledges nothing.
  recover   bring a dark worker back: one new supervised terminal, its
            conversation resumed, verified. Creates a terminal; closes one only
            with --force.
  recreate  replace all of a worker's terminals with one fresh, resumed one.
            Closes terminals.

recover and recreate are the only commands here that change anything. They
never leave two agents in a worktree, never start, take over or end a worker
session, and print their plan with --dry-run. Start with
` + "`hadron orca terminal recover --help`" + `.

Orca must be running on this host with its CLI on PATH; otherwise exit 10.`,
	}
	env := defaultEnv(f)
	cmd.AddCommand(newCmdStatus(f, env), newCmdRecover(f, env), newCmdRecreate(f, env))
	return cmd
}
