package team

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// markReadDTO is the stable --json shape of `team chat mark-read`.
type markReadDTO struct {
	WorkerID    string `json:"workerId"`
	ChannelID   string `json:"channelId"`
	Through     int    `json:"through"`
	LastSeenSeq int    `json:"lastSeenSeq"`
}

// newCmdTeamChatMarkRead is the CLI twin of hadron_team_chat_mark_read
// (hadron-server#1353): it calls markOwnTeamChatRead, which shares the MCP
// tool's server helper and gate — pilot-gated per operator+App, pinned to one
// caller-owned live worker session. Deliberately NOT advanceChannelReadState,
// the wider legacy door that is neither (Ada/Dara, team chat #1885/#1889).
func newCmdTeamChatMarkRead(f *cmdutil.Factory) *cobra.Command {
	var through int
	var channel string
	cmd := &cobra.Command{
		Use:   "mark-read --through <seq> [--channel <ref>]",
		Short: "Mark the bound worker's team chat read through a seq",
		Long: `Advance the bound worker's OWN read cursor on one Channel, through --through
(hadron-server#1353). Use it after a read that does not count by itself: a
--mentions/--mentions-me, --before or windowed read. An ordinary forward
` + "`team chat read`" + ` already marks what it returned.

The cursor only moves forward: a lower seq changes nothing (the result shows
where it already is), and a seq beyond the Channel's latest message is refused
(exit 2). --channel defaults to the App's team chat.

Needs a worker session binding (` + "`hadron team session start`" + `): the cursor belongs to
the bound worker, and the server accepts only your own live session. An
internal pilot: outside it this exits 8 (not enabled for this operator and
App).`,
		Example: `  hadron team chat mark-read --through 1878`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("through") {
				return exitcode.Newf(exitcode.Usage, "--through <seq> is required — the seq to mark read through")
			}
			if through < 0 {
				return exitcode.Newf(exitcode.Usage, "--through must not be negative")
			}
			// An EMPTY --channel is not an absent one: it is nearly always an
			// unset variable, and the absent path would silently mark the
			// App's team chat read instead of the Channel the caller named.
			if cmd.Flags().Changed("channel") && strings.TrimSpace(channel) == "" {
				return exitcode.Newf(exitcode.Usage,
					"--channel is empty — pass a Channel id or address, or omit --channel for the App's team chat")
			}
			ctx := cmd.Context()
			// A corrupt or unreadable binding keeps its own message (it says
			// how to fix it); only a MISSING one — or no worktree to hold one —
			// is "bind one first".
			b, _, err := readBinding(ctx)
			if err != nil && !errors.Is(err, errNoWorktree) {
				return err
			}
			if b == nil || b.SessionID == "" {
				return exitcode.Newf(exitcode.Usage,
					"mark-read advances the BOUND worker's cursor — bind one first with `hadron team session start --as <worker>`")
			}
			if err := checkBindingServer(f, b); err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			channelRef := strings.TrimSpace(channel)
			if channelRef == "" {
				resp, err := gen.TeamDefaultChannel(ctx, client, scope.Ref)
				if err != nil {
					return api.MapError(err)
				}
				if resp.App == nil {
					return exitcode.Newf(exitcode.NotFound, "App %q not found, or not visible to you (cor:api:140:03)", scope.Ref)
				}
				if resp.App.DefaultChannel == nil {
					return exitcode.Newf(exitcode.NotFound, "the App %s has no team chat Channel yet — nothing to mark read", scope.Ref)
				}
				channelRef = resp.App.DefaultChannel.Id
			}
			// Re-checked HERE, not only at the start: the Channel lookup above
			// is a round trip, and a rebind in between would otherwise mark a
			// retired session's cursor. Nothing is sent; exit 5, re-run.
			if !bindingIsSession(ctx, b.SessionID) {
				return exitcode.Newf(exitcode.Conflict,
					"this worktree's session binding changed while the command ran — nothing was marked; re-run it")
			}
			// The same session also rides as the header; the argument is what
			// the server pins.
			resp, err := gen.MarkOwnTeamChatRead(api.WithSession(ctx, b.SessionID), client, scope.Ref, b.SessionID, channelRef, through)
			if err != nil {
				return api.MapError(err)
			}
			r := resp.MarkOwnTeamChatRead
			dto := markReadDTO{WorkerID: r.WorkerId, ChannelID: r.ChannelId, Through: through, LastSeenSeq: r.LastSeenSeq}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				name := b.WorkerName
				if name == "" {
					name = dto.WorkerID
				}
				where := fmt.Sprintf("channel %s in %s (%s)", dto.ChannelID, scope.Ref, scope.Source)
				// Checked (PR #732, @copilot): the mark has already happened, so
				// a receipt that cannot be delivered must not exit 0 as if the
				// caller had been told.
				if dto.LastSeenSeq > through {
					_, err := fmt.Fprintf(w, "%s had already read through #%d on %s — nothing changed.\n", name, dto.LastSeenSeq, where)
					return err
				}
				_, err := fmt.Fprintf(w, "Marked read through #%d for %s on %s.\n", dto.LastSeenSeq, name, where)
				return err
			})
		},
	}
	cmd.Flags().IntVar(&through, "through", 0, "mark read through this seq (required)")
	cmd.Flags().StringVar(&channel, "channel", "", "Channel id or address (default: the App's team chat)")
	return cmd
}

// markDeliveredRead is `team chat read`'s server-side half (#1353): after a
// read has been delivered, advance the bound worker's own cursor on the App's
// team chat through `through`. Best-effort — it never fails a read that
// succeeded. Outside the pilot the server refuses FEATURE_NOT_AVAILABLE,
// which is the ordinary case and silent; any other failure is a stderr note,
// because it means a team-chat router may nudge about these messages again.
func markDeliveredRead(ctx context.Context, f *cmdutil.Factory, client graphql.Client, appRef string, b *binding, through int) {
	note := func(err error) {
		fmt.Fprintf(f.IOStreams.ErrOut,
			"note: this read was not recorded on the server (%v) — a team-chat router may nudge about these messages again; `hadron team chat mark-read --through %d` retries it\n",
			api.MapError(err), through)
	}
	resp, err := gen.TeamDefaultChannel(ctx, client, appRef)
	if err != nil {
		note(err)
		return
	}
	if resp.App == nil || resp.App.DefaultChannel == nil {
		return // no team Channel: there is no server cursor to move
	}
	if !bindingIsSession(ctx, b.SessionID) {
		return // rebound or ended during the lookup: not ours to mark
	}
	_, err = gen.MarkOwnTeamChatRead(api.WithSession(ctx, b.SessionID), client, appRef, b.SessionID, resp.App.DefaultChannel.Id, through)
	if err != nil && !api.HasErrorCode(err, "FEATURE_NOT_AVAILABLE") {
		note(err)
	}
}
