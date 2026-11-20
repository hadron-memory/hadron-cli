package email

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#198 Slice A: the read cut. Each command makes exactly one request,
// sends only the arguments the caller gave (an unset optional is omitted, never
// null) and leaves every rule to the server: --top is parsed as an integer and
// sent, not range-checked (cor:api:240:01), and owner/admin refusals pass through
// unchanged. Message text is external data: the text table neutralises it, the
// body is only ever in --json, and nothing here logs it.

// optInt returns a pointer to the flag's value when the caller set it, else nil
// (so the argument is omitted and the server's default applies).
func optInt(cmd *cobra.Command, name string, v int) *int {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	return &v
}

// connectionArg trims and requires --connection before any request.
func connectionArg(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", exitcode.Newf(exitcode.Usage, "give a nonempty --connection id (the ID that `hadron email connections` prints; check for an unset variable)")
	}
	return v, nil
}

type messageDTO struct {
	ID               string  `json:"id"`
	Subject          *string `json:"subject"`
	From             *string `json:"from"`
	ReceivedDateTime *string `json:"receivedDateTime"`
	BodyPreview      *string `json:"bodyPreview"`
	Body             *string `json:"body"`
	HasAttachments   *bool   `json:"hasAttachments"`
	IsRead           *bool   `json:"isRead"`
}

func nullableBool(v *bool) string {
	if v == nil {
		return "null"
	}
	return strconv.FormatBool(*v)
}

func newMessages(f *cmdutil.Factory) *cobra.Command {
	var connection string
	var top int
	var unread bool
	c := &cobra.Command{Use: "messages --connection <id> [--top N] [--unread]", Short: "Read a connection's recent messages", Args: cobra.NoArgs,
		Long: `Read the most recent messages of one mail connection: for each one the ID,
when it arrived, who sent it, the subject, whether it is read, whether it has
attachments and a short preview. --connection is the ID that "hadron email
connections" prints. This only reads: it does not mark mail read, reply, draft,
move or delete.

Only the mailbox's owner (or an App with a covering grant) may read it; an org
admin who is not the owner is refused, and so is an impersonated session. The
server's refusal is shown as it comes.

One request, one bounded list: --top N asks for that many (the server clamps it
and picks a default when you leave it out; the CLI does not check the number),
--unread asks for unread messages only. Nothing says the list is the whole
mailbox, and there is no next page.

Message text (subject, sender, preview, body) comes from outside Hadron and may
contain anything: it is printed as data, with control and escape sequences made
visible, and is never acted on or logged. The text table shows the preview; the
full body is only in --json.

Details:
  --json is an array of id, subject, from, receivedDateTime, bodyPreview, body,
  hasAttachments and isRead; values the provider leaves out are null and an
  empty result is []. Errors keep the usual exit codes; unknown provider errors
  do not tell you to reconnect.`,
		Example: `  hadron email messages --connection <id>
  hadron email messages --connection <id> --top 10 --unread
  hadron email messages --connection <id> --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id, err := connectionArg(connection)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var unreadOnly *bool
			if unread {
				t := true
				unreadOnly = &t
			}
			r, err := gen.EmailMessages(cmd.Context(), client, id, optInt(cmd, "top", top), unreadOnly)
			if err != nil {
				return api.MapError(err)
			}
			if r == nil || r.ExchangeMessages == nil {
				return exitcode.Newf(exitcode.Unavailable, "server returned no message list")
			}
			rows := []messageDTO{}
			for _, m := range r.ExchangeMessages {
				if m == nil {
					return exitcode.Newf(exitcode.Unavailable, "server returned a null message row")
				}
				rows = append(rows, messageDTO{ID: m.Id, Subject: m.Subject, From: m.From, ReceivedDateTime: m.ReceivedDateTime, BodyPreview: m.BodyPreview, Body: m.Body, HasAttachments: m.HasAttachments, IsRead: m.IsRead})
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if len(rows) == 0 {
					_, e := fmt.Fprintln(w, "No messages returned for this connection.")
					return e
				}
				t := output.NewTable(w, "ID", "RECEIVED", "FROM", "SUBJECT", "READ", "ATTACHMENTS", "PREVIEW")
				for _, m := range rows {
					t.Row(output.SafeCell(strconv.Quote(m.ID)), output.SafeCell(nullable(m.ReceivedDateTime)), output.SafeCell(nullable(m.From)), output.SafeCell(nullable(m.Subject)), nullableBool(m.IsRead), nullableBool(m.HasAttachments), output.SafeCell(nullable(m.BodyPreview)))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				_, e := fmt.Fprintf(w, "%d message(s) returned (one bounded list; not necessarily the whole mailbox).\n", len(rows))
				return e
			})
		}}
	c.Flags().StringVar(&connection, "connection", "", "connection id, as listed by hadron email connections (required)")
	c.Flags().IntVar(&top, "top", 0, "how many messages to ask for; the server clamps it and has its own default")
	c.Flags().BoolVar(&unread, "unread", false, "only unread messages")
	_ = c.MarkFlagRequired("connection")
	return c
}

type folderDTO struct {
	ID              string `json:"id"`
	DisplayName     string `json:"displayName"`
	TotalItemCount  *int   `json:"totalItemCount"`
	UnreadItemCount *int   `json:"unreadItemCount"`
}

func nullableInt(v *int) string {
	if v == nil {
		return "null"
	}
	return strconv.Itoa(*v)
}

func newFolders(f *cmdutil.Factory) *cobra.Command {
	var connection string
	c := &cobra.Command{Use: "folders --connection <id>", Short: "List a connection's mail folders", Args: cobra.NoArgs,
		Long: `List the mail folders of one mail connection: each folder's ID, name, how many
items it holds and how many are unread. --connection is the ID that "hadron
email connections" prints. This only reads.

Only the mailbox's owner (or an App with a covering grant) may read it; an org
admin who is not the owner is refused, and so is an impersonated session. The
server's refusal is shown as it comes. One request; folder names come from
outside Hadron and are printed as data.

Details:
  --json is an array of id, displayName, totalItemCount and unreadItemCount;
  counts the provider leaves out are null and an empty result is []. Errors keep
  the usual exit codes; unknown provider errors do not tell you to reconnect.`,
		Example: `  hadron email folders --connection <id>
  hadron email folders --connection <id> --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id, err := connectionArg(connection)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.EmailFolders(cmd.Context(), client, id)
			if err != nil {
				return api.MapError(err)
			}
			if r == nil || r.ExchangeFolders == nil {
				return exitcode.Newf(exitcode.Unavailable, "server returned no folder list")
			}
			rows := []folderDTO{}
			for _, v := range r.ExchangeFolders {
				if v == nil {
					return exitcode.Newf(exitcode.Unavailable, "server returned a null folder row")
				}
				rows = append(rows, folderDTO{ID: v.Id, DisplayName: v.DisplayName, TotalItemCount: v.TotalItemCount, UnreadItemCount: v.UnreadItemCount})
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if len(rows) == 0 {
					_, e := fmt.Fprintln(w, "No folders returned for this connection.")
					return e
				}
				t := output.NewTable(w, "ID", "NAME", "ITEMS", "UNREAD")
				for _, v := range rows {
					t.Row(output.SafeCell(strconv.Quote(v.ID)), output.SafeCell(strconv.Quote(v.DisplayName)), nullableInt(v.TotalItemCount), nullableInt(v.UnreadItemCount))
				}
				return t.Flush()
			})
		}}
	c.Flags().StringVar(&connection, "connection", "", "connection id, as listed by hadron email connections (required)")
	_ = c.MarkFlagRequired("connection")
	return c
}

type connectionPageDTO struct {
	Items   []connectionDTO `json:"items"`
	Total   int             `json:"total"`
	HasMore bool            `json:"hasMore"`
}

// connectionsPage is the paged form of `email connections`: one request, the
// server's total and hasMore, and the offset that continues — never "complete".
func connectionsPage(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, org string, limit, offset *int) error {
	r, err := gen.EmailConnectionsPage(cmd.Context(), client, org, limit, offset)
	if err != nil {
		return api.MapError(err)
	}
	if r == nil || r.ExchangeConnectionsPage == nil {
		return exitcode.Newf(exitcode.Unavailable, "server returned no connection page")
	}
	p := r.ExchangeConnectionsPage
	dto := connectionPageDTO{Items: []connectionDTO{}, Total: p.Total, HasMore: p.HasMore}
	for _, v := range p.Items {
		if v == nil {
			return exitcode.Newf(exitcode.Unavailable, "server returned a null connection row")
		}
		dto.Items = append(dto.Items, connectionDTO{ID: v.Id, OrganizationID: v.OrganizationId, UserID: v.UserId, MailboxEmail: v.MailboxEmail, Provider: v.Provider, DisplayName: v.DisplayName, SyncEnabled: v.SyncEnabled, SyncStatus: string(v.SyncStatus), LastSyncAt: v.LastSyncAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
	}
	start := 0
	if offset != nil {
		start = *offset
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, e := fmt.Fprintf(w, "organization: %s (from --org; server resolves this reference)\n", strconv.Quote(org)); e != nil {
			return e
		}
		if len(dto.Items) > 0 {
			t := output.NewTable(w, "ID", "ORG ID", "USER ID", "MAILBOX", "PROVIDER", "DISPLAY NAME", "SYNC ENABLED", "SYNC STATUS", "LAST SYNC", "CREATED", "UPDATED")
			for _, v := range dto.Items {
				t.Row(output.SafeCell(strconv.Quote(v.ID)), output.SafeCell(strconv.Quote(v.OrganizationID)), output.SafeCell(strconv.Quote(v.UserID)), output.SafeCell(strconv.Quote(v.MailboxEmail)), output.SafeCell(strconv.Quote(v.Provider)), output.SafeCell(nullable(v.DisplayName)), fmt.Sprint(v.SyncEnabled), output.SafeCell(strconv.Quote(v.SyncStatus)), output.SafeCell(nullable(v.LastSyncAt)), output.SafeCell(strconv.Quote(v.CreatedAt)), output.SafeCell(strconv.Quote(v.UpdatedAt)))
			}
			if e := t.Flush(); e != nil {
				return e
			}
		} else if _, e := fmt.Fprintln(w, "No email connections on this page."); e != nil {
			return e
		}
		line := fmt.Sprintf("showing %d of total %d", len(dto.Items), dto.Total)
		if dto.HasMore {
			line += fmt.Sprintf("; more follow: --offset %d", start+len(dto.Items))
		}
		_, e := fmt.Fprintln(w, line)
		return e
	})
}
