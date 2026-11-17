// Package email exposes the bounded organization-admin email connection inventory.
package email

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type connectionDTO struct {
	ID             string  `json:"id"`
	OrganizationID string  `json:"organizationId"`
	UserID         string  `json:"userId"`
	MailboxEmail   string  `json:"mailboxEmail"`
	Provider       string  `json:"provider"`
	DisplayName    *string `json:"displayName"`
	SyncEnabled    bool    `json:"syncEnabled"`
	SyncStatus     string  `json:"syncStatus"`
	LastSyncAt     *string `json:"lastSyncAt"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

// NewCmdEmail builds the read-only email group; mailbox operations remain separate.
func NewCmdEmail(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "email <command>", Short: "List an organization's connected email, calendar and Drive accounts", Long: "See which accounts an organization has connected (email, calendar and Drive) and\nwhether they are syncing. It only lists them; mail itself is never read here.\nThe ID it prints is what \"hadron calendar\" and \"hadron drive\" take as --connection.", Example: "  hadron email connections --org hrn:org:acme.com"}
	c.AddCommand(newConnections(f))
	return c
}

func newConnections(f *cmdutil.Factory) *cobra.Command {
	var org string
	c := &cobra.Command{Use: "connections --org <ref>", Short: "List visible connection metadata for an explicit organization", Args: cobra.NoArgs,
		Long: `List the connected accounts of an organization: for each one the ID, who owns
it, its mailbox address, the provider (mail, calendar and Drive connections all
appear here), whether syncing is on, the sync status and when it last synced.
Requires org admin. --org is the organization's ID or hrn:org:<slug> URN; no
organization is picked for you.

The ID column is the value to pass as --connection to "hadron calendar" and
"hadron drive". Run without --json it prints a table with the columns ID, ORG ID,
USER ID, MAILBOX, PROVIDER, DISPLAY NAME, SYNC ENABLED, SYNC STATUS, LAST SYNC,
CREATED and UPDATED; text values are quoted. An empty result means no
connections were returned to you, not that none exist. This only lists: it does
not read mail, reconnect an account or send anything.

Details:
  Uses the existing unpaginated connection list in one request, so it reports
  the rows returned with no page total and no completeness guarantee. --json is
  an array of id, organizationId, userId, mailboxEmail, provider, displayName,
  syncEnabled, syncStatus, lastSyncAt, createdAt and updatedAt; unset values are
  null and an empty result is []. Credentials and lastError are never requested.
  Errors keep the usual exit codes; unknown provider errors do not tell you to
  reconnect.`,
		Example: `  hadron email connections --org hrn:org:acme.com
  hadron email connections --org hrn:org:acme.com --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			org = strings.TrimSpace(org)
			if org == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty --org id or fully-qualified URN (check for an unset variable)")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailConnections(cmd.Context(), client, org)
			if err != nil {
				return api.MapError(err)
			}
			if r == nil || r.ExchangeConnections == nil {
				return exitcode.Newf(exitcode.Unavailable, "server returned no connection list")
			}
			rows := []connectionDTO{}
			for _, v := range r.ExchangeConnections {
				if v == nil {
					return exitcode.Newf(exitcode.Unavailable, "server returned a null connection row")
				}
				rows = append(rows, connectionDTO{ID: v.Id, OrganizationID: v.OrganizationId, UserID: v.UserId, MailboxEmail: v.MailboxEmail, Provider: v.Provider, DisplayName: v.DisplayName, SyncEnabled: v.SyncEnabled, SyncStatus: string(v.SyncStatus), LastSyncAt: v.LastSyncAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if _, e := fmt.Fprintf(w, "organization: %s (from --org; server resolves this reference)\n", strconv.Quote(org)); e != nil {
					return e
				}
				if len(rows) == 0 {
					_, e := fmt.Fprintln(w, "No email connections returned to this caller.")
					return e
				}
				t := output.NewTable(w, "ID", "ORG ID", "USER ID", "MAILBOX", "PROVIDER", "DISPLAY NAME", "SYNC ENABLED", "SYNC STATUS", "LAST SYNC", "CREATED", "UPDATED")
				for _, v := range rows {
					t.Row(output.SafeCell(strconv.Quote(v.ID)), output.SafeCell(strconv.Quote(v.OrganizationID)), output.SafeCell(strconv.Quote(v.UserID)), output.SafeCell(strconv.Quote(v.MailboxEmail)), output.SafeCell(strconv.Quote(v.Provider)), output.SafeCell(nullable(v.DisplayName)), fmt.Sprint(v.SyncEnabled), output.SafeCell(strconv.Quote(v.SyncStatus)), output.SafeCell(nullable(v.LastSyncAt)), output.SafeCell(strconv.Quote(v.CreatedAt)), output.SafeCell(strconv.Quote(v.UpdatedAt)))
				}
				return t.Flush()
			})
		}}
	c.Flags().StringVar(&org, "org", "", "organization id or fully-qualified URN (required)")
	_ = c.MarkFlagRequired("org")
	return c
}

func nullable(v *string) string {
	if v == nil {
		return "null"
	}
	return strconv.Quote(*v)
}
