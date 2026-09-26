package team

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// `hadron team attention` is the CLI side of hadron-server#1353's team-chat
// router support (server#1362): a router polls one cheap query — "which of my
// workers have relevant unread chat since my last poll?" — and nudges each
// listed worker to read for itself. The worker's own read then marks the
// messages read (see `team chat read` and `team chat mark-read`).
//
// The server feature is an INTERNAL PILOT, gated per operator+App; outside it
// every command here refuses FEATURE_NOT_AVAILABLE (exit 8). The CLI does not
// pre-flight that: GraphQL serverInfo carries no capability list, and the call
// itself is the cheapest honest probe.

// attentionChannelDTO / attentionWorkerDTO / attentionDTO are the stable
// --json shape of `team attention`.
type attentionChannelDTO struct {
	Channel        string `json:"channel"`
	Name           string `json:"name"`
	Unread         int    `json:"unread"`
	UnreadMentions int    `json:"unreadMentions"`
	FirstUnreadSeq *int   `json:"firstUnreadSeq"`
	LastSeq        int    `json:"lastSeq"`
}

type attentionWorkerDTO struct {
	Worker   string                `json:"worker"`
	Name     string                `json:"name"`
	URN      *string               `json:"urn"`
	Live     bool                  `json:"live"`
	Channels []attentionChannelDTO `json:"channels"`
}

type attentionDTO struct {
	App     string               `json:"app"`
	Token   string               `json:"token"`
	Workers []attentionWorkerDTO `json:"workers"`
}

func newCmdAttention(f *cmdutil.Factory) *cobra.Command {
	var since string
	cmd := &cobra.Command{
		Use:   "attention [--since <token>]",
		Short: "Which of your live workers have relevant unread team chat (router poll)",
		Long: `Report which of YOUR live workers in the team App have relevant unread
chat, without reading a single message (hadron-server#1353). This is what a
team-chat router polls: it nudges each listed worker to read the chat, and the
worker's own read marks the messages read.

"Relevant" comes from each worker's register row for the Channel — WATCH or
BOTH, mention-only or everything — and a worker's own posts never count.
Only live workers are listed.

THE TOKEN. Every poll returns an opaque, server-signed token. Pass it back as
--since on the next poll and a worker is listed only for relevant unread
NEWER than it, so one backlog produces one nudge, not one per poll. Omit
--since to list every worker with any relevant unread.

Adopt the returned token only after EVERY listed nudge was accepted. On any
failure, keep polling with the PREVIOUS token: a retry can duplicate a nudge,
but it cannot lose one. The CLI never stores the token for you, for exactly
that reason.

--since now is refused. Discarding an existing backlog is a separate,
explicitly confirmed step: ` + "`hadron team attention switchover`" + `.

An internal pilot: outside it this exits 8 (not enabled for this operator
and App).`,
		Example: `  hadron team attention --json
  hadron team attention --since "$TOKEN" --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Refused, not treated as "no token": an empty --since is almost
			// always an unset shell variable, and silently answering the
			// no-token question would re-nudge every worker's whole backlog.
			if cmd.Flags().Changed("since") && strings.TrimSpace(since) == "" {
				return exitcode.Newf(exitcode.Usage,
					"--since is empty — pass the token the previous poll returned, or omit --since to list every worker with unread")
			}
			ctx := cmd.Context()
			scope, err := attentionScope(ctx, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamAttention(ctx, client, scope.Ref, optStr(strings.TrimSpace(since)))
			if err != nil {
				return api.MapError(err)
			}
			dto := attentionDTO{App: scope.Ref, Token: resp.TeamAttention.Token, Workers: []attentionWorkerDTO{}}
			for _, w := range resp.TeamAttention.Workers {
				wd := attentionWorkerDTO{Worker: w.Worker, Name: w.Name, URN: w.Urn, Live: w.Live, Channels: []attentionChannelDTO{}}
				for _, c := range w.Channels {
					wd.Channels = append(wd.Channels, attentionChannelDTO{
						Channel: c.Channel, Name: c.Name, Unread: c.Unread, UnreadMentions: c.UnreadMentions,
						FirstUnreadSeq: c.FirstUnreadSeq, LastSeq: c.LastSeq,
					})
				}
				dto.Workers = append(dto.Workers, wd)
			}
			// Every write is CHECKED (PR #732, @codex P2): a poll whose token
			// line is lost to a closed pipe must fail, so the caller keeps the
			// previous token rather than believing it holds a new one.
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s (%s)\n", scope.Ref, scope.Source); err != nil {
					return err
				}
				if len(dto.Workers) == 0 {
					if _, err := fmt.Fprintln(w, "No live worker has new relevant unread chat."); err != nil {
						return err
					}
				} else {
					t := output.NewTable(w, "WORKER", "CHANNEL", "UNREAD", "MENTIONS", "FIRST", "LAST")
					for _, wk := range dto.Workers {
						for _, c := range wk.Channels {
							first := "-"
							if c.FirstUnreadSeq != nil {
								first = fmt.Sprintf("#%d", *c.FirstUnreadSeq)
							}
							t.Row(wk.Name, c.Name, fmt.Sprint(c.Unread), fmt.Sprint(c.UnreadMentions), first, fmt.Sprintf("#%d", c.LastSeq))
						}
					}
					if err := t.Flush(); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(w, "token: %s\n", dto.Token); err != nil {
					return err
				}
				_, err := fmt.Fprintln(w, "Pass it as --since only after every nudge was accepted; on any failure keep the previous token.")
				return err
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "the token the previous successful poll returned")
	cmd.AddCommand(newCmdAttentionSwitchover(f))
	return cmd
}

// attentionScope resolves the team App the attention commands act on. When the
// App comes from the worktree BINDING (no --app, no App context), the binding
// must have been made against this server: App ids are not unique across
// deployments (a clone or restore carries them over), so a binding from server
// A would otherwise name an unrelated App on --server B — and `switchover
// apply` would mark ITS backlog read (PR #732, @codex / @copilot). An explicit
// --app still reaches any deployment.
func attentionScope(ctx context.Context, f *cmdutil.Factory) (appScope, error) {
	b, err := readBindingOrNilWithApp(ctx, f)
	if err != nil {
		return appScope{}, err
	}
	if appRef, _ := f.App(); appRef == "" && b != nil {
		if err := checkBindingServer(f, b); err != nil {
			return appScope{}, err
		}
	}
	return resolveTeamAppScope(ctx, f, b)
}

// Switchover DTOs — the stable --json shapes of `switchover preview|apply`.
type switchoverChannelDTO struct {
	Channel        string `json:"channel"`
	Name           string `json:"name"`
	FromSeq        int    `json:"fromSeq"`
	ThroughSeq     int    `json:"throughSeq"`
	Unread         int    `json:"unread"`
	UnreadMentions int    `json:"unreadMentions"`
}

type switchoverWorkerDTO struct {
	Worker   string                 `json:"worker"`
	Name     string                 `json:"name"`
	URN      *string                `json:"urn"`
	Channels []switchoverChannelDTO `json:"channels"`
}

type switchoverPreviewDTO struct {
	App       string                `json:"app"`
	Proof     string                `json:"proof"`
	ExpiresAt string                `json:"expiresAt"`
	Workers   []switchoverWorkerDTO `json:"workers"`
}

type switchoverResultDTO struct {
	App              string `json:"app"`
	Applied          bool   `json:"applied"`
	WorkersAdvanced  int    `json:"workersAdvanced"`
	ChannelsAdvanced int    `json:"channelsAdvanced"`
}

func newCmdAttentionSwitchover(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "switchover <command>",
		Short: "One-time baseline: mark every live worker read through the current heads",
		Long: `Before workers' own reads drove read state, the stored cursors were router
artefacts, so the first attention poll would report most of the chat's history
as unread. The switchover discards that backlog ONCE, in two explicit steps:

  preview   read-only: each live worker's current cursor, the head it would
            move to, and the unread it would clear, plus a short-lived proof
  apply     advances exactly what the preview showed, given its proof —
            atomically, or not at all if any worker, register or cursor has
            changed since (then preview again)

Messages that arrive after the preview stay unread. This is the only
supported way to discard a backlog; ` + "`attention --since now`" + ` is refused.`,
	}
	cmd.AddCommand(newCmdSwitchoverPreview(f))
	cmd.AddCommand(newCmdSwitchoverApply(f))
	return cmd
}

func newCmdSwitchoverPreview(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "preview",
		Short: "Show what a switchover would mark read, and the proof to apply it (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			scope, err := attentionScope(ctx, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamAttentionSwitchoverPreview(ctx, client, scope.Ref)
			if err != nil {
				return api.MapError(err)
			}
			p := resp.TeamAttentionSwitchoverPreview
			dto := switchoverPreviewDTO{App: scope.Ref, Proof: p.Proof, ExpiresAt: p.ExpiresAt, Workers: []switchoverWorkerDTO{}}
			for _, w := range p.Workers {
				wd := switchoverWorkerDTO{Worker: w.Worker, Name: w.Name, URN: w.Urn, Channels: []switchoverChannelDTO{}}
				for _, c := range w.Channels {
					wd.Channels = append(wd.Channels, switchoverChannelDTO{
						Channel: c.Channel, Name: c.Name, FromSeq: c.FromSeq, ThroughSeq: c.ThroughSeq,
						Unread: c.Unread, UnreadMentions: c.UnreadMentions,
					})
				}
				dto.Workers = append(dto.Workers, wd)
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s (%s)\n", scope.Ref, scope.Source); err != nil {
					return err
				}
				if len(dto.Workers) == 0 {
					if _, err := fmt.Fprintln(w, "No live worker to switch over."); err != nil {
						return err
					}
				} else {
					t := output.NewTable(w, "WORKER", "CHANNEL", "FROM", "THROUGH", "UNREAD", "MENTIONS")
					for _, wk := range dto.Workers {
						for _, c := range wk.Channels {
							t.Row(wk.Name, c.Name, fmt.Sprintf("#%d", c.FromSeq), fmt.Sprintf("#%d", c.ThroughSeq),
								fmt.Sprint(c.Unread), fmt.Sprint(c.UnreadMentions))
						}
					}
					if err := t.Flush(); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(w, "Nothing has been written. To apply exactly this, before %s:\n", dto.ExpiresAt); err != nil {
					return err
				}
				_, err := fmt.Fprintf(w, "  hadron team attention switchover apply --proof '%s'\n", dto.Proof)
				return err
			})
		},
	}
}

func newCmdSwitchoverApply(f *cmdutil.Factory) *cobra.Command {
	var proof string
	var yes bool
	cmd := &cobra.Command{
		Use:   "apply --proof <proof> [--yes]",
		Short: "Apply a previewed switchover (marks the previewed backlog read)",
		Long: `Apply the switchover a preview showed, identified by its proof. Every
previewed worker's cursor advances through the previewed head, atomically; if
any worker, register or cursor changed since the preview, nothing is written
and this exits 5 — preview again.

The backlog it marks read is no longer reported by ` + "`team attention`" + `, so this
asks for confirmation on a terminal and needs --yes otherwise.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(proof) == "" {
				return exitcode.Newf(exitcode.Usage,
					"--proof is required — run `hadron team attention switchover preview` and pass the proof it prints")
			}
			ctx := cmd.Context()
			scope, err := attentionScope(ctx, f)
			if err != nil {
				return err
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf(
				"Mark every previewed worker read through the previewed heads in %s (%s)? That backlog will no longer be reported as unread.",
				scope.Ref, scope.Source)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.ConfirmTeamAttentionSwitchover(ctx, client, scope.Ref, strings.TrimSpace(proof))
			if err != nil {
				return api.MapError(err)
			}
			r := resp.ConfirmTeamAttentionSwitchover
			dto := switchoverResultDTO{App: scope.Ref, Applied: r.Applied, WorkersAdvanced: r.WorkersAdvanced, ChannelsAdvanced: r.ChannelsAdvanced}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if !dto.Applied {
					_, err := fmt.Fprintf(w, "Switchover not applied in %s (%s) — nothing was written.\n", scope.Ref, scope.Source)
					return err
				}
				_, err := fmt.Fprintf(w, "Switchover applied in %s (%s): %d worker(s), %d channel cursor(s) advanced.\n",
					scope.Ref, scope.Source, dto.WorkersAdvanced, dto.ChannelsAdvanced)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&proof, "proof", "", "the proof that switchover preview returned (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (required in non-interactive use)")
	return cmd
}
