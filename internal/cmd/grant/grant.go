// Package grant implements `hadron grant ...` — individual action grants
// (design:grant-model; hadron-server #615). A grant hands one org member
// extra management actions (e.g. memory.clone) on top of their role bundle,
// without a role change. Management is org-ADMIN and interactive-only; the
// server is the enforcement authority (cor:acl:040:03) — this group only
// manages and lists the grant rows.
package grant

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdGrant builds the `grant` command group.
func NewCmdGrant(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "grant <command>",
		Aliases: []string{"grants"},
		Short:   "Manage individual action grants",
		Long: `Give one org member an extra management action, such as memory.clone,
without raising their role.

A grant belongs to one org and one member. It may expire, can be revoked, and
only applies while the person is a member (removing them suspends it). Org
ADMINs and OWNERs create and revoke grants; anyone can list their own.

Actions are named like memory.clone, memory.create and memory.delete, which
are the actions that currently check for a grant. There is no command that
lists every action name. An action no check uses is accepted but does nothing.

Grants differ from tickets (hadron ticket): a grant is a standing permission
for a person, while a ticket is used up by one action of an agent run.`,
		Example: `  hadron grant create --org acme.com --user jane --action memory.clone
  hadron grant list --org acme.com`,
	}
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdRevoke(f))
	return cmd
}
