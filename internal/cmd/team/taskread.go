package team

// `team task get|list|tree` — the read half of #830. Plain reads: no session
// header, no read-state side effects, the server's derivation rendered as
// returned.

import (
	"context"
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

// taskStatusDoc is the one help-text spelling of the status set.
const taskStatusDoc = "PENDING, BACKLOG, STARTED, FINISHED or CANCELLED (cor:agt:050:02)"

// alarmPageLimit bounds the tree's alarm paging even against a server that
// advances: 100 pages at the 200 cap is twenty thousand alarm events, past
// which a render wants a specialist tool, not a wider loop.
const alarmPageLimit = 100

// taskStatuses mirrors AgentTaskStatus; validated client-side so a typo is a
// usage error, not an empty server result.
var taskStatuses = map[string]bool{
	"PENDING":   true,
	"BACKLOG":   true,
	"STARTED":   true,
	"FINISHED":  true,
	"CANCELLED": true,
}

// partyCell renders a party for a table cell: the name when there is one,
// else the id. Never empty enough to look like a bug.
func partyCell(p *taskPartyDTO) string {
	if p == nil {
		return "—"
	}
	if p.Name != nil && *p.Name != "" {
		return *p.Name
	}
	return p.ID
}

// ---- get ------------------------------------------------------------------

func newCmdTeamTaskGet(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <taskId>",
		Short: "Show one task",
		Long: `Show one task, with priority, blocked state and the first 20 dependencies in
each direction (total/hasMore included). Use task dependencies for later pages. A task you may not read answers "not found"
(no existence oracle) — the server's shape, kept honest here.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskId, err := taskRefArg(args)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentTaskGet(cmd.Context(), client, taskId)
			if err != nil {
				return api.MapError(err)
			}
			if resp.AgentTask == nil {
				return exitcode.Newf(exitcode.NotFound, "task %s not found", taskId)
			}
			task := taskDTOFrom(resp.AgentTask.AgentTaskFields)
			return output.Write(f.IOStreams, f.JSON, task, func(w io.Writer) error {
				return renderTaskDetail(w, task)
			})
		},
	}
	return cmd
}

func renderTaskDetail(w io.Writer, task taskDTO) error {
	line := func(format string, a ...any) error {
		_, err := fmt.Fprintf(w, format+"\n", a...)
		return err
	}
	if err := line("%s  %s  (%s)", task.ID, task.Title, task.Status); err != nil {
		return err
	}
	if err := line("  priority: %d (1 = highest); blocked: %t", task.Priority, task.Blocked); err != nil {
		return err
	}
	if err := renderDependencyPage(w, "blocked by", task.BlockedBy); err != nil {
		return err
	}
	if err := renderDependencyPage(w, "blocks", task.Blocks); err != nil {
		return err
	}
	if task.Description != nil {
		if err := line("  %s", *task.Description); err != nil {
			return err
		}
	}
	if err := line("  app: %s", task.AppID); err != nil {
		return err
	}
	if err := line("  owner: %s  created by: %s", partyCell(task.Owner), partyCell(task.CreatedBy)); err != nil {
		return err
	}
	if task.Assignee != nil {
		if err := line("  assignee: %s (assigned by %s)", partyCell(task.Assignee), partyCell(task.AssignedBy)); err != nil {
			return err
		}
	} else if task.Queued {
		if err := line("  queued: no assignee yet"); err != nil {
			return err
		}
	}
	if err := writeCloseSessionLine(w, task); err != nil {
		return err
	}
	if task.ParentTaskID != nil {
		if err := line("  parent: %s", *task.ParentTaskID); err != nil {
			return err
		}
	}
	if task.WaitingOnChildren {
		if err := line("  waiting on children"); err != nil {
			return err
		}
	}
	if task.StartBy != nil {
		if err := line("  start by: %s", *task.StartBy); err != nil {
			return err
		}
	}
	if len(task.Tags) > 0 {
		if err := line("  tags: %s", strings.Join(task.Tags, ", ")); err != nil {
			return err
		}
	}
	if task.Source != nil {
		if err := line("  source: %s", *task.Source); err != nil {
			return err
		}
	}
	if task.Requirements != nil {
		if err := line("  requirements: role=%s familyIn=%s familyNotIn=%s exclude=%s",
			strOr(task.Requirements.Role),
			strOrList(task.Requirements.FamilyIn),
			strOrList(task.Requirements.FamilyNotIn),
			strOrList(task.Requirements.ExcludeWorkerIds)); err != nil {
			return err
		}
	}
	if task.Result != nil {
		if err := line("  result: %s", task.Result.Outcome); err != nil {
			return err
		}
		if task.Result.Summary != nil {
			if err := line("    %s", *task.Result.Summary); err != nil {
				return err
			}
		}
		if task.Result.Evidence != nil {
			if err := line("    evidence: %s", *task.Result.Evidence); err != nil {
				return err
			}
		}
	}
	if err := line("  created: %s", task.CreatedAt); err != nil {
		return err
	}
	for _, stamp := range []struct {
		label string
		at    *string
	}{
		{"started", task.StartedAt},
		{"finished", task.FinishedAt},
		{"cancelled", task.CancelledAt},
		{"updated", task.UpdatedAt},
	} {
		if stamp.at != nil {
			if err := line("  %s: %s", stamp.label, *stamp.at); err != nil {
				return err
			}
		}
	}
	return nil
}

// strOr renders an optional string, an em dash for none.
func strOr(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

func strOrList(list []string) string {
	if len(list) == 0 {
		return "—"
	}
	return strings.Join(list, ",")
}

// ---- list -----------------------------------------------------------------

func newCmdTeamTaskList(f *cmdutil.Factory) *cobra.Command {
	var statuses []string
	var assignee, owner, parent, tag, team, staleOver string
	var queued, rootsOnly, tree bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the App's tasks (newest first)",
		Long: `List the team App's tasks, newest first (the server's order). Filters
combine with AND: --status (repeatable, ` + taskStatusDoc + `), --assignee and
--owner name a party, --queued keeps only pending tasks with no assignee,
--roots-only drops children, --parent lists one task's children, --tag
filters by tag. --limit/--offset page (default 20, cap 200); a full default page says more may exist.

--team is a coordinator's view of work in flight (#1044). The team is the
coordinator — the bound worker for a bare --team, or --team=worker:<id, URN
or name> (attached with =) — plus every worker whose roster reportsTo chain
reaches them, in this App. It lists the App's tasks a team member OWNS or is
ASSIGNED, plus every descendant of those, even where the descendant's parties
sit outside the team; owner/assignee cells say "(outside)" for a worker in
another reporting subtree and "(not on roster)" for one the roster does not
list (a retired worker: the roster omits them, so their tasks join only as a
descendant or through a current team member). Who CREATED a task does not
count yet, and other Apps are never read (hadron-server#2370). It reads every
page of the App's tasks (no truncation; a page that does not advance is
refused, exit 1), then applies --status after the join, so an open task under
a finished team task stays in view. It combines only with --status, --tree
and --stale-over.
  --tree draws parent/child trees with status, idle time, owner, assignee,
    title and open alarms; a row whose parent is not shown is a root.
  --stale-over <duration> (e.g. 1h) keeps PENDING/STARTED tasks idle longer
    than that, where idle runs from the record's updatedAt, else startedAt,
    else createdAt. Records lag real progress (PR pushes, receipts); a stderr
    note says so.
--json is an object: {appId, coordinator, coordinatorSource, members,
staleOverSeconds, tasks}, each task the plain task plus ownerTeam/assigneeTeam
(team|outside|unknown|user|null), match (party|descendant), ageSeconds and
ageFrom; --tree changes text only.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			for _, s := range statuses {
				if !taskStatuses[strings.ToUpper(s)] {
					return exitcode.Newf(exitcode.Usage, "--status takes %s, got %q", taskStatusDoc, s)
				}
			}
			stale, err := checkTeamListFlags(cmd, args, team, staleOver)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("team") {
				b, err := readBindingOrNilWithApp(ctx, f)
				if err != nil {
					return err
				}
				scope, err := resolveTeamAppScope(ctx, f, b)
				if err != nil {
					return err
				}
				wanted := map[string]bool{}
				for _, s := range statuses {
					wanted[strings.ToUpper(s)] = true
				}
				return listTeamTasks(ctx, f, scope, b, teamTaskQuery{team: team, status: wanted, staleOver: stale, tree: tree})
			}
			if err := checkTaskPage(limit, offset); err != nil {
				return err
			}
			var status []gen.AgentTaskStatus
			for _, s := range statuses {
				status = append(status, gen.AgentTaskStatus(strings.ToUpper(s)))
			}
			var assigneeIn, ownerIn *gen.AgentTaskPartyInput
			if assignee != "" {
				p, err := parseTaskPartyFlag("--assignee", assignee)
				if err != nil {
					return err
				}
				assigneeIn = p
			}
			if owner != "" {
				p, err := parseTaskPartyFlag("--owner", owner)
				if err != nil {
					return err
				}
				ownerIn = p
			}
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			return listTasks(ctx, f, scope, taskListQuery{
				assignee: assigneeIn, owner: ownerIn, parent: optStr(parent), queued: optBool(queued),
				rootsOnly: optBool(rootsOnly), status: status, tag: optStr(tag), limit: limit, offset: offset,
				limitExplicit: cmd.Flags().Changed("limit"),
			})
		},
	}
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "status filter, repeatable ("+taskStatusDoc+")")
	cmd.Flags().StringVar(&assignee, "assignee", "", "party filter (worker:<…> / user:<…>)")
	cmd.Flags().StringVar(&owner, "owner", "", "party filter (worker:<…> / user:<…>)")
	cmd.Flags().StringVar(&parent, "parent", "", "list this task's children")
	cmd.Flags().StringVar(&tag, "tag", "", "tag filter")
	cmd.Flags().BoolVar(&queued, "queued", false, "only queued tasks (pending, no assignee)")
	cmd.Flags().BoolVar(&rootsOnly, "roots-only", false, "drop child tasks")
	cmd.Flags().IntVar(&limit, "limit", taskListDefaultLimit, "page size (default 20, server cap 200)")
	cmd.Flags().IntVar(&offset, "offset", 0, "page offset")
	cmd.Flags().StringVar(&team, "team", "", "a coordinator's team view: bare for the bound worker, or --team=worker:<ref>")
	cmd.Flags().Lookup("team").NoOptDefVal = teamFlagBound
	cmd.Flags().BoolVar(&tree, "tree", false, "with --team: render parent/child trees")
	cmd.Flags().StringVar(&staleOver, "stale-over", "", "with --team: keep PENDING/STARTED tasks idle longer than this duration (e.g. 1h)")
	return cmd
}

// taskListQuery is one AgentTaskList request's filters and page.
type taskListQuery struct {
	assignee, owner   *gen.AgentTaskPartyInput
	parent, tag       *string
	queued, rootsOnly *bool
	status            []gen.AgentTaskStatus
	limit, offset     int
	// limitExplicit is true when the user passed --limit: a full page of the
	// default size is then their own page, not a silent truncation.
	limitExplicit bool
}

// taskListDefaultLimit is the page `task list` and `task mine` ask for when
// --limit is unset (cli#967: hidden defaults stay at 20 or less).
const taskListDefaultLimit = 20

func checkTaskPage(limit, offset int) error {
	if limit < 1 || limit > teamEventPageCap {
		return exitcode.Newf(exitcode.Usage, "--limit takes 1 to %d (the server's page cap) — %d would be clamped silently", teamEventPageCap, limit)
	}
	if offset < 0 {
		return exitcode.Newf(exitcode.Usage, "--offset takes a non-negative page offset, got %d", offset)
	}
	return nil
}

// listTasks is `task list`'s request and render, shared with `task mine`
// so the shortcut's output is the list's own.
func listTasks(ctx context.Context, f *cmdutil.Factory, scope appScope, q taskListQuery) error {
	appLabel := lazyAppLabel(ctx, f, scope)
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	resp, err := gen.AgentTaskList(ctx, client, scope.Ref, q.assignee, &q.limit, &q.offset, q.owner, q.parent, q.queued, q.rootsOnly, q.status, q.tag)
	if err != nil {
		return api.MapError(err)
	}
	tasks := []taskDTO{}
	for _, t := range resp.AgentTasks {
		if t != nil {
			tasks = append(tasks, taskDTOFrom(t.AgentTaskFields))
		}
	}
	// agentTasks returns no total, so a full DEFAULT page cannot show that the
	// list ended: say so on stderr (the output shape stays a bare array).
	if !q.limitExplicit && len(resp.AgentTasks) >= q.limit {
		_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "Showing a full page of %d tasks; there may be more: --offset %d\n", q.limit, q.offset+q.limit)
	}
	return output.Write(f.IOStreams, f.JSON, tasks, func(w io.Writer) error {
		// Unconditionally, and before the rows (#470): an empty list
		// is the normal steady state of a quiet App, and without this
		// line "no tasks" and "another team's App" look identical.
		if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
			return err
		}
		t := output.NewTable(w, "ID", "STATUS", "PRIORITY", "BLOCKED", "BLOCKED BY", "BLOCKS", "TITLE", "OWNER", "ASSIGNEE")
		for _, task := range tasks {
			assignee := "—"
			if task.Queued {
				assignee = "(queued)"
			} else if task.Assignee != nil {
				assignee = partyCell(task.Assignee)
			}
			t.Row(output.SafeCell(task.ID), output.SafeCell(task.Status), strconv.Itoa(task.Priority), strconv.FormatBool(task.Blocked), dependencySummary(task.BlockedBy), dependencySummary(task.Blocks), output.SafeCell(task.Title), output.SafeCell(partyCell(task.Owner)), output.SafeCell(assignee))
		}
		return t.Flush()
	})
}

// ---- mine -----------------------------------------------------------------

// newCmdTeamTaskMine is #910 (C4 of hadron-server#2016): "give me my
// assignment". It is `task list` with the filters fixed — the bound worker
// as assignee, PENDING and STARTED — and no way to widen them, so it refuses
// rather than fall back to a person, an unfiltered list or another App.
func newCmdTeamTaskMine(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "mine",
		Short: "List the bound worker's open tasks",
		Long: `List the tasks assigned to this worktree's bound worker that are PENDING or
STARTED — ` + "`task list --assignee worker:<bound worker> --status PENDING --status STARTED`" + `
in the bound worker's App, with the same output. The filters are fixed.
--limit/--offset page (default 20, cap 200); a full default page says more may exist.

It needs a worker session binding: without one, start a session with
` + "`hadron team session start`" + `. --app may name the bound App (by id, URN
or <root>:<slug>) but no other: "mine" never means another App, and the
binding's server must be the one this call targets.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := checkTaskPage(limit, offset); err != nil {
				return err
			}
			b, _, err := readBinding(ctx)
			if err != nil {
				return err
			}
			if b == nil {
				return exitcode.Newf(exitcode.Usage, "`task mine` lists the bound worker's tasks, and this worktree has no worker session — start one with `hadron team session start`")
			}
			if b.SessionID == "" || b.WorkerID == "" || b.AppID == "" {
				return exitcode.Newf(exitcode.Usage, "the worker session binding does not record its session, worker and App — re-run `hadron team session start`")
			}
			if !bindingServerMatches(f, b) {
				server, _ := f.Server()
				return exitcode.Newf(exitcode.Usage, "the worker session is bound on %s, and this call targets %s — `task mine` reads only the bound server", b.Server, server)
			}
			// --app may restate the bound App in any spelling: compared by
			// identity, and the query below stays on the bound id.
			if f.AppFlag != "" {
				ref, err := f.App()
				if err != nil {
					return err
				}
				same, err := bindingsAppIdentity(ctx, f, ref, b.AppID)
				if err != nil {
					return err
				}
				if !same {
					return exitcode.Newf(exitcode.Usage, "`task mine` reads the bound worker's App %s; --app %s is not that App (omit --app)", b.AppID, f.AppFlag)
				}
			}
			worker := b.WorkerID
			return listTasks(ctx, f, appScope{Ref: b.AppID, Source: "from the worktree binding", Ambient: true}, taskListQuery{
				assignee: &gen.AgentTaskPartyInput{Worker: &worker},
				status:   []gen.AgentTaskStatus{gen.AgentTaskStatusPending, gen.AgentTaskStatusStarted},
				limit:    limit,
				offset:   offset,

				limitExplicit: cmd.Flags().Changed("limit"),
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", taskListDefaultLimit, "page size (default 20, server cap 200)")
	cmd.Flags().IntVar(&offset, "offset", 0, "page offset")
	return cmd
}

// optBool turns a false flag into a nil (omitted) variable: only an explicitly
// set filter goes on the wire.
func optBool(b bool) *bool {
	if !b {
		return nil
	}
	return &b
}

// ---- tree -----------------------------------------------------------------

func newCmdTeamTaskTree(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tree <rootTaskId>",
		Short: "Show a task's parent/child chain",
		Long: `Render the task and its children as a tree, with status, owner,
assignee and OPEN ALARMS per node. The alarms come from the App's unacknowledged
alarm events, joined onto the tree client-side — the join is presentation, the
alarm facts are the server's.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			rootId, err := taskRefArg(args)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentTaskTree(ctx, client, rootId)
			if err != nil {
				return api.MapError(err)
			}
			if len(resp.AgentTaskTree) == 0 {
				return exitcode.Newf(exitcode.NotFound, "task %s not found", rootId)
			}
			appID := ""
			for _, t := range resp.AgentTaskTree {
				if t != nil {
					appID = t.AppId
					break
				}
			}
			alarmsByTask, err := openAlarmsByTask(ctx, client, f.IOStreams.ErrOut, appID)
			if err != nil {
				return err
			}
			tasks := []taskDTO{}
			for _, t := range resp.AgentTaskTree {
				if t == nil {
					continue
				}
				dto := taskDTOFrom(t.AgentTaskFields)
				if n, ok := alarmsByTask[dto.ID]; ok {
					open := n
					dto.OpenAlarms = &open
				}
				tasks = append(tasks, dto)
			}
			// The server returns the whole chain root-first but flat; the
			// parent/child shape is this render's. An orphan (its parent row
			// outside the chain) renders at the top level rather than
			// silently dropping a task the read did return. A CYCLE (A under
			// B under A — corrupt parent links, nothing the healthy server
			// mints, but nothing this render may ASSUME either) leaves roots
			// empty; the fallback then renders every row flat, and the
			// visited set stops the walk from chasing the loop forever
			// (cycle 2, Xylon P2-3: the empty-roots case used to render
			// NOTHING at exit 0 — the #470-class sin in this very render).
			children := map[string][]*taskDTO{}
			roots := []*taskDTO{}
			for i := range tasks {
				if tasks[i].ParentTaskID == nil {
					roots = append(roots, &tasks[i])
					continue
				}
				if parent, ok := findByID(tasks, *tasks[i].ParentTaskID); ok {
					children[parent.ID] = append(children[parent.ID], &tasks[i])
				} else {
					roots = append(roots, &tasks[i])
				}
			}
			if len(roots) == 0 && len(tasks) > 0 {
				for i := range tasks {
					roots = append(roots, &tasks[i])
				}
			}
			return output.Write(f.IOStreams, f.JSON, tasks, func(w io.Writer) error {
				visited := map[string]bool{}
				var draw func(*taskDTO, int) error
				draw = func(task *taskDTO, depth int) error {
					if visited[task.ID] {
						return nil
					}
					visited[task.ID] = true
					if err := renderTreeNode(w, task, depth); err != nil {
						return err
					}
					for _, child := range children[task.ID] {
						if err := draw(child, depth+1); err != nil {
							return err
						}
					}
					return nil
				}
				for _, root := range roots {
					if err := draw(root, 0); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	return cmd
}

// openAlarmsByTask counts the App's alarm events not yet acknowledged, keyed
// by task — `task tree`'s alarm join, shared with `task list --team`. Paged
// to exhaustion with the after cursor: one bounded read stopped at the first
// page and silently missed every later alarm, open ones included (cycle 1,
// Diego P2-1; a team with 205 alarms rendered 49 open). An empty appID reads
// nothing.
func openAlarmsByTask(ctx context.Context, client graphql.Client, errOut io.Writer, appID string) (map[string]int, error) {
	alarmsByTask := map[string]int{}
	if appID == "" {
		return alarmsByTask, nil
	}
	var after *string
	alarmPageSize := teamEventPageCap
	// Non-advancement safeguard (cycle 2, Xylon P3-4): a server that repeats
	// the same full page for every cursor would loop forever. A repeated last
	// id, or an absurd page count, stops the walk with a stderr note — the
	// counts render possibly incomplete rather than never arriving.
	prevLast := ""
	for page := 0; ; page++ {
		events, err := gen.AgentTeamEvents(ctx, client, appID, after, boolPtr(true), &alarmPageSize, nil, nil, nil)
		if err != nil {
			return nil, api.MapError(err)
		}
		pageSize := len(events.AgentTeamEvents)
		last := ""
		for _, e := range events.AgentTeamEvents {
			if e == nil {
				continue
			}
			last = e.Id
			if e.Severity != nil && e.AcknowledgedAt == nil && e.TaskId != nil {
				alarmsByTask[*e.TaskId]++
			}
		}
		if pageSize < alarmPageSize || last == "" {
			return alarmsByTask, nil
		}
		if last == prevLast || page >= alarmPageLimit {
			fmt.Fprintf(errOut,
				"note: alarm paging stopped at %s without exhausting the stream; open-alarm counts may be incomplete\n", last)
			return alarmsByTask, nil
		}
		prevLast = last
		after = &last
	}
}

func findByID(tasks []taskDTO, id string) (*taskDTO, bool) {
	for i := range tasks {
		if tasks[i].ID == id {
			return &tasks[i], true
		}
	}
	return nil, false
}

func renderTreeNode(w io.Writer, task *taskDTO, depth int) error {
	indent := strings.Repeat("  ", depth)
	alarm := ""
	if task.OpenAlarms != nil && *task.OpenAlarms > 0 {
		alarm = fmt.Sprintf("  ⏰ %s open alarm%s", strconv.Itoa(*task.OpenAlarms), plural(*task.OpenAlarms))
	}
	assignee := "—"
	if task.Assignee != nil {
		assignee = partyCell(task.Assignee)
	}
	flags := ""
	if task.WaitingOnChildren {
		flags += "  waiting-on-children"
	}
	if _, err := fmt.Fprintf(w, "%s• %s [%s] %s → %s%s%s\n", indent, task.ID, task.Status, partyCell(task.Owner), assignee, flags, alarm); err != nil {
		return err
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
