package org

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdOrg builds the `hadron org` command group.
func NewCmdOrg(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "org <command>",
		Aliases: []string{"orgs", "organization"},
		Short:   "Work with organizations and their members",
		Long: `Create organizations, manage who belongs to them, and choose the one you
work in.

Typical flow:
  hadron org create --name Acme --urn acme.com     you become its OWNER
  hadron org invite create ...  or  hadron org member add ...
  hadron org use acme.com                          make it your active org

Commands that act on one org take it as an argument or --org (an id, URN or
root such as acme.com); only searches under --scope global use the active org.
Org roles, highest first: OWNER, ADMIN, CONTRIBUTOR, READER. Managing members
and invitations needs ADMIN or OWNER.`,
		Example: `  hadron org list --mine
  hadron org member list acme.com`,
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdPublic(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdMember(f))
	cmd.AddCommand(newCmdInvite(f))
	cmd.AddCommand(newCmdSetActive(f))
	return cmd
}
