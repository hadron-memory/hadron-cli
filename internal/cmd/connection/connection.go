// Package connection implements `hadron connection ...` — management of a
// user's external connections (email/calendar) and the scoped grants that
// delegate access on them to Apps (spec-042; hadron-server #593/#599).
//
// Today the surface is the `grant` subgroup: a ConnectionGrant lets a
// connection OWNER hand a specific App install scoped access (mail.read /
// mail.send / calendar.freebusy / calendar.read) to their own connection.
// Create/revoke are owner-only and enforced server-side; this group only
// manages and lists the grant rows.
package connection

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdConnection builds the `connection` command group.
func NewCmdConnection(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "connection <command>",
		Aliases: []string{"connections", "conn"},
		Short:   "Manage external connections and their grants",
		Long: `Work with external connections: an organization's link to a Microsoft 365 or
Gmail mailbox, a Google Calendar or Google Drive, made by signing in at the
provider. Three tasks live here:
  connect  start the sign-in flow for a new connection (org ADMIN)
  flow     check on a flow you started
  grant    let one App use a connection with limited scopes, and revoke it

There is no command here to list or remove connections. 'hadron email
connections --org <org>' lists an organization's connections and their IDs (org
admin); that ID is what grant commands take as --connection.`,
	}
	cmd.AddCommand(newCmdConnect(f))
	cmd.AddCommand(newCmdFlow(f))
	cmd.AddCommand(newCmdGrant(f))
	return cmd
}

// newCmdGrant builds the `connection grant` subgroup.
func newCmdGrant(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "grant <command>",
		Aliases: []string{"grants"},
		Short:   "Delegate scoped access on a connection to an App",
		Long: `Manage connection grants.

A grant lets the owner of a connection (the user it is recorded against, shown
as USER ID by 'hadron email connections') give one App limited access to it:
for example an assistant App that reads the inbox or answers free/busy. Scopes
are mail.read, mail.send, calendar.freebusy and calendar.read. A grant may
expire and can be revoked at any time. Only the owner can create or revoke one;
the server enforces that. Look grants up with 'list' and 'get'.`,
	}
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdRevoke(f))
	return cmd
}
