// Package policy implements `hadron policy ...` — the standing action-policy
// chain (cor:acl:040:02, hadron-server #510; CLI #1343). A run may perform an
// action only if every layer of the chain allows it: org, the user it runs on
// behalf of, and the App. The server owns every rule; this group explains the
// chain and authors the caller's own user layer. The org layer is set with
// `org update --policy`; the App layer only at createApp (cli#63 tracks it).
package policy

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdPolicy builds the `policy` command group.
func NewCmdPolicy(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy <command>",
		Short: "Explain and set the action-policy chain runs must pass",
		Long: `Every agent run passes a chain of action policies before it may do an action
such as run.execute or comm.outbound. The standing chain has three layers:

  org    set by an org ADMIN with hadron org update --policy
  user   the person a run acts for; each user sets their own with policy set --mine
  app    the App's own link, set when the App is created (the CLI cannot set
         it yet; app create has no --policy)

Each layer is either absent (no restriction) or an allow-list,
{"allow":["run.execute", ...]}. An action is allowed only if every present
layer allows it; {"allow":[]} allows nothing. Schedules and webhooks add their
own link per trigger (--policy on schedule and webhook), which is not part of
the standing chain.

policy explain shows which layer denies an action; policy set --mine writes
your own user layer.`,
		Example: `  hadron policy explain --action comm.outbound --app hrn:app:acme.com:assistant
  hadron policy set --mine '{"allow":["run.execute","comm.outbound"]}'`,
	}
	cmd.AddCommand(newCmdExplain(f))
	cmd.AddCommand(newCmdSet(f))
	return cmd
}
