// Package auth implements `hadron auth ...`.
package auth

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

func NewCmdAuth(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth <command>",
		Short: "Sign in to Hadron and inspect credentials",
	}
	cmd.AddCommand(newCmdLogin(f))
	cmd.AddCommand(newCmdLogout(f))
	cmd.AddCommand(newCmdWhoami(f))
	cmd.AddCommand(newCmdStatus(f))
	cmd.AddCommand(newCmdToken(f))
	return cmd
}
