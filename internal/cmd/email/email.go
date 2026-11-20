// Package email lists an organization's connected accounts and reads a connection's mail (messages and folders). It sends and changes nothing.
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

// NewCmdEmail builds the read-only email group: the connection list and the
// two mailbox reads. Nothing here sends, drafts, moves or deletes mail.
func NewCmdEmail(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "email <command>", Short: "List connected accounts and read a connection's messages and folders", Long: "See which accounts an organization has connected (email, calendar and Drive)\nand whether they are syncing, then read one connection's recent messages and\nmail folders. These commands only read: they never send, reply, draft, move,\nflag or delete mail. Message text (subject, sender, preview, body) comes from\noutside Hadron; it is printed as data and never acted on.\nThe connection ID that \"connections\" prints is what \"messages\", \"folders\",\n\"hadron calendar\" and \"hadron drive\" take as --connection. Reading a mailbox\nneeds the mailbox's owner (or an App with a covering grant); being an org admin\nis enough to list connections, not to read mail.", Example: "  hadron email connections --org hrn:org:acme.com\n  hadron email messages --connection <id> --top 10\n  hadron email folders --connection <id>"}
	c.AddCommand(newConnections(f), newMessages(f), newFolders(f))
	return c
}

func newConnections(f *cmdutil.Factory) *cobra.Command {
	var org string
	var limit, offset int
	c := &cobra.Command{Use: "connections --org <ref>", Short: "List visible connection metadata for an explicit organization", Args: cobra.NoArgs,
		Long: `List the connected accounts of an organization: for each one the ID, who owns
it, its mailbox address, the provider (mail, calendar and Drive connections all
appear here), whether syncing is on, the sync status and when it last synced.
Requires org admin. --org is the organization's ID or hrn:org:<slug> URN; no
organization is picked for you. Pass --limit and/or --offset to read one page
instead of the whole list.

The ID column is the value to pass as --connection to "hadron calendar" and
"hadron drive". Run without --json it prints a table with the columns ID, ORG ID,
USER ID, MAILBOX, PROVIDER, DISPLAY NAME, SYNC ENABLED, SYNC STATUS, LAST SYNC,
CREATED and UPDATED; text values are quoted. An empty result means no
connections were returned to you, not that none exist. This only lists: it does
not read mail, reconnect an account or send anything.

Details:
  Without --limit or --offset it uses the unpaginated connection list in one
  request, so it reports the rows returned with no page total and no completeness
  guarantee. --json is an array of id, organizationId, userId, mailboxEmail,
  provider, displayName, syncEnabled, syncStatus, lastSyncAt, createdAt and
  updatedAt; unset values are null and an empty result is [].
  With --limit and/or --offset it makes one paged request (a flag you leave out
  is omitted, so the server's default applies) and reports the server's total
  and whether more follow. Text ends with the total and, when more follow, the
  --offset that continues; --json is then an object {items, total, hasMore}
  with the same item shape. One call is one page: it never says the list is
  complete, and the CLI does not fetch the next page for you.
  Credentials and lastError are never requested. Errors keep the usual exit
  codes; unknown provider errors do not tell you to reconnect.`,
		Example: `  hadron email connections --org hrn:org:acme.com
  hadron email connections --org hrn:org:acme.com --json
  hadron email connections --org hrn:org:acme.com --limit 20 --offset 20`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			org = strings.TrimSpace(org)
			if org == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty --org id or fully-qualified URN (check for an unset variable)")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset") {
				return connectionsPage(cmd, f, client, org, optInt(cmd, "limit", limit), optInt(cmd, "offset", offset))
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
	c.Flags().IntVar(&limit, "limit", 0, "read one page of at most this many connections (the server bounds it); omitted means its default")
	c.Flags().IntVar(&offset, "offset", 0, "skip this many connections; with --limit or alone it reads one page")
	_ = c.MarkFlagRequired("org")
	return c
}

func nullable(v *string) string {
	if v == nil {
		return "null"
	}
	return strconv.Quote(*v)
}
