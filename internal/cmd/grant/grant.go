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
		Short:   "Manage organization-level grants (retiring; see team grant)",
		Long: `This is the organization-level grant, and it is retiring: App grants
replace it. It gives one org member an extra management action, such as
memory.clone, without raising their role. It cannot grant anything to a worker
or inside an App. For an App grant, whose holder can be a person, an agent, a
worker or an App, use hadron team grant (a worker or agent asks with hadron
team grant request).

A grant belongs to one org and one member. It may expire, can be revoked, and
only applies while the person is a member (removing them suspends it). Org
ADMINs and OWNERs create and revoke grants; anyone can list their own.

Actions are named like memory.clone, memory.create and memory.delete, which
are the actions that currently check for a grant. To read the grantable
actions an App has registered, run hadron team grant actions. No command lists
the action names the policy chain takes (hadron policy explain and set); an
action no check uses is accepted but does nothing.

Three listings answer "what can I do?": hadron grant list (org action grants),
hadron team grant list (a worker's grants) and hadron auth grants (grants held
by you personally, across Apps).

Grants differ from tickets (hadron ticket): a grant is a standing permission
for a person, while a ticket is used up by one action of an agent run.`,
		Example: `  hadron grant create --org acme.com --user jane --action memory.clone
  hadron grant list --org acme.com`,
	}
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdRevoke(f))
	return cmd
}
