package team

// `hadron team task find-untracked` (hadron-cli#1524 C3; hadron-server#3125 S3,
// cor:agt:120:03; plan plans:1524-find-untracked). It lists the open items of
// one of the App's task integrations that no task of the App claims, and with
// --assign-to-me claims some as STARTED tasks assigned to the caller. A thin
// client (D1): the list comes from hadron-server, never from `gh` on this host;
// bounds, who may ask and every refusal are the server's (cor:api:240:01). One
// GraphQL call per invocation, never retried.

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// untrackedItemDTO is one outside item (--json). Labels is always an array.
type untrackedItemDTO struct {
	ExternalID              string   `json:"externalId"`
	Repository              string   `json:"repository"`
	Number                  int      `json:"number"`
	Title                   string   `json:"title"`
	URL                     string   `json:"url"`
	Labels                  []string `json:"labels"`
	CreatedAt               string   `json:"createdAt"`
	UpdatedAt               string   `json:"updatedAt"`
	LinkedPullRequestMerged bool     `json:"linkedPullRequestMerged"`
}

// untrackedPageDTO is the list's --json shape.
type untrackedPageDTO struct {
	Items            []untrackedItemDTO `json:"items"`
	Total            int                `json:"total"`
	Complete         bool               `json:"complete"`
	IncompleteReason *string            `json:"incompleteReason"`
}

// claimResultDTO is the --assign-to-me --json shape: the tasks this call
// created, in claim order, and why the walk stopped short, if it did.
type claimResultDTO struct {
	Tasks            []taskDTO `json:"tasks"`
	Complete         bool      `json:"complete"`
	IncompleteReason *string   `json:"incompleteReason"`
	RefusalCode      *string   `json:"refusalCode"`
}

const (
	claimRefused = "claim_refused"
	claimFailed  = "claim_failed"
)

var readyFilters = map[string]gen.ReadyFilter{
	"only": gen.ReadyFilterOnly, "exclude": gen.ReadyFilterExclude, "any": gen.ReadyFilterAny,
}

func newCmdTaskFindUntracked(f *cmdutil.Factory) *cobra.Command {
	var integration, team, ready, repo, assignee, owner string
	var adapterPath, forWorker string
	var skip, limit int
	var assignToMe, noTeam, track bool
	cmd := &cobra.Command{
		Use:   "find-untracked --adapter <exe> [flags]",
		Short: "List outside items (issues) no task claims, or claim some",
		Long: `Find outside items (issues in a tracker) that no task of the App holds, and
optionally claim some as tasks.

The source is the team's intake adapter: --adapter <executable>, or set
$` + adapterEnv + ` (it counts only when no --adapter is given).
The server's task integrations are retired, so --integration is refused
(exit 2, nothing sent), and so are the flags that belonged to it: --team,
--no-team, --ready, --repo, --skip and --assign-to-me.

INTAKE ADAPTER (--adapter <executable> [--for <worker>] [--track] [--assignee
<party>] [--owner <party>] [--limit N]): the team's own local
executable that knows its tracker and holds the tracker credential; Hadron
never calls the tracker. The CLI runs it as ` + "`<executable> <call>`" + ` (describe,
list-untracked, mark-tracked, untrack, report), a JSON request on stdin and one JSON answer on
stdout. Every HADRON_* environment variable is removed before invocation;
configure the tracker credential under its own name. Its output is untrusted:
capped, parsed strictly, printed terminal-safe.
An adapter that speaks another contract version exits 15 before it is asked
for anything, and before the App or Hadron is read. Its describe answer must
list the calls it answers (list-untracked, and mark-tracked for --track), else
exit 2 with nothing claimed. The adapter pages its own list by an opaque cursor, so --skip does
not exist on this path. Each listed item is checked against the App's tasks
(Hadron decides what is tracked); the rest are listed, up to --limit (1-100,
default 20). --for <worker> asks for that worker's items.

--track claims them: --limit (default 1) is how many tasks this call HOLDS. The
create is the claim: if another caller got the item first (TASK_EXTERNAL_ID_TAKEN)
you lost it, it does not count, and the next item is tried. A lost race exits 0.
The winner is told to the adapter (mark-tracked) with the task, the server's
portal link and the worker. The claim goes to --assignee, else --for, else your
bound worker. The server starts a caller-own claim atomically in the create;
the CLI reports the returned state and sends no separate start. Another
assignee's claim, or an older server's PENDING answer, stays PENDING for its
assignee to start. A refused create stops the walk after printing what was
claimed; a failed mark leaves the claim standing and exits 1.

On the adapter path, JSON complete means the list ran out without an error.
Reaching --limit is a successful stop (exit 0), with complete:false and
stoppedReason:"limit_reached"; no extra page is fetched. Both list and track
report stoppedReason: null on exhaustion, "limit_reached" on that successful
limit stop, otherwise the stage that failed — check_failed, walk_failed and
reconcile_failed on both list and track; claim_failed and mark_failed only
under --track.

RECONCILE (every adapter run, before the list). The CLI reads, from the server,
what changed on the App's tasks that carry an outside item since its cursor, and
tells the adapter what its tracker should hear: a reassignment calls mark-tracked
again for the new assignee, a release (` + "`task release`" + `) calls untrack with the
released item, a cancel calls report only if the adapter's describe lists it, and
a finish, hand-back or first assignment call nothing. The cursor is the server's
own opaque value, kept per server, App and adapter in a file beside the CLI's
config (intake-cursors.json); one run reconciles a feed at a time (a second
waits up to 2 minutes, then exits 5); the first run reads from the oldest change, a later
run only what is new, and nothing is told twice. A change the adapter refuses
holds the cursor there so the next run retries it: the list and claim still run,
the run exits 1, and the output's reconcile object names the refusal. An
adapter whose describe does not list a call is simply not told. The summary is a
note on stderr, and the reconcile object in --json.`,
		Example: `  # with your team's intake adapter: list one worker's items, then claim three
  hadron team task find-untracked --adapter ~/bin/hadron-task-adapter-github --for Marco
  hadron team task find-untracked --adapter ~/bin/hadron-task-adapter-github --track --limit 3`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			ctx := cmd.Context()
			if path, ok, err := selectAdapter(cmd, adapterPath, integration, adapterFlagUse{
				team: team, ready: ready, repo: repo, skip: skip, assignToMe: assignToMe, noTeam: noTeam,
				track: track, forWorker: forWorker, assignee: assignee, owner: owner, limit: limit,
			}); err != nil {
				return err
			} else if ok {
				return runAdapterCommand(cmd, f, path, adapterFlagUse{
					track: track, forWorker: forWorker, assignee: assignee, owner: owner, limit: limit,
				})
			}
			// The server's task integrations are retired: it no longer serves
			// the untracked-items read or claim this path sent, so the request
			// would fail with a raw schema error. Refuse the path itself, before
			// any request or binding read, and name what replaced it (cli#1964).
			if cmd.Flags().Changed("integration") {
				return errIntegrationRetired()
			}
			// cli#1622: judged first, with no request and no binding read. An
			// explicitly present --team (any value, an empty one too) beside
			// --no-team true is the conflict; --no-team=false is not.
			if noTeam && cmd.Flags().Changed("team") {
				return exitcode.Newf(exitcode.Usage, "--no-team and --team cannot be combined: --no-team keeps items carrying none of the integration's team labels, --team keeps one team's; nothing was sent")
			}
			if strings.TrimSpace(integration) == "" {
				return exitcode.Newf(exitcode.Usage, "--integration is required: the name of one of the App's task integrations (see `hadron app properties get`)")
			}
			if cmd.Flags().Changed("team") && strings.TrimSpace(team) == "" {
				return exitcode.Newf(exitcode.Usage, "--team needs the team's lead worker (name, URN or id)")
			}
			// cli#1596: the parties belong to a claim only, and are parsed
			// before any request (the binding's App read included).
			partyFlags := cmd.Flags().Changed("assignee") || cmd.Flags().Changed("owner")
			if partyFlags && !assignToMe {
				return exitcode.Newf(exitcode.Usage, "--assignee and --owner name a claim's parties: they need --assign-to-me (a list takes neither); nothing was sent")
			}
			var assigneeArg, ownerArg *gen.AgentTaskPartyInput
			if cmd.Flags().Changed("assignee") {
				if assigneeArg, err = parseTaskPartyFlag("--assignee", assignee); err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("owner") {
				if ownerArg, err = parseTaskPartyFlag("--owner", owner); err != nil {
					return err
				}
			}
			if assignToMe && cmd.Flags().Changed("skip") {
				return exitcode.Newf(exitcode.Usage, "--skip does not combine with --assign-to-me: a claim walks the list from its start")
			}
			var readyArg *gen.ReadyFilter
			if cmd.Flags().Changed("ready") {
				v, ok := readyFilters[ready]
				if !ok {
					return exitcode.Newf(exitcode.Usage, "--ready takes only, exclude or any, got %q", ready)
				}
				readyArg = &v
			}
			intArg := func(name string, v int) *int {
				if !cmd.Flags().Changed(name) {
					return nil
				}
				return &v
			}
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil && !errors.Is(err, errNoWorktree) {
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
			if noTeam {
				return runFindUntrackedNoTeam(ctx, f, client, scope.Ref, integration, repo, readyArg, intArg("skip", skip), intArg("limit", limit), assignToMe, assigneeArg, ownerArg)
			}
			if assignToMe && (assigneeArg != nil || ownerArg != nil) {
				resp, err := gen.ClaimUntrackedTaskItemsForParties(ctx, client, scope.Ref, integration, optStr(team), optStr(repo), readyArg, intArg("limit", limit), assigneeArg, ownerArg)
				if err != nil {
					return api.WithServerCode(mapTaskError(err), err)
				}
				r := resp.ClaimUntrackedTaskItems
				if r == nil {
					return writeClaimResult(f, nil)
				}
				tasks := make([]*gen.AgentTaskFields, 0, len(r.Tasks))
				for _, t := range r.Tasks {
					if t != nil {
						tasks = append(tasks, &t.AgentTaskFields)
					}
				}
				return writeClaimResult(f, &claimOutcome{tasks, r.Complete, r.IncompleteReason, r.RefusalCode})
			}
			if assignToMe {
				resp, err := gen.ClaimUntrackedTaskItems(ctx, client, scope.Ref, integration, optStr(team), optStr(repo), readyArg, intArg("limit", limit))
				if err != nil {
					return api.WithServerCode(mapTaskError(err), err)
				}
				r := resp.ClaimUntrackedTaskItems
				if r == nil {
					return writeClaimResult(f, nil)
				}
				tasks := make([]*gen.AgentTaskFields, 0, len(r.Tasks))
				for _, t := range r.Tasks {
					if t != nil {
						tasks = append(tasks, &t.AgentTaskFields)
					}
				}
				return writeClaimResult(f, &claimOutcome{tasks, r.Complete, r.IncompleteReason, r.RefusalCode})
			}
			resp, err := gen.UntrackedTaskItems(ctx, client, scope.Ref, integration, optStr(team), optStr(repo), readyArg, intArg("skip", skip), intArg("limit", limit))
			if err != nil {
				return api.WithServerCode(mapTaskError(err), err)
			}
			return writeUntrackedPage(f, untrackedPageFrom(resp.UntrackedTaskItems))
		},
	}
	cmd.Flags().StringVar(&adapterPath, "adapter", "", "the team's intake adapter executable (else $"+adapterEnv+")")
	cmd.Flags().BoolVar(&track, "track", false, "with an adapter: claim items as tasks (the create is the claim); --limit is how many to hold")
	cmd.Flags().StringVar(&forWorker, "for", "", "with an adapter: only `worker` X's items, and X holds the claims unless --assignee names another")
	cmd.Flags().StringVar(&integration, "integration", "", "retired: the server's task integrations are gone; refused (exit 2), use --adapter")
	cmd.Flags().StringVar(&team, "team", "", "retired with --integration: refused (exit 2); with an adapter use --for")
	cmd.Flags().BoolVar(&noTeam, "no-team", false, "retired with --integration: refused (exit 2)")
	cmd.Flags().StringVar(&ready, "ready", "", "retired with --integration: refused (exit 2)")
	cmd.Flags().StringVar(&repo, "repo", "", "retired with --integration: refused (exit 2)")
	cmd.Flags().IntVar(&skip, "skip", 0, "retired with --integration: refused (exit 2); an adapter pages by its own cursor")
	cmd.Flags().IntVar(&limit, "limit", 0, "items to list (1-100, default 20), or with --track how many claims to hold (default 1)")
	cmd.Flags().BoolVar(&assignToMe, "assign-to-me", false, "retired with --integration: refused (exit 2); an adapter claims with --track")
	cmd.Flags().StringVar(&assignee, "assignee", "", "with --track: the claim's assignee, worker:<ref> or user:<ref>, sent as written (an App key names both parties)")
	cmd.Flags().StringVar(&owner, "owner", "", "with --track: the claim's owner, worker:<ref> or user:<ref>, sent as written (an App key names both parties)")
	return cmd
}

// untrackedPageFrom adapts the generated list type to the DTO; the no-team
// document (cli#1622) has its own generated type with the same fields.
func untrackedPageFrom(p *gen.UntrackedTaskItemsUntrackedTaskItemsUntrackedTaskItemPage) *untrackedPageDTO {
	if p == nil {
		return nil
	}
	dto := &untrackedPageDTO{Items: []untrackedItemDTO{}, Total: p.Total, Complete: p.Complete, IncompleteReason: p.IncompleteReason}
	for _, it := range p.Items {
		if it == nil {
			continue
		}
		dto.Items = append(dto.Items, untrackedItemDTO{ExternalID: it.ExternalId, Repository: it.Repository, Number: it.Number,
			Title: it.Title, URL: it.Url, Labels: append([]string{}, it.Labels...), CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt,
			LinkedPullRequestMerged: it.LinkedPullRequestMerged})
	}
	return dto
}

func writeUntrackedPage(f *cmdutil.Factory, p *untrackedPageDTO) error {
	if p == nil {
		return exitcode.Newf(exitcode.Error, "the server returned no untracked-items page")
	}
	dto := *p
	if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if len(dto.Items) == 0 {
			_, err := fmt.Fprintf(w, "no untracked items (total %d)\n", dto.Total)
			return err
		}
		t := output.NewTable(w, "REPOSITORY", "NUMBER", "TITLE", "LABELS", "CLOSE CHECK", "URL")
		for _, it := range dto.Items {
			closeCheck := ""
			if it.LinkedPullRequestMerged {
				closeCheck = "merged PR: close check"
			}
			t.Row(output.SafeCell(it.Repository), strconv.Itoa(it.Number), output.SafeCell(it.Title),
				output.SafeCell(strings.Join(it.Labels, ",")), closeCheck, output.SafeCell(it.URL))
		}
		if err := t.Flush(); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "%d of %d shown\n", len(dto.Items), dto.Total)
		return err
	}); err != nil {
		return err
	}
	noteIncomplete(f, dto.Complete, dto.IncompleteReason)
	return nil
}

// claimOutcome is either claim document's result (the plain claim and the
// party claim, cli#1596), so both print and fail the same way.
type claimOutcome struct {
	tasks            []*gen.AgentTaskFields
	complete         bool
	incompleteReason *string
	refusalCode      *string
}

// writeClaimResult prints the claim result FIRST (stdout is the tasks this call
// created, one JSON document under --json), then fails for a stopped walk
// (plan A3): the root renderer puts the error on stderr because stdout is
// already written.
func writeClaimResult(f *cmdutil.Factory, r *claimOutcome) error {
	if r == nil {
		return exitcode.Newf(exitcode.Error, "the server returned no claim result")
	}
	dto := claimResultDTO{Tasks: []taskDTO{}, Complete: r.complete, IncompleteReason: r.incompleteReason, RefusalCode: r.refusalCode}
	// The claim has already been written when its answer is read: name every task
	// it returned, so a refused answer can be verified, not blindly retried.
	claimedRefs := make([]string, 0, len(r.tasks))
	for _, t := range r.tasks {
		claimedRefs = append(claimedRefs, writtenTaskRef(t.Handle, t.Id))
	}
	for _, t := range r.tasks {
		task, err := taskDTOFrom(*t)
		if err != nil {
			return writeAnswerUncertain(err, claimedRefs...)
		}
		dto.Tasks = append(dto.Tasks, task)
	}
	if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if len(dto.Tasks) == 0 {
			_, err := fmt.Fprintln(w, "claimed nothing: no untracked item matched")
			return err
		}
		t := output.NewTable(w, "TASK", "STATUS", "EXTERNAL ID", "TITLE")
		for _, task := range dto.Tasks {
			ext := ""
			if task.ExternalID != nil {
				ext = output.SafeCell(*task.ExternalID)
			}
			t.Row(output.SafeCell(taskRef(task.Handle, task.ID)), output.SafeCell(task.Status), ext, output.SafeCell(task.Title))
		}
		return t.Flush()
	}); err != nil {
		return err
	}
	reason := ""
	if dto.IncompleteReason != nil {
		reason = *dto.IncompleteReason
	}
	claimed := fmt.Sprintf("%d task", len(dto.Tasks))
	if len(dto.Tasks) != 1 {
		claimed += "s"
	}
	switch {
	case reason == claimRefused && dto.RefusalCode != nil && *dto.RefusalCode != "":
		code := *dto.RefusalCode
		return api.WithCode(exitcode.Newf(api.ExitCodeForExtension(code),
			"the claim stopped at a refusal (%s) after %s; those are committed", output.SafeCell(code), claimed), code)
	case reason == claimRefused || reason == claimFailed:
		// No code to map: never reported as success.
		return exitcode.Newf(exitcode.Error,
			"the claim stopped (%s) after %s; those are committed", output.SafeCell(reason), claimed)
	}
	noteIncomplete(f, dto.Complete, dto.IncompleteReason)
	return nil
}

// noteIncomplete is the one-line stderr note for a read the server could not
// finish (V44): not a refusal, so the exit stays 0.
func noteIncomplete(f *cmdutil.Factory, complete bool, reason *string) {
	if complete {
		return
	}
	why := "unknown"
	if reason != nil {
		why = *reason
	}
	_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "note: the server could not read the tracker in full (%s); the result is partial\n", output.SafeCell(why))
}
