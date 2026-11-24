package team

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type taskDependencyDTO struct {
	ID     string `json:"id"`
	Handle string `json:"handle"`
	Title  string `json:"title"`
	Status string `json:"status"`
}
type taskDependencyPageDTO struct {
	Items   []taskDependencyDTO `json:"items"`
	Total   int                 `json:"total"`
	HasMore bool                `json:"hasMore"`
}
type taskDependencyGetter interface {
	GetId() string
	GetHandle() string
	GetTitle() string
	GetStatus() gen.AgentTaskStatus
}

// dependencyPage copies one provider page into its DTO. The provider declares
// its items non-null ([AgentTask!]!), so a null item, or one with no id, is a
// malformed answer: it is refused (exit 1, like every "server returned ..."
// refusal, naming the page and the item) instead of skipped, which would make
// len(Items) disagree with the page and hide a bad answer behind a good-looking
// one (cli#1182). P is constrained to a pointer so the nil check is explicit.
func dependencyPage[E any, P interface {
	*E
	taskDependencyGetter
}](label string, total int, more bool, items []P) (*taskDependencyPageDTO, error) {
	if items == nil {
		// null or absent: a decoded empty array is non-nil, so this is never a
		// legitimate empty page.
		return nil, exitcode.Newf(exitcode.Error, "server returned a malformed %s page: items is missing or null", label)
	}
	d := &taskDependencyPageDTO{Items: []taskDependencyDTO{}, Total: total, HasMore: more}
	for i, v := range items {
		if v == nil || v.GetId() == "" {
			return nil, exitcode.Newf(exitcode.Error, "server returned a malformed %s page: item %d of %d is empty", label, i+1, len(items))
		}
		d.Items = append(d.Items, taskDependencyDTO{v.GetId(), v.GetHandle(), v.GetTitle(), string(v.GetStatus())})
	}
	return d, nil
}

func newCmdTaskDependencyWrite(f *cmdutil.Factory, verb string) *cobra.Command {
	var rawInstruction string
	priority := verb == "set-priority"
	arg := "<blocker-task>"
	short := "Add a blocker to a task"
	if priority {
		arg = "<priority>"
		short = "Set a task's matching priority"
	} else if verb == "remove-blocker" {
		short = "Remove a blocker from a task"
	}
	cmd := &cobra.Command{
		Use:     verb + " <task> " + arg,
		Short:   short,
		Long:    dependencyWriteLong(verb) + "\n\n" + taskInstructionHelpShort(taskInstructionFloor),
		Example: dependencyWriteExample(verb),
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			claim, err := taskClaim(cmd, rawInstruction)
			if err != nil {
				return err
			}
			ref, err := taskRefArg(f, args[:1])
			if err != nil {
				return err
			}
			second := strings.TrimSpace(args[1])
			if second == "" {
				return exitcode.Newf(exitcode.Usage, "%s must not be empty", arg)
			}
			if !priority {
				// The blocker is a task too (cli#1831).
				if second, err = qualifyTaskRef(f, second); err != nil {
					return err
				}
			}
			var n int
			if priority {
				n, err = strconv.Atoi(second)
				if err != nil {
					return exitcode.Newf(exitcode.Usage, "priority must be an integer (range 1-5, 1 = highest)")
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var fields gen.AgentTaskFields
			var e error
			switch {
			case verb == "set-priority" && claim != nil:
				var r *gen.AgentTaskSetPriorityInstructedResponse
				if r, e = gen.AgentTaskSetPriorityInstructed(cmd.Context(), client, ref, n, claim); e == nil && r.SetAgentTaskPriority != nil {
					fields = r.SetAgentTaskPriority.AgentTaskFields
				} else if e == nil {
					return exitcode.Newf(exitcode.Error, "server returned no task")
				}
			case verb == "set-priority":
				var r *gen.AgentTaskSetPriorityResponse
				if r, e = gen.AgentTaskSetPriority(cmd.Context(), client, ref, n); e == nil && r.SetAgentTaskPriority != nil {
					fields = r.SetAgentTaskPriority.AgentTaskFields
				} else if e == nil {
					return exitcode.Newf(exitcode.Error, "server returned no task")
				}
			case verb == "add-blocker" && claim != nil:
				var r *gen.AgentTaskAddBlockerInstructedResponse
				if r, e = gen.AgentTaskAddBlockerInstructed(cmd.Context(), client, ref, second, claim); e == nil && r.AddAgentTaskBlocker != nil {
					fields = r.AddAgentTaskBlocker.AgentTaskFields
				} else if e == nil {
					return exitcode.Newf(exitcode.Error, "server returned no task")
				}
			case verb == "add-blocker":
				var r *gen.AgentTaskAddBlockerResponse
				if r, e = gen.AgentTaskAddBlocker(cmd.Context(), client, ref, second); e == nil && r.AddAgentTaskBlocker != nil {
					fields = r.AddAgentTaskBlocker.AgentTaskFields
				} else if e == nil {
					return exitcode.Newf(exitcode.Error, "server returned no task")
				}
			case claim != nil:
				var r *gen.AgentTaskRemoveBlockerInstructedResponse
				if r, e = gen.AgentTaskRemoveBlockerInstructed(cmd.Context(), client, ref, second, claim); e == nil && r.RemoveAgentTaskBlocker != nil {
					fields = r.RemoveAgentTaskBlocker.AgentTaskFields
				} else if e == nil {
					return exitcode.Newf(exitcode.Error, "server returned no task")
				}
			default:
				var r *gen.AgentTaskRemoveBlockerResponse
				if r, e = gen.AgentTaskRemoveBlocker(cmd.Context(), client, ref, second); e == nil && r.RemoveAgentTaskBlocker != nil {
					fields = r.RemoveAgentTaskBlocker.AgentTaskFields
				} else if e == nil {
					return exitcode.Newf(exitcode.Error, "server returned no task")
				}
			}
			if e != nil {
				return mapTaskError(e)
			}
			dto, err := taskDTOFromWrite(fields)
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return renderTaskDetail(w, dto) })
		},
	}
	addTaskInstructionFlag(cmd, &rawInstruction)
	return cmd
}

// dependencyWriteLong is the page for set-priority/add-blocker/remove-blocker:
// the verb's own effect first, then the authority and reference rules they
// share (cor:agt:050:09).
func dependencyWriteLong(verb string) string {
	var head string
	switch verb {
	case "set-priority":
		head = `Set a task's matching priority, 1 to 5, where 1 is the highest; a new task
has 1. The task may be BACKLOG, PENDING or STARTED; an ended task is refused
(exit 5), and a value outside 1-5 is BAD_USER_INPUT (exit 2).

Priority only affects which queued task the platform's matcher offers first:
among PENDING tasks with no assignee and no open blocker, priority 1 goes
first, then the oldest. It does not change the task list's order, an existing
assignment, or who may act on the task; it preempts no started work and
assigns nothing by itself.`
	case "add-blocker":
		head = `Make a task wait for another: add-blocker A B means A waits for B. Both must
be in the same App, and A must be BACKLOG or PENDING (exit 5 otherwise). A
blocker that would close a loop is TASK_BLOCKER_CYCLE (exit 5).

While a blocker is open, A is never offered by the matcher and its assignee
cannot start it (TASK_BLOCKED). B stops blocking when it is FINISHED or
CANCELLED; when the last open blocker ends, A is released automatically and
keeps its status and assignee. Adding a blocker that A already has changes
nothing.`
	default:
		head = `Remove a blocker: remove-blocker A B means A no longer waits for B. A must be
BACKLOG or PENDING (exit 5 otherwise). Removing a blocker that A does not
have changes nothing.

Removing the last open blocker makes A matchable again if it is queued, or
startable by its assignee. It keeps its status and assignee and does not start
by itself; its stall clocks restart from now. To release A because B is done,
you need not remove anything: finishing or cancelling B releases it.`
	}
	return head + `

Who may: ` + taskAssignerDoc + `;
anyone else is refused with TASK_NOT_OWNER (exit 8). Check a task's status and
blockers first with ` + "`task get`" + `.

Task references are ids or handles (AT1.2). Sign in first. --app names the App
a bare handle belongs to, for every task argument. Without --app, a bare handle
is refused (TASK_HANDLE_QUALIFY) when you belong to more than one App:
pass --app, an id or a qualified handle such as hrn:agenttask:example.com:team:AT1.2.
Success prints the returned task with its priority and dependency summaries;
--json returns the same fields as structured data. A refusal prints its code
and message with no task output. Each invocation sends one request and never
retries it.`
}

func dependencyWriteExample(verb string) string {
	switch verb {
	case "set-priority":
		return "  hadron team task set-priority AT1.2 2"
	case "add-blocker":
		return "  # AT1.3 waits for AT1.2\n  hadron team task add-blocker AT1.3 AT1.2"
	}
	return "  hadron team task remove-blocker AT1.3 AT1.2"
}

func renderDependencyPage(w io.Writer, label string, p *taskDependencyPageDTO) error {
	if p == nil {
		_, e := fmt.Fprintf(w, "  %s: unavailable\n", label)
		return e
	}
	if _, err := fmt.Fprintf(w, "  %s: %d shown of %d (more: %t)\n", label, len(p.Items), p.Total, p.HasMore); err != nil {
		return err
	}
	for _, v := range p.Items {
		if _, err := fmt.Fprintf(w, "    %s (%s) %s: %s\n", output.SafeCell(v.Handle), output.SafeCell(v.ID), output.SafeCell(v.Status), output.SafeCell(v.Title)); err != nil {
			return err
		}
	}
	return nil
}

func newCmdTaskDependencies(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	c := &cobra.Command{
		Use:   "dependencies <task>",
		Short: "Read pages of a task's blockers and what it blocks",
		Long: `Read a task's dependencies in both directions: "blocked by" lists the
tasks it waits for, "blocks" the tasks waiting for it (see
` + "`task add-blocker`" + `). Task get and list show the first 20 of each; this
command pages both directions together with --limit/--offset and reports each
direction's total and hasMore — follow hasMore, never infer completeness from
a short page. Refs are ids or handles. Sign in first. --app names the App a
bare handle belongs to; without it, a bare handle is refused (TASK_HANDLE_QUALIFY) when you belong
to more than one App (pass --app, an id or a qualified handle such as
hrn:agenttask:example.com:team:AT1.2). Text prints each
direction's page and counts; --json returns id, blockedBy and blocks with
items/total/hasMore. A hidden or missing task exits 4. Reads write nothing.`,
		Example: "  hadron team task dependencies AT1.3\n  hadron team task dependencies AT1.3 --limit 20 --offset 20 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			if err := checkTaskPage(limit, offset); err != nil {
				return err
			}
			ref, err := taskRefArg(f, a)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.AgentTaskDependencies(c.Context(), client, ref, limit, offset)
			if err != nil {
				return mapTaskError(err)
			}
			if r.AgentTask == nil {
				return exitcode.Newf(exitcode.NotFound, "task not found")
			}
			d := struct {
				ID        string                 `json:"id"`
				BlockedBy *taskDependencyPageDTO `json:"blockedBy"`
				Blocks    *taskDependencyPageDTO `json:"blocks"`
			}{ID: r.AgentTask.Id}
			if p := r.AgentTask.BlockedBy; p != nil {
				if d.BlockedBy, err = dependencyPage("blockedBy", p.Total, p.HasMore, p.Items); err != nil {
					return err
				}
			}
			if p := r.AgentTask.Blocks; p != nil {
				if d.Blocks, err = dependencyPage("blocks", p.Total, p.HasMore, p.Items); err != nil {
					return err
				}
			}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				if err := renderDependencyPage(w, "blocked by", d.BlockedBy); err != nil {
					return err
				}
				return renderDependencyPage(w, "blocks", d.Blocks)
			})
		},
	}
	c.Flags().IntVar(&limit, "limit", 20, "page size (cap 200)")
	c.Flags().IntVar(&offset, "offset", 0, "offset in both dependency directions")
	return c
}

func dependencySummary(p *taskDependencyPageDTO) string {
	if p == nil {
		return "unavailable"
	}
	refs := []string{}
	for _, v := range p.Items {
		ref := v.Handle
		if ref == "" {
			ref = v.ID
		}
		refs = append(refs, ref)
	}
	return output.SafeCell(fmt.Sprintf("%s (%d/%d; more:%t)", strings.Join(refs, ","), len(p.Items), p.Total, p.HasMore))
}
