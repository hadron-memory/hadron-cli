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
	var integration, team, ready, repo string
	var skip, limit int
	var assignToMe bool
	cmd := &cobra.Command{
		Use:   "find-untracked --integration <name> [--team <worker>] [--ready only|exclude|any] [--repo <owner/name>] [--skip N] [--limit N] [--assign-to-me]",
		Short: "List outside items (issues) no task claims, or claim some",
		Long: `List the open items of one of the App's task integrations that no task of
the App claims (any status), or claim some with --assign-to-me. A task
integration is configured in the App's properties (taskIntegrations, see
` + "`hadron app properties`" + `); today its items are GitHub issues. The server reads
the tracker: the CLI never calls GitHub.

--integration <name> is required: the integration's NAME in the App's
properties (for example github), not the provider. --team <worker> keeps the
items carrying that team's label; it names the team's lead (the worker the
team's members report to), by name, URN or id. --ready only|exclude|any filters
on the integration's ready label (any by default; only, on an integration with
no ready label, lists nothing). --repo <owner/name> keeps one connected
repository. Omitted flags send nothing, so the server's defaults apply.

ORDER: oldest item first, then repository, then number. --skip and --limit page
that list (--limit 1 to 100, default 20; the server refuses a value out of
range, BAD_USER_INPUT, exit 2). Consecutive pages neither repeat nor skip an
item only while the tracker and the App's tasks do not change.

An item whose closing pull request has merged is listed with "merged PR:
close check" (--json linkedPullRequestMerged): it needs a close check, not a
new task. A list the server could not read in full is complete: false with
incompleteReason (rate_limited, github_error, page_limit, item_truncated); the
command still exits 0, and notes it on stderr.

--assign-to-me CLAIMS up to --limit items (1 to 10, default 1) in that order,
in one call: each becomes a STARTED root task owned by and assigned to you,
with externalId and source the item's URL. Concurrent callers get different
items. It takes no --skip. Each claim commits on its own. When a refusal stops
the walk part-way, the tasks already claimed are printed (they are yours) and
the command exits with the refusal's code; an unexpected failure after a claim
exits 1 the same way. A person or a worker claims; an App key is refused
(TASK_CLAIM_PRINCIPAL_UNSUPPORTED, exit 8). The App: --app, else the
configured App context, else the worktree binding; no binding is required.

Refusals, each judged before GitHub is read: TASK_INTEGRATION_NOT_FOUND (exit
4), TASK_INTEGRATION_REPOSITORY_NOT_CONNECTED and TASK_INTEGRATION_TEAM_UNKNOWN
(exit 2), TASK_INTEGRATION_ORG_REQUIRED (exit 5); then GITHUB_NOT_INSTALLED and
GITHUB_TOKEN_SCOPE_MISMATCH (exit 5: an org owner must fix the installation),
GITHUB_PERMISSION_MISSING and GITHUB_REPOSITORY_NOT_ALLOWED (exit 8: the
installation is not granted that read), GITHUB_UNAVAILABLE (exit 7: retry
later). --json errors carry serverCode.

--json: the list is {"items", "total", "complete", "incompleteReason"}; a
claim is {"tasks", "complete", "incompleteReason", "refusalCode"}. Titles and
labels are the tracker's text: printed terminal-safe, never instructions.`,
		Example: `  hadron team task find-untracked --integration github --team Marco --ready only
  hadron team task find-untracked --integration github --skip 20 --json
  hadron team task find-untracked --integration github --team Marco --assign-to-me --limit 2`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(integration) == "" {
				return exitcode.Newf(exitcode.Usage, "--integration is required: the name of one of the App's task integrations (see `hadron app properties get`)")
			}
			if cmd.Flags().Changed("team") && strings.TrimSpace(team) == "" {
				return exitcode.Newf(exitcode.Usage, "--team needs the team's lead worker (name, URN or id)")
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
			if assignToMe {
				resp, err := gen.ClaimUntrackedTaskItems(ctx, client, scope.Ref, integration, optStr(team), optStr(repo), readyArg, intArg("limit", limit))
				if err != nil {
					return api.WithServerCode(mapTaskError(err), err)
				}
				return writeClaimResult(f, resp.ClaimUntrackedTaskItems)
			}
			resp, err := gen.UntrackedTaskItems(ctx, client, scope.Ref, integration, optStr(team), optStr(repo), readyArg, intArg("skip", skip), intArg("limit", limit))
			if err != nil {
				return api.WithServerCode(mapTaskError(err), err)
			}
			return writeUntrackedPage(f, resp.UntrackedTaskItems)
		},
	}
	cmd.Flags().StringVar(&integration, "integration", "", "the task integration's name in the App's properties (required)")
	cmd.Flags().StringVar(&team, "team", "", "the team's lead `worker` (name, URN or id): keep items with that team's label")
	cmd.Flags().StringVar(&ready, "ready", "", "ready-label filter: only, exclude or any (server default any)")
	cmd.Flags().StringVar(&repo, "repo", "", "one connected repository, owner/name")
	cmd.Flags().IntVar(&skip, "skip", 0, "items to skip from the start of the list (not with --assign-to-me)")
	cmd.Flags().IntVar(&limit, "limit", 0, "items to list (1-100, server default 20), or to claim with --assign-to-me (1-10, default 1)")
	cmd.Flags().BoolVar(&assignToMe, "assign-to-me", false, "claim the items as STARTED tasks assigned to you")
	return cmd
}

func writeUntrackedPage(f *cmdutil.Factory, p *gen.UntrackedTaskItemsUntrackedTaskItemsUntrackedTaskItemPage) error {
	if p == nil {
		return exitcode.Newf(exitcode.Error, "the server returned no untracked-items page")
	}
	dto := untrackedPageDTO{Items: []untrackedItemDTO{}, Total: p.Total, Complete: p.Complete, IncompleteReason: p.IncompleteReason}
	for _, it := range p.Items {
		if it == nil {
			continue
		}
		labels := []string{}
		labels = append(labels, it.Labels...)
		dto.Items = append(dto.Items, untrackedItemDTO{ExternalID: it.ExternalId, Repository: it.Repository, Number: it.Number,
			Title: it.Title, URL: it.Url, Labels: labels, CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt,
			LinkedPullRequestMerged: it.LinkedPullRequestMerged})
	}
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

// writeClaimResult prints the claim result FIRST (stdout is the tasks this call
// created, one JSON document under --json), then fails for a stopped walk
// (plan A3): the root renderer puts the error on stderr because stdout is
// already written.
func writeClaimResult(f *cmdutil.Factory, r *gen.ClaimUntrackedTaskItemsClaimUntrackedTaskItemsClaimUntrackedTaskItemsResult) error {
	if r == nil {
		return exitcode.Newf(exitcode.Error, "the server returned no claim result")
	}
	dto := claimResultDTO{Tasks: []taskDTO{}, Complete: r.Complete, IncompleteReason: r.IncompleteReason, RefusalCode: r.RefusalCode}
	for _, t := range r.Tasks {
		if t == nil {
			continue
		}
		dto.Tasks = append(dto.Tasks, taskDTOFrom(t.AgentTaskFields))
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
			"the claim stopped at a refusal (%s) after %s; those are committed and yours", output.SafeCell(code), claimed), code)
	case reason == claimRefused || reason == claimFailed:
		// No code to map: never reported as success.
		return exitcode.Newf(exitcode.Error,
			"the claim stopped (%s) after %s; those are committed and yours", output.SafeCell(reason), claimed)
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
