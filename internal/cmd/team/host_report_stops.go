package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// newCmdTeamHostReportStops is the dropped-halt reporter (hadron-cli#1629).
func newCmdTeamHostReportStops(f *cmdutil.Factory) *cobra.Command {
	var configPath, reporterWorker string
	var dryRun bool
	var staleAfter time.Duration
	cmd := &cobra.Command{
		Use:   "report-stops --config <host.yaml> (--reporter-worker <name> | --dry-run)",
		Short: "Flag halt notices the host poller could not type to each worker's coordinator",
		Long: `Read the host poller's outbox of halt notices it could not type (held on
a draft, a question, an unknown screen, a changed incarnation or no
terminal) and flag each one with one team-chat post, tagging the halted
worker's reportsTo worker, or the person holding it when it has none. The
halted worker is named without an @, so it is not woken.

The post is authored by the worker session bound in the CURRENT worktree,
which must be a live session of --reporter-worker. With no binding, an ended
session or another worker, nothing is posted, the reports stay pending and
the exit is non-zero. It never posts as you (no --as-me) and never uses the
halted worker's session.

Delivery: at most one post per notice, never silently none. An intent file
is written before the first post; a retry first reads the chat for that
exact post and records it instead of posting again; an identical re-post
inside ten minutes is refused by the server and its earlier seq recorded.
An accepted seq is saved before read-back; later runs only re-read that seq,
never post it again. A read-back mismatch stays failed and pending and says
"needs a person". A later matching read-back can still finish the record.
One run at a time per outbox (a run that finds another running does
nothing). A post the server commits more than three minutes late, after the
next run's check, can still appear twice. This is not exactly-once.

Run it from a timer every few minutes. --dry-run posts nothing and counts
pending reports, and those first held more than --stale-after ago.

Each notice handled prints one line: <result> <event id> <worker> [#seq].
  reported    posted now, at #seq
  reconciled  an earlier attempt's post was found at #seq; not posted again
  duplicate   the server refused an identical re-post; its earlier #seq is recorded
  waiting     an earlier attempt is too recent to check; the next run checks it
  failed      not reported; stays pending (the reason follows)
A run with nothing pending prints "nothing to report".

Exit codes: 0 every pending notice reported, or waiting; 2 usage; 7 no live
binding of --reporter-worker in this worktree, or some report failed (they
stay pending); 1 any other error.`,
		Example: `  hadron team host report-stops --config host.yaml --reporter-worker Hugo
  hadron team host report-stops --config host.yaml --dry-run --stale-after 10m`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(configPath) == "" {
				return exitcode.Newf(exitcode.Usage, "--config is required: the host configuration file")
			}
			if !dryRun && strings.TrimSpace(reporterWorker) == "" {
				return exitcode.Newf(exitcode.Usage, "--reporter-worker is required: the worker whose bound session posts the reports")
			}
			cfg, err := hostpoller.LoadConfig(configPath)
			if err != nil {
				return exitcode.New(exitcode.Usage, err)
			}
			rep := &delivery.StopReporter{
				Outbox:         &delivery.StopOutbox{Dir: delivery.StopOutboxDir(cfg.DeliveryStateDir())},
				ReporterWorker: reporterWorker,
			}
			out := f.IOStreams.Out
			if dryRun {
				pending, stale, err := rep.Pending(staleAfter)
				if err != nil {
					return exitcode.Newf(exitcode.Error, "reading the stop outbox: %v", err)
				}
				if f.JSON {
					return json.NewEncoder(out).Encode(map[string]int{"pending": pending, "stale": stale})
				}
				fmt.Fprintf(out, "pending %d, of which held over %s: %d\n", pending, staleAfter, stale)
				return nil
			}
			rep.API = &stopReporterAPI{f: f, cmd: cmd}
			run, err := rep.Run(cmd.Context())
			if err != nil {
				if errors.Is(err, delivery.ErrNoReporterBinding) {
					return exitcode.Newf(exitcode.Unavailable, "report-stops: %v; nothing posted, reports stay pending", err)
				}
				return exitcode.Newf(exitcode.Error, "report-stops: %v", err)
			}
			failed := 0
			for _, o := range run.Outcomes {
				if o.Result == "failed" {
					failed++
				}
			}
			if f.JSON {
				if err := json.NewEncoder(out).Encode(run); err != nil {
					return exitcode.Newf(exitcode.Error, "report-stops: writing the result: %v", err)
				}
			} else if run.Busy {
				fmt.Fprintln(out, "another report-stops run holds the outbox; nothing done")
			} else if len(run.Outcomes) == 0 {
				fmt.Fprintln(out, "nothing to report")
			} else {
				for _, o := range run.Outcomes {
					line := fmt.Sprintf("%s %s %s", o.Result, o.EventID, o.Worker)
					if o.Seq > 0 {
						line += fmt.Sprintf(" #%d", o.Seq)
					}
					if o.Error != "" {
						line += ": " + o.Error
					}
					fmt.Fprintln(out, line)
				}
			}
			if failed > 0 {
				return exitcode.Newf(exitcode.Unavailable, "report-stops: %d report(s) failed and stay pending", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "the host configuration file (its delivery state holds the outbox)")
	cmd.Flags().StringVar(&reporterWorker, "reporter-worker", "", "the worker whose live session, bound in this worktree, posts the reports")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "post nothing; count pending reports")
	cmd.Flags().DurationVar(&staleAfter, "stale-after", 10*time.Minute, "with --dry-run: count pending reports first held longer ago than this")
	return cmd
}

// stopReporterAPI implements delivery.ReporterAPI over this worktree's
// worker-session binding and the caller's credential.
type stopReporterAPI struct {
	f      *cmdutil.Factory
	cmd    *cobra.Command
	b      *binding
	app    string
	client graphql.Client
}

func (a *stopReporterAPI) BoundWorker(ctx context.Context) (string, string, error) {
	b, _, err := readBinding(ctx)
	if err != nil {
		return "", "", err
	}
	if b == nil || b.SessionID == "" || b.WorkerID == "" {
		return "", "", errors.New("this worktree has no worker session binding")
	}
	if err := checkBindingServer(a.f, b); err != nil {
		return "", "", err
	}
	client, err := a.f.GraphQLClient()
	if err != nil {
		return "", "", err
	}
	id, name, err := reporterSessionIdentity(ctx, client, b)
	if err != nil {
		return "", "", err
	}
	scope, err := resolveTeamAppScope(ctx, a.f, b)
	if err != nil {
		return "", "", err
	}
	a.b, a.app, a.client = b, scope.Ref, client
	return id, name, nil
}

// reporterSessionIdentity is the reporter's identity proof, from the server
// and never from the binding file alone (LOCAL P2 at b38108c9): the bound
// session must exist, be open and be worker-bound, and its worker must be the
// binding's worker. It answers the server's worker id and name, so the
// caller's --reporter-worker check compares against the server's name.
func reporterSessionIdentity(ctx context.Context, client graphql.Client, b *binding) (string, string, error) {
	resp, err := gen.GetTeamSession(ctx, client, b.SessionID)
	if err != nil {
		return "", "", api.MapError(err)
	}
	if resp == nil || resp.Session == nil {
		return "", "", fmt.Errorf("the server does not report the bound session %s", b.SessionID)
	}
	s := resp.Session.TeamSessionFields
	if s.EndedAt != nil {
		return "", "", fmt.Errorf("the bound session %s has ended", b.SessionID)
	}
	if s.WorkerId == nil || *s.WorkerId == "" || s.Worker == nil || s.Worker.Id != *s.WorkerId {
		return "", "", fmt.Errorf("the bound session %s is not a worker session", b.SessionID)
	}
	if *s.WorkerId != b.WorkerID {
		return "", "", fmt.Errorf("the bound session %s belongs to worker %s, not the binding's worker %s", b.SessionID, s.Worker.Name, b.WorkerName)
	}
	return *s.WorkerId, s.Worker.Name, nil
}

func (a *stopReporterAPI) Recipient(ctx context.Context, workerID string) (string, error) {
	resp, err := gen.GetWorker(ctx, a.client, workerID)
	if err != nil {
		return "", api.MapError(err)
	}
	if resp == nil || resp.Worker == nil {
		return "", fmt.Errorf("worker %s not found", workerID)
	}
	w := resp.Worker.WorkerFields
	if w.ReportsTo != nil && w.ReportsTo.Id != "" {
		boss, err := gen.GetWorker(ctx, a.client, w.ReportsTo.Id)
		if err != nil {
			return "", api.MapError(err)
		}
		if boss == nil || boss.Worker == nil || boss.Worker.Slug == "" {
			return "", fmt.Errorf("the reportsTo worker %s not found", w.ReportsTo.Id)
		}
		return boss.Worker.Slug, nil
	}
	if w.HeldByUserId == nil {
		return "", errors.New("the worker has no reportsTo and its holder is not visible")
	}
	u, err := gen.GetUser(ctx, a.client, *w.HeldByUserId)
	if err != nil {
		return "", api.MapError(err)
	}
	if u == nil || u.User == nil || u.User.Handle == nil || *u.User.Handle == "" {
		return "", errors.New("the worker's holder has no handle")
	}
	return *u.User.Handle, nil
}

func (a *stopReporterAPI) Head(ctx context.Context) (int, error) {
	// The ...Attributed reads pass advanceReadState: false: the reporter never
	// moves anyone's read mark.
	resp, err := gen.TeamChatReadHeadAttributed(ctx, a.client, a.app)
	if err != nil {
		return 0, api.MapError(err)
	}
	if resp == nil || resp.TeamChatMessages == nil || len(resp.TeamChatMessages.Items) == 0 {
		return 0, nil
	}
	return resp.TeamChatMessages.Items[0].Seq, nil
}

func (a *stopReporterAPI) Post(ctx context.Context, body string) (int, error) {
	resp, err := gen.CreateTeamChatMessage(ctx, a.client, a.app, body, nil, optStr(a.b.SessionID))
	if err != nil {
		if api.HasErrorCode(err, "TEAM_CHAT_DUPLICATE") {
			if seq, ok := api.ExtensionInt(err, "earlierSeq"); ok && seq > 0 {
				return 0, &delivery.DuplicatePostError{EarlierSeq: seq}
			}
		}
		return 0, api.MapError(err)
	}
	return resp.CreateTeamChatMessage.Seq, nil
}

func (a *stopReporterAPI) MessagesAfter(ctx context.Context, after, limit int) ([]delivery.ChatMessage, error) {
	since := after
	resp, err := gen.TeamChatMessagesAttributed(ctx, a.client, a.app, &since, nil, &limit, nil, nil)
	if err != nil {
		return nil, api.MapError(err)
	}
	out := []delivery.ChatMessage{}
	if resp == nil || resp.TeamChatMessages == nil {
		return out, nil
	}
	for _, m := range resp.TeamChatMessages.Items {
		author := ""
		if m.AuthorWorkerId != nil {
			author = *m.AuthorWorkerId
		}
		out = append(out, delivery.ChatMessage{Seq: m.Seq, AuthorWorkerID: author, Body: m.Body})
	}
	return out, nil
}
