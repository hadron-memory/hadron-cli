package team

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// `hadron team attention` drains the server's bounded #1384 attention pages
// before returning a candidate token. A router nudges listed workers to read
// their own chat; the CLI never stores or adopts that token.

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
Only live workers are listed. The server scans in bounded pages; this command
drains them before printing a report or a token. An incomplete scan is an
error, never a partial result a router could adopt.

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

A configured App context without a matching server-bound worker session has
no server provenance; pass --app explicitly in that case.`,
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
			dto, err := collectAttention(scope.Ref, optStr(since), func(since, page *string) (*gen.TeamAttentionPageTeamAttentionPage, error) {
				resp, err := gen.TeamAttentionPage(ctx, client, scope.Ref, since, page)
				if err != nil {
					return nil, err
				}
				return resp.TeamAttentionPage, nil
			})
			if missingAttentionPageField(err, "teamAttentionPage") {
				legacy, legacyErr := gen.TeamAttention(ctx, client, scope.Ref, optStr(since))
				if legacyErr != nil {
					return api.MapError(legacyErr)
				}
				if legacy == nil || legacy.TeamAttention == nil {
					return exitcode.Newf(exitcode.Error, "teamAttention returned no result")
				}
				dto = legacyAttentionDTO(scope.Ref, legacy.TeamAttention)
				err = nil
			}
			if err != nil {
				return api.MapError(err)
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

// A server before #1384 has no paged field. Only that exact schema refusal
// falls back to the old operation; an auth or token error must remain an error.
func missingAttentionPageField(err error, field string) bool {
	return err != nil && strings.Contains(err.Error(), "Cannot query field \""+field+"\" on type \"Query\"")
}

func legacyAttentionDTO(app string, p *gen.TeamAttentionTeamAttentionTeamAttentionResult) attentionDTO {
	dto := attentionDTO{App: app, Workers: []attentionWorkerDTO{}}
	dto.Token = p.Token
	for _, w := range p.Workers {
		if w == nil {
			continue
		}
		row := attentionWorkerDTO{Worker: w.Worker, Name: w.Name, URN: w.Urn, Live: w.Live, Channels: []attentionChannelDTO{}}
		for _, c := range w.Channels {
			if c != nil {
				row.Channels = append(row.Channels, attentionChannelDTO{
					Channel: c.Channel, Name: c.Name, Unread: c.Unread,
					UnreadMentions: c.UnreadMentions, FirstUnreadSeq: c.FirstUnreadSeq, LastSeq: c.LastSeq,
				})
			}
		}
		dto.Workers = append(dto.Workers, row)
	}
	return dto
}

// collectAttention turns a complete server scan into the stable v1-shaped
// report. No partial page can carry an adoptable token or escape as output.
func collectAttention(app string, since *string, fetch func(since, page *string) (*gen.TeamAttentionPageTeamAttentionPage, error)) (attentionDTO, error) {
	dto := attentionDTO{App: app, Workers: []attentionWorkerDTO{}}
	workerIndex := map[string]int{}
	seenPairs := map[string]bool{}
	seenPages := map[string]bool{}
	var page *string
	for {
		p, err := fetch(since, page)
		if err != nil {
			return attentionDTO{}, err
		}
		if p == nil {
			return attentionDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPage returned no result")
		}
		for _, item := range p.Items {
			if item == nil {
				return attentionDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPage returned a null item")
			}
			pair := item.Worker + "\x00" + item.Channel
			if seenPairs[pair] {
				return attentionDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPage repeated worker/channel %s/%s", item.Worker, item.Channel)
			}
			seenPairs[pair] = true
			i, ok := workerIndex[item.Worker]
			if !ok {
				i = len(dto.Workers)
				workerIndex[item.Worker] = i
				dto.Workers = append(dto.Workers, attentionWorkerDTO{
					Worker: item.Worker, Name: item.WorkerName, URN: item.WorkerUrn,
					Live: true, Channels: []attentionChannelDTO{},
				})
			}
			dto.Workers[i].Channels = append(dto.Workers[i].Channels, attentionChannelDTO{
				Channel: item.Channel, Name: item.ChannelName, Unread: item.Unread,
				UnreadMentions: item.UnreadMentions, FirstUnreadSeq: item.FirstUnreadSeq,
				LastSeq: item.LastSeq,
			})
		}
		if p.ScanComplete {
			if p.NextPage != nil || p.AdoptableSince == nil || *p.AdoptableSince == "" {
				return attentionDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPage completed without exactly one adoptable token")
			}
			dto.Token = *p.AdoptableSince
			return dto, nil
		}
		if p.NextPage == nil || *p.NextPage == "" || p.AdoptableSince != nil {
			return attentionDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPage returned an incomplete scan without a continuation")
		}
		if seenPages[*p.NextPage] {
			return attentionDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPage repeated a continuation")
		}
		seenPages[*p.NextPage] = true
		page = p.NextPage
		since = nil // a continuation carries the original since watermark
	}
}

// attentionScope refuses ambient App scopes that cannot be tied to the current
// server. App IDs can be reused across deployments, while switchover apply
// marks every previewed worker's backlog read. Configured App context has no
// saved server provenance, so only a matching server-bound worker binding can
// attest it; otherwise the caller must select --app explicitly.
func attentionScope(ctx context.Context, f *cmdutil.Factory) (appScope, error) {
	b, err := readBindingOrNilWithApp(ctx, f)
	if err != nil {
		return appScope{}, err
	}
	appRef, err := f.App()
	if err != nil {
		return appScope{}, err
	}
	if f.AppFlag == "" && appRef != "" {
		if b == nil || b.AppID == "" || b.Server == "" || !bindingServerMatches(f, b) {
			return appScope{}, exitcode.Newf(exitcode.Usage,
				"the configured App context has no verified server provenance here — pass --app explicitly to select it on this server, or use a matching server-bound worker session")
		}
		match, err := bindingsAppIdentity(ctx, f, appRef, b.AppID)
		if err != nil {
			return appScope{}, err
		}
		if !match {
			return appScope{}, exitcode.Newf(exitcode.Usage,
				"the configured App context has no verified server provenance here — pass --app explicitly to select it on this server, or use a matching server-bound worker session")
		}
	}
	if appRef == "" && b != nil {
		if b.Server == "" {
			return appScope{}, exitcode.Newf(exitcode.Usage,
				"this worktree's session binding does not record its server — pass --app explicitly or start a new worker session")
		}
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
	FromSeq        *int   `json:"fromSeq,omitempty"`
	FirstUnreadSeq *int   `json:"firstUnreadSeq"`
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

func legacyAttentionPreviewDTO(app string, p *gen.TeamAttentionSwitchoverPreviewTeamAttentionSwitchoverPreview) switchoverPreviewDTO {
	dto := switchoverPreviewDTO{App: app, Workers: []switchoverWorkerDTO{}}
	dto.Proof, dto.ExpiresAt = p.Proof, p.ExpiresAt
	for _, w := range p.Workers {
		if w == nil {
			continue
		}
		row := switchoverWorkerDTO{Worker: w.Worker, Name: w.Name, URN: w.Urn, Channels: []switchoverChannelDTO{}}
		for _, c := range w.Channels {
			if c != nil {
				row.Channels = append(row.Channels, switchoverChannelDTO{
					Channel: c.Channel, Name: c.Name, FromSeq: &c.FromSeq, ThroughSeq: c.ThroughSeq,
					Unread: c.Unread, UnreadMentions: c.UnreadMentions,
				})
			}
		}
		dto.Workers = append(dto.Workers, row)
	}
	return dto
}

// collectAttentionPreview withholds the proof until the complete bounded
// preview has arrived. The server's v2 item exposes first-unread and frozen
// head, not the old exact fromSeq, so the CLI does not invent that value.
func collectAttentionPreview(app string, fetch func(page *string) (*gen.TeamAttentionPreviewPageTeamAttentionPreviewPage, error)) (switchoverPreviewDTO, error) {
	dto := switchoverPreviewDTO{App: app, Workers: []switchoverWorkerDTO{}}
	workerIndex := map[string]int{}
	seenPairs := map[string]bool{}
	seenPages := map[string]bool{}
	var page *string
	for {
		p, err := fetch(page)
		if err != nil {
			return switchoverPreviewDTO{}, err
		}
		if p == nil {
			return switchoverPreviewDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPreviewPage returned no result")
		}
		for _, item := range p.Items {
			if item == nil {
				return switchoverPreviewDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPreviewPage returned a null item")
			}
			pair := item.Worker + "\x00" + item.Channel
			if seenPairs[pair] {
				return switchoverPreviewDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPreviewPage repeated worker/channel %s/%s", item.Worker, item.Channel)
			}
			seenPairs[pair] = true
			i, ok := workerIndex[item.Worker]
			if !ok {
				i = len(dto.Workers)
				workerIndex[item.Worker] = i
				dto.Workers = append(dto.Workers, switchoverWorkerDTO{
					Worker: item.Worker, Name: item.WorkerName, URN: item.WorkerUrn,
					Channels: []switchoverChannelDTO{},
				})
			}
			dto.Workers[i].Channels = append(dto.Workers[i].Channels, switchoverChannelDTO{
				Channel: item.Channel, Name: item.ChannelName, FirstUnreadSeq: item.FirstUnreadSeq,
				ThroughSeq: item.LastSeq, Unread: item.Unread, UnreadMentions: item.UnreadMentions,
			})
		}
		if p.ScanComplete {
			if p.NextPage != nil || p.Proof == nil || *p.Proof == "" || p.ExpiresAt == nil || *p.ExpiresAt == "" {
				return switchoverPreviewDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPreviewPage completed without exactly one proof and expiry")
			}
			dto.Proof, dto.ExpiresAt = *p.Proof, *p.ExpiresAt
			return dto, nil
		}
		if p.NextPage == nil || *p.NextPage == "" || p.Proof != nil {
			return switchoverPreviewDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPreviewPage returned an incomplete scan without a continuation")
		}
		if seenPages[*p.NextPage] {
			return switchoverPreviewDTO{}, exitcode.Newf(exitcode.Error, "teamAttentionPreviewPage repeated a continuation")
		}
		seenPages[*p.NextPage] = true
		page = p.NextPage
	}
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
			p, err := collectAttentionPreview(scope.Ref, func(page *string) (*gen.TeamAttentionPreviewPageTeamAttentionPreviewPage, error) {
				resp, err := gen.TeamAttentionPreviewPage(ctx, client, scope.Ref, page)
				if err != nil {
					return nil, err
				}
				return resp.TeamAttentionPreviewPage, nil
			})
			if missingAttentionPageField(err, "teamAttentionPreviewPage") {
				legacy, legacyErr := gen.TeamAttentionSwitchoverPreview(ctx, client, scope.Ref)
				if legacyErr != nil {
					return api.MapError(legacyErr)
				}
				if legacy == nil || legacy.TeamAttentionSwitchoverPreview == nil {
					return exitcode.Newf(exitcode.Error, "teamAttentionSwitchoverPreview returned no result")
				}
				p = legacyAttentionPreviewDTO(scope.Ref, legacy.TeamAttentionSwitchoverPreview)
				err = nil
			}
			if err != nil {
				return api.MapError(err)
			}
			dto := p
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s (%s)\n", scope.Ref, scope.Source); err != nil {
					return err
				}
				if len(dto.Workers) == 0 {
					if _, err := fmt.Fprintln(w, "No live worker to switch over."); err != nil {
						return err
					}
				} else {
					t := output.NewTable(w, "WORKER", "CHANNEL", "FIRST UNREAD", "THROUGH", "UNREAD", "MENTIONS")
					for _, wk := range dto.Workers {
						for _, c := range wk.Channels {
							first := "-"
							if c.FirstUnreadSeq != nil {
								first = fmt.Sprintf("#%d", *c.FirstUnreadSeq)
							}
							t.Row(wk.Name, c.Name, first, fmt.Sprintf("#%d", c.ThroughSeq),
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
				server, err := f.Server()
				if err != nil {
					return err
				}
				if runtime.GOOS == "windows" {
					_, err = fmt.Fprintf(w, "Apply with --app %q, --server %q, and --proof %q.\n", scope.Ref, server, dto.Proof)
					return err
				}
				_, err = fmt.Fprintf(w, "  hadron team attention switchover apply --app %s --server %s --proof %s\n",
					shellQuote(scope.Ref), shellQuote(server), shellQuote(dto.Proof))
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
			resp, err := gen.ConfirmTeamAttentionSwitchover(ctx, client, scope.Ref, proof)
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
