// Package telegram exposes the Telegram bot-connection surface: two read-only
// commands (status, get) and two writes (connect, disconnect).
package telegram

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

const maxLimit = 200

type connectionDTO struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	AppID          string `json:"appId"`
	Provider       string `json:"provider"`
	Status         string `json:"status"`
	DisplayName    string `json:"displayName"`
	BotID          string `json:"botId"`
	BotUsername    string `json:"botUsername"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type statusDTO struct {
	Items   []connectionDTO `json:"items"`
	Total   int             `json:"total"`
	Offset  int             `json:"offset"`
	HasMore bool            `json:"hasMore"`
}

func toDTO(id, org, app, provider, status, displayName, bot, botUsername, created, updated string) (connectionDTO, error) {
	if status != "ACTIVE" && status != "ERROR" {
		return connectionDTO{}, exitcode.Newf(exitcode.Unavailable, "server returned an unknown Telegram connection status %s", strconv.Quote(status))
	}
	return connectionDTO{ID: id, OrganizationID: org, AppID: app, Provider: provider, Status: status, DisplayName: displayName, BotID: bot, BotUsername: botUsername, CreatedAt: created, UpdatedAt: updated}, nil
}

// NewCmdTelegram builds the telegram group: read a bot connection, connect a bot, disconnect it.
func NewCmdTelegram(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "telegram <command>", Short: "See, connect and disconnect Telegram bots registered with your Apps", Long: `See which Telegram bots are registered with your Apps, read one
registration's details, register a bot and remove one. status and get
only read; connect and disconnect change the registration (the server
allows that to an organization admin only). The bot token is never shown,
and this CLI never contacts Telegram itself.

A connection belongs to one App (there is no --org here): the bot a person
messages names the App, one active connection per App and one per bot. The
organization comes from the App.

What the status means:
  ACTIVE means the connection is registered. It does not prove it is working:
  a failed bot token is not always recorded here, so a dead connection can
  still show ACTIVE.
  ERROR means Hadron has recorded this connection as failed. The reason is not
  shown here, and this CLI never requests it.

To recover a connection that shows ERROR, an organization admin first removes
it with "hadron telegram disconnect <connection-id>" and then registers the
bot again with "hadron telegram connect": the server refuses to connect a bot
that is still registered, whatever its status.`, Example: "  hadron telegram status\n  hadron telegram get <connection-id>\n  hadron telegram connect --app <ref> --bot-token-file <file>\n  hadron telegram disconnect <connection-id> --yes"}
	c.AddCommand(newStatus(f), newGet(f), newConnect(f), newDisconnect(f))
	return c
}

func newStatus(f *cmdutil.Factory) *cobra.Command {
	var org string
	var limit, offset int
	c := &cobra.Command{Use: "status [--org <ref>] [--limit N] [--offset N]", Short: "List Telegram bot connections (one page, read-only)", Args: cobra.NoArgs,
		Long: `This is the list of Telegram bot connections you can see, one page at a time
(read-only; sign in first, or exit 3): for each one the ID, organization, App,
status (ACTIVE or ERROR), bot display name, username and bot id. ACTIVE means
registered, not proof the connection works; see "hadron telegram --help".
Without --org the server returns every organization you are a member of; --org
narrows to one organization (ID or hrn:org:<slug>) — a member sees that
organization's connections, anyone else an empty page. --app does not apply,
and the saved organization is not used: only --org narrows.

You get one page: --limit (the server's default page size is 20, at most 200;
0 returns only the total) and --offset. When rows are shown and more exist,
stderr says how to fetch the next page; a short page is not proof the list is
complete unless the total says so. The order is the server's (bot username
ascending). An empty result means none were returned to you.

Details:
  The text output is a table, one row per connection (ID, ORG ID, APP ID,
  STATUS, BOT, BOT USERNAME, BOT ID, UPDATED).
  --json is {items, total, offset, hasMore}; each item has id, organizationId,
  appId, provider, status, displayName, botId, botUsername, createdAt and
  updatedAt. The bot token and the connection's last error are never requested.
  Exit 2 for a bad --limit, --offset or empty --org. An unrecognized status
  from the server is refused (exit 7).`,
		Example: "  hadron telegram status\n  hadron telegram status --org hrn:org:acme.com --limit 20 --offset 20 --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 0 || limit > maxLimit || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit must be 0 to %d and --offset must not be negative", maxLimit)
			}
			var o *string
			if cmd.Flags().Changed("org") {
				v := strings.TrimSpace(org)
				if v == "" {
					return exitcode.Newf(exitcode.Usage, "give a nonempty --org id or URN, e.g. hrn:org:acme.com (check for an unset variable)")
				}
				o = &v
			}
			var l, off *int
			if cmd.Flags().Changed("limit") {
				l = &limit
			}
			if cmd.Flags().Changed("offset") {
				off = &offset
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.TelegramConnections(cmd.Context(), client, o, l, off)
			if err != nil {
				return api.MapError(err)
			}
			if r == nil || r.TelegramConnections == nil {
				return exitcode.Newf(exitcode.Unavailable, "server returned no connection page")
			}
			dto := statusDTO{Items: []connectionDTO{}, Total: r.TelegramConnections.Total, Offset: offset}
			for _, v := range r.TelegramConnections.Items {
				if v == nil {
					return exitcode.Newf(exitcode.Unavailable, "server returned a null connection row")
				}
				row, e := toDTO(v.Id, v.OrganizationId, v.AppId, v.Provider, v.Status, v.DisplayName, v.BotId, v.BotUsername, v.CreatedAt, v.UpdatedAt)
				if e != nil {
					return e
				}
				dto.Items = append(dto.Items, row)
			}
			dto.HasMore = dto.Offset+len(dto.Items) < dto.Total
			writeErr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Items) == 0 {
					_, e := fmt.Fprintf(w, "No Telegram connections returned to this caller (total %d).\n", dto.Total)
					return e
				}
				t := output.NewTable(w, "ID", "ORG ID", "APP ID", "STATUS", "BOT", "BOT USERNAME", "BOT ID", "UPDATED")
				for _, v := range dto.Items {
					t.Row(output.SafeCell(strconv.Quote(v.ID)), output.SafeCell(strconv.Quote(v.OrganizationID)), output.SafeCell(strconv.Quote(v.AppID)), output.SafeCell(strconv.Quote(v.Status)), output.SafeCell(strconv.Quote(v.DisplayName)), output.SafeCell(strconv.Quote(v.BotUsername)), output.SafeCell(strconv.Quote(v.BotID)), output.SafeCell(strconv.Quote(v.UpdatedAt)))
				}
				return t.Flush()
			})
			// The hint is cosmetic and only follows a rendered page: a failed
			// write prints nothing extra, and its own failure cannot fail a
			// command whose table already rendered.
			if writeErr == nil && !f.JSON && dto.HasMore && len(dto.Items) > 0 {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "showing %d of %d; next page: --offset %d\n", len(dto.Items), dto.Total, dto.Offset+len(dto.Items))
			}
			return writeErr
		}}
	c.Flags().StringVar(&org, "org", "", "organization id or URN (hrn:org:<slug>; default: all your organizations)")
	c.Flags().IntVar(&limit, "limit", 0, "page size (omit for the server's default 20, at most 200; 0 returns only the total)")
	c.Flags().IntVar(&offset, "offset", 0, "connections to skip")
	return c
}

func newGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{Use: "get <connection-id>", Short: "Show one Telegram bot connection (read-only)", Args: cobra.ExactArgs(1),
		Long: `Show one Telegram bot connection by its ID (from "hadron telegram status").
Read-only; sign in first, or exit 3. ACTIVE means registered, not proof the
connection works; see "hadron telegram --help". Exit 4 when no connection with
that ID is visible to you; the server does not say whether it exists for
someone else.

Details:
  The text output is one line per field (id, organization, app, provider,
  status, bot display name, bot username, bot id, created, updated).
  --json is one object with id, organizationId, appId, provider, status
  (ACTIVE or ERROR), displayName, botId, botUsername, createdAt and updatedAt.
  The bot token and the last error are never requested.`,
		Example: "  hadron telegram get 01a1...\n  hadron telegram get 01a1... --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty connection id")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.TelegramConnection(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			if r == nil || r.TelegramConnection == nil {
				return exitcode.Newf(exitcode.NotFound, "no Telegram connection %s is visible to you", strconv.Quote(ref))
			}
			v := r.TelegramConnection
			dto, err := toDTO(v.Id, v.OrganizationId, v.AppId, v.Provider, v.Status, v.DisplayName, v.BotId, v.BotUsername, v.CreatedAt, v.UpdatedAt)
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return writeConnection(w, dto) })
		}}
}

// writeConnection is one connection as one line per field (get and connect).
func writeConnection(w io.Writer, dto connectionDTO) error {
	for _, kv := range [][2]string{{"id", dto.ID}, {"organization", dto.OrganizationID}, {"app", dto.AppID}, {"provider", dto.Provider}, {"status", dto.Status}, {"bot", dto.DisplayName}, {"bot username", dto.BotUsername}, {"bot id", dto.BotID}, {"created", dto.CreatedAt}, {"updated", dto.UpdatedAt}} {
		if _, e := fmt.Fprintf(w, "%-13s %s\n", kv[0]+":", output.SafeCell(strconv.Quote(kv[1]))); e != nil {
			return e
		}
	}
	return nil
}
