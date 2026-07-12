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
	}
	cmd.AddCommand(newCmdGrant(f))
	return cmd
}

// newCmdGrant builds the `connection grant` subgroup.
func newCmdGrant(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "grant <command>",
		Aliases: []string{"grants"},
		Short:   "Delegate scoped access on a connection to an App",
		Long: `Manage connection grants (spec-042 Track B).

A connection grant lets you — the connection OWNER — delegate scoped access on
your own email/calendar connection to a specific App install, e.g. a headless
assistant that reads your inbox or answers free/busy. Scopes are drawn from
mail.read, mail.send, calendar.freebusy, calendar.read. Grants may expire and
are revocable; creating and revoking is owner-only, enforced server-side.`,
	}
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdRevoke(f))
	return cmd
}
