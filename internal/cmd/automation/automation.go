// Package automation implements `hadron automation ...`: the owner-facing
// approval of an App's schedules and webhooks (cli#1071, hadron-server#2441).
//
// Everything that decides — state, hash, the plain summary — is the server's.
// The CLI binds the wire names, resolves a human name to the trigger the
// server listed, shows what the server said, and sends back exactly what it
// showed.
package automation

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdAutomation builds the `automation` command group.
func NewCmdAutomation(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "automation <command>",
		Aliases: []string{"automations"},
		Short:   "Review and approve an App's automations",
		Long: `Review, approve and decline the automations (schedules and webhooks) of an App
(cor:agt:010:01), without a row id or a hash.

The App is --app, else the active App (` + "`hadron app use`" + `). A trigger is named
` + "`schedule/<name>`" + ` or ` + "`webhook/<name>`" + `; a bare <name> works when exactly one trigger of the
App has it. A manual run's approval is named by the run: --run <run-id>, the id
printed by the run that was refused. Nothing is ever picked for you: no match
and several matches both refuse.

What an automation may do, its state and its hash are all the server's answer;
this CLI infers no permission from being able to list an App, and who may
approve is the server's call. Approve shows the summary first and sends the hash
it showed; if the automation changed meanwhile the server refuses (exit 5) and
nothing is retried.

For raw trigger ids and an explicit digest, use preview and re-adopt instead
of these name-based approval commands. They do not require an App context.`,
	}
	cmd.AddCommand(newCmdRaw(f, false))
	cmd.AddCommand(newCmdRaw(f, true))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdApproval(f))
	cmd.AddCommand(newCmdApprove(f))
	cmd.AddCommand(newCmdDecline(f))
	return cmd
}
