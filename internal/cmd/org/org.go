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
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdMember(f))
	cmd.AddCommand(newCmdInvite(f))
	return cmd
}
