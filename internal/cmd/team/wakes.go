package team

// `team wakes due|ack` (cli#1279): the person's view of the wakes due for the
// workers they hold, or for the App's lane when they manage it (dueWakes,
// cor:agt:080:08), and the hand acknowledgement of one (acknowledgeWakes,
// cor:agt:080:16). The host poller (`team host run`) does both for the
// workers it types into; these commands are for a wake no poller clears.

import (
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

type dueWake = gen.TeamDueWakesDueWakesDueWakePageWakesDueWake

// Explicit DTOs: schema regeneration must not change this JSON contract.
type dueWakesDTO struct {
	Status      string           `json:"status"`
	Withheld    int              `json:"withheld"`
	Wakes       []dueWakeDTO     `json:"wakes"`
	Dormant     []dormantWakeDTO `json:"dormant"`
	HasNextPage bool             `json:"hasNextPage"`
	EndCursor   *string          `json:"endCursor"`
}

type dueWakeDTO struct {
	WorkerID       string              `json:"workerId"`
	WorkerName     string              `json:"workerName"`
	Proof          string              `json:"proof"`
	TickText       *string             `json:"tickText"`
	Unread         int                 `json:"unread"`
	UnreadMentions int                 `json:"unreadMentions"`
	AnchorAt       string              `json:"anchorAt"`
	DeliverAfter   string              `json:"deliverAfter"`
	BusyDeadline   string              `json:"busyDeadline"`
	Channels       []dueWakeChannelDTO `json:"channels"`
	Reasons        []dueWakeReasonDTO  `json:"reasons"`
}

type dueWakeChannelDTO struct {
	ChannelID        string `json:"channelId"`
	Loc              string `json:"loc"`
	FromSeq          int    `json:"fromSeq"`
	ThroughSeq       int    `json:"throughSeq"`
	LatestMentionSeq *int   `json:"latestMentionSeq"`
}

type dueWakeReasonDTO struct {
	Kind      string            `json:"kind"`
	TickDueAt *string           `json:"tickDueAt"`
	Ticker    *dueWakeTickerDTO `json:"ticker"`
	Task      *dueWakeTaskDTO   `json:"task"`
	Stop      *dueWakeStopDTO   `json:"stop"`
}

type dueWakeTickerDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type dueWakeTaskDTO struct {
	ID         string `json:"id"`
	Handle     string `json:"handle"`
	Title      string `json:"title"`
	Event      string `json:"event"`
	ChainIndex int    `json:"chainIndex"`
	EventID    string `json:"eventId"`
}

type dueWakeStopDTO struct {
	EventID   string `json:"eventId"`
	SignalID  string `json:"signalId"`
	Level     string `json:"level"`
	Notice    string `json:"notice"`
	Interrupt bool   `json:"interrupt"`
}

type dormantWakeDTO struct {
	WorkerID   string `json:"workerId"`
	WorkerName string `json:"workerName"`
	Mentions   int    `json:"mentions"`
}

type ackWakesDTO struct {
	Acknowledged int `json:"acknowledged"`
	Unchanged    int `json:"unchanged"`
}

// wakeRefusalCodes are the typed refusals the two operations return; the
// code leads the message so a caller can tell them apart.
var wakeRefusalCodes = []string{
	"APP_NOT_FOUND", "WORKER_NOT_FOUND", "UNAUTHENTICATED", "FORBIDDEN", "BAD_USER_INPUT",
	"WAKE_ACK_FORBIDDEN", "WAKE_PROOF_REQUESTER_MISMATCH", "WAKE_PROOF_STALE", "WAKE_PROOF_INVALID",
	"APP_KEY_SCOPE_REQUIRED", "APP_KEY_SESSION_BOUND",
}

func wakeFailure(err error) error {
	mapped := api.MapError(err)
	for _, code := range wakeRefusalCodes {
		if api.HasErrorCode(err, code) {
			return exitcode.Newf(exitcode.FromError(mapped), "%s: %s", code, mapped.Error())
		}
	}
	return mapped
}

func newCmdTeamWakes(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{
		Use:   "wakes <command>",
		Short: "List and acknowledge the wakes due to workers",
		Long: `A wake is the platform asking a worker to look: unread mentions, a ticker's
tick, a task event or a stop notice (cor:agt:080:08). The host poller
(` + "`team host run`" + `) reads and acknowledges the wakes of the workers it types
into. These commands are the same two operations by hand: ` + "`due`" + ` lists what is
due and ` + "`ack`" + ` records a delivery.

Both run as you, the person, never as a worker session, even in a bound
worktree: the server lets no worker's session read or acknowledge wakes. You
see the workers you hold, or every worker of the App when you manage it.`,
	}
	c.AddCommand(newCmdTeamWakesDue(f), newCmdTeamWakesAck(f))
	return c
}

func newCmdTeamWakesDue(f *cmdutil.Factory) *cobra.Command {
	var workers []string
	var first int
	var after string
	var acceptStop bool
	cmd := &cobra.Command{
		Use:   "due [--worker <ref>]... [--first N] [--after <cursor>] [--accept-stop]",
		Short: "List the wakes due now",
		Long: `List the wakes due now (cor:agt:080:08): one merged wake per worker, with
every reason (MENTION, TICK with its ticker, TASK with the task's handle and
event, STOP with the notice), one seq range per channel, the unread counts and
the proof that ` + "`team wakes ack`" + ` takes. It shows counts and ranges, never message
text, and listing moves no read position.

--worker <name, URN or id> narrows to that worker; repeat it for several.
Names resolve in the App first, so an unknown worker is refused (exit 4)
rather than read as "nothing due". Without --worker you get every worker you
may read: the ones you hold, or the whole App when you manage it. --first
sets the page size (the server's default without it) and --after continues
from the endCursor of the page before.

A page the App's daily wake budget held back has status WAKE_BUDGET_EXHAUSTED
(cor:agt:080:15). Its wakes stay due, so an empty list then proves nothing:
the page is still printed, and the command exits 5 naming the status.

--accept-stop also asks for stop notices (STOP). Passing it declares that you
serve the lane you read and can deliver those notices (cor:agt:090:04). The
server records the declaration for a manager's read of the whole lane (no
--worker, no --after, one page), and a stop signal can be set only while each
covered worker's lane has a fresh one. Leave it off unless you deliver stop
notices.

Everything here runs as you, the person: a worker session cannot read wakes,
and this command never sends one. --json returns {status, withheld, wakes[],
dormant[], hasNextPage, endCursor}; dormant lists workers whose mentions are
held back, never dropped.`,
		Example: `  hadron team wakes due --worker Iris
  hadron team wakes due --app acme.com:eng-team --json
  hadron team wakes due --first 50 --after <endCursor>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("first") && first < 1 {
				return exitcode.Newf(exitcode.Usage, "--first must be at least 1, got %d", first)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var workerIDs []string
			for _, ref := range workers {
				if strings.TrimSpace(ref) == "" {
					return exitcode.Newf(exitcode.Usage, "--worker is empty (an unset variable?); it takes the worker's name, URN or id")
				}
				// The server answers a ref it cannot resolve with silence (no
				// existence oracle) and resolves no bare name, so the name is
				// resolved here, where an unknown one is a loud not-found.
				w, err := resolveWorker(ctx, client, scope.Ref, ref)
				if err != nil {
					return err
				}
				workerIDs = append(workerIDs, w.Id)
			}
			var afterIn *string
			var firstPtr *int
			if cmd.Flags().Changed("first") {
				firstPtr = &first
			}
			if cmd.Flags().Changed("after") {
				if strings.TrimSpace(after) == "" {
					return exitcode.Newf(exitcode.Usage, "--after is empty; pass the endCursor of the previous page")
				}
				afterIn = &after
			}
			var accepts []gen.WakeKind
			if acceptStop {
				accepts = []gen.WakeKind{gen.WakeKindMention, gen.WakeKindTick, gen.WakeKindTask, gen.WakeKindStop}
			}
			resp, err := gen.TeamDueWakes(ctx, client, scope.Ref, workerIDs, firstPtr, afterIn, accepts)
			if err != nil {
				return wakeFailure(err)
			}
			if resp == nil || resp.DueWakes == nil || resp.DueWakes.PageInfo == nil {
				return exitcode.Newf(exitcode.Error, "server returned no due-wakes page")
			}
			dto := dueWakesFrom(resp.DueWakes)
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return writeDueWakes(w, dto) }); err != nil {
				return err
			}
			if dto.Status != string(gen.DueWakeStatusOk) {
				return exitcode.Newf(exitcode.Conflict, "wakes withheld: %s (%d held back; they stay due, so this page does not show everything due)", dto.Status, dto.Withheld)
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&workers, "worker", nil, "only this worker (name, URN or id); repeatable")
	cmd.Flags().IntVar(&first, "first", 0, "page size (server default without it)")
	cmd.Flags().StringVar(&after, "after", "", "continue from a previous page's endCursor")
	cmd.Flags().BoolVar(&acceptStop, "accept-stop", false, "also ask for stop notices; declares the lane you read (cor:agt:090:04)")
	return cmd
}

func dueWakesFrom(p *gen.TeamDueWakesDueWakesDueWakePage) dueWakesDTO {
	dto := dueWakesDTO{
		Status: string(p.Status), Withheld: p.Withheld,
		Wakes: []dueWakeDTO{}, Dormant: []dormantWakeDTO{},
		HasNextPage: p.PageInfo.HasNextPage, EndCursor: p.PageInfo.EndCursor,
	}
	for _, d := range p.Dormant {
		if d == nil || d.Worker == nil {
			continue
		}
		dto.Dormant = append(dto.Dormant, dormantWakeDTO{WorkerID: d.Worker.Id, WorkerName: d.Worker.Name, Mentions: d.Mentions})
	}
	for _, w := range p.Wakes {
		if w == nil {
			continue
		}
		dto.Wakes = append(dto.Wakes, dueWakeFrom(w))
	}
	return dto
}

func dueWakeFrom(w *dueWake) dueWakeDTO {
	out := dueWakeDTO{
		Proof: w.Proof, TickText: w.TickText, Unread: w.Unread, UnreadMentions: w.UnreadMentions,
		AnchorAt: w.AnchorAt, DeliverAfter: w.DeliverAfter, BusyDeadline: w.BusyDeadline,
		Channels: []dueWakeChannelDTO{}, Reasons: []dueWakeReasonDTO{},
	}
	if w.Worker != nil {
		out.WorkerID, out.WorkerName = w.Worker.Id, w.Worker.Name
	}
	for _, c := range w.Channels {
		if c == nil {
			continue
		}
		ch := dueWakeChannelDTO{FromSeq: c.FromSeq, ThroughSeq: c.ThroughSeq, LatestMentionSeq: c.LatestMentionSeq}
		if c.Channel != nil {
			ch.ChannelID, ch.Loc = c.Channel.Id, c.Channel.Loc
		}
		out.Channels = append(out.Channels, ch)
	}
	for _, r := range w.Reasons {
		if r == nil {
			continue
		}
		reason := dueWakeReasonDTO{Kind: string(r.Kind), TickDueAt: r.TickDueAt}
		if r.Ticker != nil {
			reason.Ticker = &dueWakeTickerDTO{ID: r.Ticker.Id, Name: r.Ticker.Name}
		}
		if r.Task != nil {
			reason.Task = &dueWakeTaskDTO{ID: r.Task.Id, Handle: r.Task.Handle, Title: r.Task.Title, Event: string(r.Task.Event), ChainIndex: r.Task.ChainIndex, EventID: r.Task.EventId}
		}
		if r.Stop != nil {
			reason.Stop = &dueWakeStopDTO{EventID: r.Stop.EventId, SignalID: r.Stop.SignalId, Level: string(r.Stop.Level), Notice: string(r.Stop.Notice), Interrupt: r.Stop.Interrupt}
		}
		out.Reasons = append(out.Reasons, reason)
	}
	return out
}

// reasonLabel is one reason in a line: its kind, then what it is about.
func reasonLabel(r dueWakeReasonDTO) string {
	switch {
	case r.Ticker != nil:
		return r.Kind + " " + output.SafeCell(r.Ticker.Name)
	case r.Task != nil:
		return r.Kind + " " + taskRef(r.Task.Handle, r.Task.ID) + " " + r.Task.Event
	case r.Stop != nil:
		return r.Kind + " " + r.Stop.Notice + " " + r.Stop.Level
	}
	return r.Kind
}

func writeDueWakes(w io.Writer, dto dueWakesDTO) error {
	if dto.Status != string(gen.DueWakeStatusOk) {
		if _, err := fmt.Fprintf(w, "status: %s (%d withheld; they stay due)\n", dto.Status, dto.Withheld); err != nil {
			return err
		}
	}
	if len(dto.Wakes) == 0 {
		// Withheld wakes stay due, so only an OK page may say nothing is due.
		empty := "no wakes due"
		if dto.Status != string(gen.DueWakeStatusOk) {
			empty = "no wakes returned"
		}
		if _, err := fmt.Fprintln(w, empty); err != nil {
			return err
		}
	}
	for _, wk := range dto.Wakes {
		reasons := make([]string, 0, len(wk.Reasons))
		for _, r := range wk.Reasons {
			reasons = append(reasons, reasonLabel(r))
		}
		lines := []string{
			fmt.Sprintf("%s: %s", output.SafeCell(wk.WorkerName), strings.Join(reasons, ", ")),
			fmt.Sprintf("  unread %d, mentions %d; due since %s, deliver after %s", wk.Unread, wk.UnreadMentions, wk.AnchorAt, wk.DeliverAfter),
		}
		for _, c := range wk.Channels {
			lines = append(lines, fmt.Sprintf("  %s %d-%d", output.SafeCell(c.Loc), c.FromSeq, c.ThroughSeq))
		}
		lines = append(lines, "  proof: "+wk.Proof)
		for _, l := range lines {
			if _, err := fmt.Fprintln(w, l); err != nil {
				return err
			}
		}
	}
	for _, d := range dto.Dormant {
		if _, err := fmt.Fprintf(w, "dormant: %s (%d mentions held back)\n", output.SafeCell(d.WorkerName), d.Mentions); err != nil {
			return err
		}
	}
	if dto.HasNextPage && dto.EndCursor != nil {
		if _, err := fmt.Fprintf(w, "more: --after %s\n", *dto.EndCursor); err != nil {
			return err
		}
	}
	return nil
}

func newCmdTeamWakesAck(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "ack <proof>...",
		Short: "Record that wakes were delivered",
		Long: `Record that the wakes behind these proofs were delivered (cor:agt:080:16).
Take each proof unchanged from ` + "`team wakes due`" + `; a proof is bound to whoever
read it and is good for 15 minutes. Acknowledging clears the wake from the next
read. It never moves a read position: the worker's unread chat stays unread.

Prints how many proofs moved the delivery record and how many were already
delivered (a repeat counts nowhere twice). --json returns {acknowledged,
unchanged}.

Refusals keep the server's code: WAKE_PROOF_REQUESTER_MISMATCH (someone else
read it, exit 8), WAKE_PROOF_STALE (read again, exit 5), WAKE_PROOF_INVALID
(not a proof, exit 2), and WAKE_ACK_FORBIDDEN (exit 8) for a worker's session,
which this command never sends: you acknowledge as yourself.`,
		Example: `  hadron team wakes due --worker Iris --json
  hadron team wakes ack <proof>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return exitcode.Newf(exitcode.Usage, "pass at least one proof from team wakes due")
			}
			for _, p := range args {
				if strings.TrimSpace(p) == "" {
					return exitcode.Newf(exitcode.Usage, "an empty proof (an unset variable?); pass the proof from team wakes due")
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AcknowledgeWakes(cmd.Context(), client, args)
			if err != nil {
				return wakeFailure(err)
			}
			if resp == nil || resp.AcknowledgeWakes == nil {
				return exitcode.Newf(exitcode.Error, "server returned no acknowledgement result")
			}
			dto := ackWakesDTO{Acknowledged: resp.AcknowledgeWakes.Acknowledged, Unchanged: resp.AcknowledgeWakes.Unchanged}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ acknowledged %d, unchanged %d\n", dto.Acknowledged, dto.Unchanged)
				return err
			})
		},
	}
}
