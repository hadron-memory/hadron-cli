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

func dependencyPage[T taskDependencyGetter](total int, more bool, items []T) *taskDependencyPageDTO {
	d := &taskDependencyPageDTO{Items: []taskDependencyDTO{}, Total: total, HasMore: more}
	for _, v := range items {
		var zero T
		if any(v) == any(zero) {
			continue
		}
		d.Items = append(d.Items, taskDependencyDTO{v.GetId(), v.GetHandle(), v.GetTitle(), string(v.GetStatus())})
	}
	return d
}

func newCmdTaskDependencyWrite(f *cmdutil.Factory, verb string) *cobra.Command {
	priority := verb == "set-priority"
	arg := "<blocker-task>"
	short := "Add a blocker to a task"
	if priority {
		arg = "<priority>"
		short = "Set a task's matching priority"
	} else if verb == "remove-blocker" {
		short = "Remove a blocker from a task"
	}
	cmd := &cobra.Command{Use: verb + " <task> " + arg, Short: short,
		Long: short + `. Task references are ids or server-supported handles (AT1.2).
Priority defaults to 1 on creation; range 1-5, 1 = highest. It affects matching,
not authority or the task list's ordering. Only whoever may assign the task
may change priority or blockers. Blockers change only on pending/backlog tasks;
the server checks same-App references, cycles and every transition.
Sign in first. Task references determine the App on the server; --app does not
qualify these references. When a bare handle is ambiguous across your Apps,
pass an id or a qualified handle such as
hrn:agenttask:example.com:team:AT1.2.
Success prints the returned task, priority and dependency summaries; --json
returns the same fields as structured data. A server refusal prints its code
and message with no task output; check task get before changing the request.
Each invocation sends one mutation and never retries it.`, Example: "  hadron team task " + verb + " AT1.2 " + map[bool]string{true: "2", false: "AT1.3"}[priority], Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := taskRefArg(args[:1])
			if err != nil {
				return err
			}
			second := strings.TrimSpace(args[1])
			if second == "" {
				return exitcode.Newf(exitcode.Usage, "%s must not be empty", arg)
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
			switch verb {
			case "set-priority":
				r, e := gen.AgentTaskSetPriority(cmd.Context(), client, ref, n)
				if e != nil {
					return mapTaskError(e)
				}
				if r.SetAgentTaskPriority == nil {
					return exitcode.Newf(exitcode.Error, "server returned no task")
				}
				fields = r.SetAgentTaskPriority.AgentTaskFields
			case "add-blocker":
				r, e := gen.AgentTaskAddBlocker(cmd.Context(), client, ref, second)
				if e != nil {
					return mapTaskError(e)
				}
				if r.AddAgentTaskBlocker == nil {
					return exitcode.Newf(exitcode.Error, "server returned no task")
				}
				fields = r.AddAgentTaskBlocker.AgentTaskFields
			case "remove-blocker":
				r, e := gen.AgentTaskRemoveBlocker(cmd.Context(), client, ref, second)
				if e != nil {
					return mapTaskError(e)
				}
				if r.RemoveAgentTaskBlocker == nil {
					return exitcode.Newf(exitcode.Error, "server returned no task")
				}
				fields = r.RemoveAgentTaskBlocker.AgentTaskFields
			}
			dto := taskDTOFrom(fields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return renderTaskDetail(w, dto) })
		}}
	return cmd
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
	c := &cobra.Command{Use: "dependencies <task>", Short: "Read pages of a task's blockers and what it blocks", Long: `Read both dependency directions with explicit total/hasMore metadata.
Task get/list and write JSON show the first 20 of each. This command pages
both directions with --limit/--offset; follow hasMore, never infer completeness
from a short page. Refs are ids or server-supported handles. Sign in first. --app does not qualify
these references. For an ambiguous bare handle, pass an id or a qualified handle
such as hrn:agenttask:example.com:team:AT1.2. Text prints each direction's page and
counts; --json returns id, blockedBy and blocks with items/total/hasMore.
A hidden or missing task exits 4. Reads write nothing.`, Example: "  hadron team task dependencies AT1.2 --limit 20 --offset 20 --json", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
		if err := checkTaskPage(limit, offset); err != nil {
			return err
		}
		ref, err := taskRefArg(a)
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
			d.BlockedBy = dependencyPage(p.Total, p.HasMore, p.Items)
		}
		if p := r.AgentTask.Blocks; p != nil {
			d.Blocks = dependencyPage(p.Total, p.HasMore, p.Items)
		}
		return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			if err := renderDependencyPage(w, "blocked by", d.BlockedBy); err != nil {
				return err
			}
			return renderDependencyPage(w, "blocks", d.Blocks)
		})
	}}
	c.Flags().IntVar(&limit, "limit", 20, "page size (default 20, cap 200)")
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
