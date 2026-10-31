// Package slack exposes the read-only Slack connection metadata surface.
package slack

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
	DefaultAppID   string `json:"defaultAppId"`
	Provider       string `json:"provider"`
	Status         string `json:"status"`
	TeamID         string `json:"teamId"`
	TeamName       string `json:"teamName"`
	BotID          string `json:"botId"`
	BotUserID      string `json:"botUserId"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type statusDTO struct {
	Items   []connectionDTO `json:"items"`
	Total   int             `json:"total"`
	Offset  int             `json:"offset"`
	HasMore bool            `json:"hasMore"`
}

func toDTO(id, org, app, provider, status, team, teamName, bot, botUser, created, updated string) (connectionDTO, error) {
	if status != "ACTIVE" && status != "ERROR" {
		return connectionDTO{}, exitcode.Newf(exitcode.Unavailable, "server returned an unknown Slack connection status %s", strconv.Quote(status))
	}
	return connectionDTO{ID: id, OrganizationID: org, DefaultAppID: app, Provider: provider, Status: status, TeamID: team, TeamName: teamName, BotID: bot, BotUserID: botUser, CreatedAt: created, UpdatedAt: updated}, nil
}

// NewCmdSlack builds the read-only slack group; connect/disconnect are a later cut.
func NewCmdSlack(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "slack <command>", Short: "See the Slack workspaces connected to your organizations", Long: "See which Slack workspaces are connected to your organizations and whether each\nconnection is healthy. It only reads; tokens are never shown and nothing here\ncontacts Slack.", Example: "  hadron slack status\n  hadron slack get <connection-id>"}
	c.AddCommand(newStatus(f), newGet(f))
	return c
}

func newStatus(f *cmdutil.Factory) *cobra.Command {
	var org string
	var limit, offset int
	c := &cobra.Command{Use: "status [--org <ref>] [--limit N] [--offset N]", Short: "List Slack workspace connections (one page)", Args: cobra.NoArgs,
		Long: `List the Slack workspace connections you can see, one page at a time: for each
one the ID, organization, default app, status (ACTIVE or ERROR), workspace and
bot ids. Without --org the server returns every organization you are a member
of; --org narrows to one organization (ID or hrn:org:<slug>).

You get one page: --limit (server default page size, at most 200; 0 returns only the total)
and --offset. When more exist, stderr says how to fetch the next page; a short
page is not proof the list is complete unless the total says so. The order is
the server's. An empty result means none were returned to you.

Details:
  --json is {items, total, offset, hasMore}; each item has id, organizationId,
  defaultAppId, provider, status, teamId, teamName, botId, botUserId, createdAt
  and updatedAt. Tokens and the connection's last error are never requested.
  An unrecognized status from the server is refused (exit 7).`,
		Example: "  hadron slack status\n  hadron slack status --org hrn:org:acme.com --limit 20 --offset 20 --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 0 || limit > maxLimit || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit must be 0 to %d and --offset must not be negative", maxLimit)
			}
			var o *string
			if cmd.Flags().Changed("org") {
				v := strings.TrimSpace(org)
				if v == "" {
					return exitcode.Newf(exitcode.Usage, "give a nonempty --org id or fully-qualified URN (check for an unset variable)")
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
			r, err := gen.SlackConnections(cmd.Context(), client, o, l, off)
			if err != nil {
				return api.MapError(err)
			}
			if r == nil || r.SlackConnections == nil {
				return exitcode.Newf(exitcode.Unavailable, "server returned no connection page")
			}
			dto := statusDTO{Items: []connectionDTO{}, Total: r.SlackConnections.Total, Offset: offset}
			for _, v := range r.SlackConnections.Items {
				if v == nil {
					return exitcode.Newf(exitcode.Unavailable, "server returned a null connection row")
				}
				row, e := toDTO(v.Id, v.OrganizationId, v.DefaultAppId, v.Provider, v.Status, v.TeamId, v.TeamName, v.BotId, v.BotUserId, v.CreatedAt, v.UpdatedAt)
				if e != nil {
					return e
				}
				dto.Items = append(dto.Items, row)
			}
			dto.HasMore = dto.Offset+len(dto.Items) < dto.Total
			if dto.HasMore && !f.JSON && len(dto.Items) > 0 {
				defer fmt.Fprintf(f.IOStreams.ErrOut, "showing %d of %d; next page: --offset %d\n", len(dto.Items), dto.Total, dto.Offset+len(dto.Items))
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Items) == 0 {
					_, e := fmt.Fprintf(w, "No Slack connections returned to this caller (total %d).\n", dto.Total)
					return e
				}
				t := output.NewTable(w, "ID", "ORG ID", "APP ID", "STATUS", "TEAM ID", "TEAM", "BOT ID", "BOT USER", "UPDATED")
				for _, v := range dto.Items {
					t.Row(output.SafeCell(strconv.Quote(v.ID)), output.SafeCell(strconv.Quote(v.OrganizationID)), output.SafeCell(strconv.Quote(v.DefaultAppID)), v.Status, output.SafeCell(strconv.Quote(v.TeamID)), output.SafeCell(strconv.Quote(v.TeamName)), output.SafeCell(strconv.Quote(v.BotID)), output.SafeCell(strconv.Quote(v.BotUserID)), output.SafeCell(strconv.Quote(v.UpdatedAt)))
				}
				return t.Flush()
			})
		}}
	c.Flags().StringVar(&org, "org", "", "organization id or fully-qualified URN (default: all your organizations)")
	c.Flags().IntVar(&limit, "limit", 0, "page size, 0 to 200 (server default; 0 returns only the total)")
	c.Flags().IntVar(&offset, "offset", 0, "connections to skip")
	return c
}

func newGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{Use: "get <connection-id>", Short: "Show one Slack workspace connection", Args: cobra.ExactArgs(1),
		Long: `Show one Slack workspace connection by its ID (from "hadron slack status").
Exit 4 when no connection with that ID is visible to you; the server does not
say whether it exists for someone else.

Details:
  --json is one object with id, organizationId, defaultAppId, provider, status
  (ACTIVE or ERROR), teamId, teamName, botId, botUserId, createdAt and
  updatedAt. Tokens and the last error are never requested.`,
		Example: "  hadron slack get 01a1...\n  hadron slack get 01a1... --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty connection id")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.SlackConnection(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			if r == nil || r.SlackConnection == nil {
				return exitcode.Newf(exitcode.NotFound, "no Slack connection %s is visible to you", strconv.Quote(ref))
			}
			v := r.SlackConnection
			dto, err := toDTO(v.Id, v.OrganizationId, v.DefaultAppId, v.Provider, v.Status, v.TeamId, v.TeamName, v.BotId, v.BotUserId, v.CreatedAt, v.UpdatedAt)
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				for _, kv := range [][2]string{{"id", dto.ID}, {"organization", dto.OrganizationID}, {"default app", dto.DefaultAppID}, {"provider", dto.Provider}, {"status", dto.Status}, {"team id", dto.TeamID}, {"team", dto.TeamName}, {"bot id", dto.BotID}, {"bot user", dto.BotUserID}, {"created", dto.CreatedAt}, {"updated", dto.UpdatedAt}} {
					if _, e := fmt.Fprintf(w, "%-13s %s\n", kv[0]+":", output.SafeCell(strconv.Quote(kv[1]))); e != nil {
						return e
					}
				}
				return nil
			})
		}}
}
