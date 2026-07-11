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
		Long: `Manage individual action grants (design:grant-model).

A grant hands one org member extra management actions (for example
memory.clone) on top of what their role bundle already allows — the additive
exception, without promoting them. Grants are org-scoped, revocable, may
expire, and die with the membership. Creating and revoking requires org
ADMIN; listing defaults to your own grants (self-audit is never gated).`,
	}
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdRevoke(f))
	return cmd
}
