package team

// `team task get|list|tree` — the read half of #830. Plain reads: no session
// header, no read-state side effects, the server's derivation rendered as
// returned.

import (
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

// taskStatusDoc is the one help-text spelling of the status set.
const taskStatusDoc = "PENDING, STARTED, FINISHED or CANCELLED (cor:agt:050:02)"

// taskStatuses mirrors AgentTaskStatus; validated client-side so a typo is a
// usage error, not an empty server result.
var taskStatuses = map[string]bool{
	"PENDING":   true,
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
		Long: `Show one task in full. A task you may not read answers "not found"
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
	var assignee, owner, parent, tag string
	var queued, rootsOnly bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the App's tasks (newest first)",
		Long: `List the team App's tasks, newest first (the server's order). Filters
combine with AND: --status (repeatable, ` + taskStatusDoc + `), --assignee and
--owner name a party, --queued keeps only pending tasks with no assignee,
--roots-only drops children, --parent lists one task's children, --tag
filters by tag. --limit/--offset page (server default 50, cap 200).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if limit < 1 || limit > teamEventPageCap {
				return exitcode.Newf(exitcode.Usage, "--limit takes 1 to %d (the server's page cap) — %d would be clamped silently", teamEventPageCap, limit)
			}
			for _, s := range statuses {
				if !taskStatuses[strings.ToUpper(s)] {
					return exitcode.Newf(exitcode.Usage, "--status takes %s, got %q", taskStatusDoc, s)
				}
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
			appLabel := lazyAppLabel(ctx, f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentTaskList(ctx, client, scope.Ref, assigneeIn, &limit, &offset, ownerIn, optStr(parent), optBool(queued), optBool(rootsOnly), status, optStr(tag))
			if err != nil {
				return api.MapError(err)
			}
			tasks := []taskDTO{}
			for _, t := range resp.AgentTasks {
				if t != nil {
					tasks = append(tasks, taskDTOFrom(t.AgentTaskFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, tasks, func(w io.Writer) error {
				// Unconditionally, and before the rows (#470): an empty list
				// is the normal steady state of a quiet App, and without this
				// line "no tasks" and "another team's App" look identical.
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				t := output.NewTable(w, "ID", "STATUS", "TITLE", "OWNER", "ASSIGNEE")
				for _, task := range tasks {
					assignee := "—"
					if task.Queued {
						assignee = "(queued)"
					} else if task.Assignee != nil {
						assignee = partyCell(task.Assignee)
					}
					t.Row(task.ID, task.Status, task.Title, partyCell(task.Owner), assignee)
				}
				return t.Flush()
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
	cmd.Flags().IntVar(&limit, "limit", 50, "page size (server cap 200)")
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
			// Open alarms: the App's alarm events not yet acknowledged,
			// keyed by task. Paged to exhaustion with the after cursor —
			// one bounded read stopped at the first page and silently
			// missed every later alarm, open ones included (cycle 1,
			// Diego P2-1; a team with 205 alarms rendered 49 open).
			alarmsByTask := map[string]int{}
			appID := ""
			for _, t := range resp.AgentTaskTree {
				if t != nil {
					appID = t.AppId
					break
				}
			}
			if appID != "" {
				var after *string
				alarmPageSize := teamEventPageCap
				for {
					events, err := gen.AgentTeamEvents(ctx, client, appID, after, boolPtr(true), &alarmPageSize, nil, nil, nil)
					if err != nil {
						return api.MapError(err)
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
						break
					}
					after = &last
				}
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
			// silently dropping a task the read did return.
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
			return output.Write(f.IOStreams, f.JSON, tasks, func(w io.Writer) error {
				var draw func(*taskDTO, int) error
				draw = func(task *taskDTO, depth int) error {
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
