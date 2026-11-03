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
		Long: `Sign in to a Hadron server, check who you are signed in as, and manage the
credentials the CLI uses. Everything here applies to the current server: the
default one, or the one --server names.

  Sign in:            hadron auth login   (then: hadron auth status)
  Who am I:           hadron auth whoami
  Sign out:           hadron auth logout
  Automation / CI:    hadron auth token create, then HADRON_TOKEN or
                      hadron auth login --with-token
  Support (admins):   hadron auth impersonate <user> --org <org>, and
                      hadron auth impersonate --stop

Which credential a command uses, in order: the HADRON_TOKEN environment
variable, then a live impersonation session, then the login stored for the
server (OS keychain, or ~/.config/hadron/auth.json without one).`,
	}
	cmd.AddCommand(newCmdLogin(f))
	cmd.AddCommand(newCmdLogout(f))
	cmd.AddCommand(newCmdWhoami(f))
	cmd.AddCommand(newCmdStatus(f))
	cmd.AddCommand(newCmdToken(f))
	cmd.AddCommand(newCmdImpersonate(f))
	return cmd
}
