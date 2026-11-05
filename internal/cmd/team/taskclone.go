package team

// `hadron team task clone` (hadron-cli#1565, server#2646 cloneAgentTask,
// cor:agt:050:02 "What a clone does"): make a NEW task from an existing one, in
// its App, linked to it. The CLI composes nothing: it sends only the overrides
// a flag names and prints the task the server returns.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdTeamTaskClone(f *cmdutil.Factory) *cobra.Command {
	var title, description, parent, source, status, assignee, startBy, finishBy string
	var priority int
	var noParent, closeSession, noCloseSession bool
	cmd := &cobra.Command{
		Use:   "clone <task>",
		Short: "Make a new task from an existing one",
		Long: `Make a NEW task from an existing one, in its App and linked to it
(cor:agt:050:02). A clone is a create: whoever may read the source and create a
task there may clone it, in any status. The source is never changed and an ended
task is never reopened.

The server copies the title, description, source, tags, requirements,
references, close-session choice, priority, properties and parent. It does not
copy the assignee, status, result, handle, blockers, start-by, finish-by or
external id. The clone is PENDING (or BACKLOG with --status backlog) and a
queued one is matched like any other. Priority and properties are copied only
for a caller who may set them, and are otherwise left out without a refusal.
` + "`task get`" + ` on either task shows the link ("cloned from" / "replaces" and the
clones).

Only the overrides you name are sent; an omitted flag keeps the source's value
(or leaves the field unset). A flag that is given sends its value, an empty one
included, and the server judges it.
  --title, --description, --source, --priority   replace the copied value
  --assignee worker:<name> | user:<handle>        assign the clone (not copied)
  --start-by, --finish-by <RFC 3339>              set a deadline (not copied)
  --close-session | --no-close-session            replace the copied choice
  --status pending|backlog                        the clone's create-time status
  --parent <task>                                 put the clone under another
                                                  parent in the same App
  --no-parent                                     put it under no parent: a root
                                                  under AT0 (--parent and
                                                  --no-parent together are exit 2)
Without either, the parent is kept for someone who may create a child of it
(otherwise TASK_NOT_ASSIGNEE, exit 8, and nothing is created); a root is cloned
as a root, and a clone under an ended parent goes under AT0. Overrides for tags,
requirements, references, properties, owner and project are not exposed yet
(hadron-cli#1579): the copied values stand.

Refusals: an unknown, unreadable or other-App source or parent is exit 4
(TASK_NOT_FOUND, one answer for all three); AT0 itself cannot be cloned
(BAD_USER_INPUT, exit 2); TASK_NOT_ASSIGNEE and a priority you may not set
(TASK_NOT_OWNER) are exit 8; a named parent that has ended is TASK_ENDED, exit 5.
A transport failure after the request left can leave the outcome unknown: list
the App's tasks before you clone again, since a clone is not deduplicated.

Needs a provider with hadron-server#2646; an older one rejects the document as
schema skew (exit 2). Success prints the new task's handle, id and status;
--json returns the task with its warnings list, as ` + "`task create`" + ` does.`,
		Example: `  # a plain copy
  hadron team task clone AT0.12

  # copy it as a backlog item for another worker, with a new title
  hadron team task clone AT0.12 --status backlog --assignee worker:Iris --title "Retest the export fix, round 2"

  # lift a child out of its parent as a root task
  hadron team task clone AT1.4 --no-parent`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if noParent && flags.Changed("parent") {
				return exitcode.Newf(exitcode.Usage, "--parent and --no-parent contradict each other: name a parent or none")
			}
			if flags.Changed("no-parent") && !noParent {
				return exitcode.Newf(exitcode.Usage, "--no-parent=false is not a choice: leave both flags out to keep the source's parent")
			}
			// PRESENCE, not value: --close-session=false must override a copied true
			// (an explicitly given flag sends its value, false included).
			if flags.Changed("close-session") && flags.Changed("no-close-session") {
				return exitcode.Newf(exitcode.Usage, "--close-session and --no-close-session contradict each other")
			}
			if flags.Changed("parent") && strings.TrimSpace(parent) == "" {
				return exitcode.Newf(exitcode.Usage, "--parent needs a task id or handle (--no-parent clones it with none)")
			}
			ref, err := taskRefArg(args)
			if err != nil {
				return err
			}
			input := gen.CloneAgentTaskInput{}
			if flags.Changed("title") {
				input.Title = &title
			}
			if flags.Changed("description") {
				input.Description = &description
			}
			if flags.Changed("source") {
				input.Source = &source
			}
			if flags.Changed("priority") {
				input.Priority = &priority
			}
			if flags.Changed("status") {
				switch strings.ToLower(status) {
				case "backlog":
					s := gen.AgentTaskCreateStatusBacklog
					input.Status = &s
				case "pending":
					s := gen.AgentTaskCreateStatusPending
					input.Status = &s
				default:
					return exitcode.Newf(exitcode.Usage, "--status is backlog or pending at creation (every other status is reached by a move), got %q", status)
				}
			}
			// The same two validators `task create` uses for these two flags
			// (RFC 3339 for --start-by, the strict date-time for --finish-by), so a
			// value one command takes the other takes.
			if flags.Changed("start-by") {
				if _, err := time.Parse(time.RFC3339, startBy); err != nil {
					return exitcode.Newf(exitcode.Usage, "--start-by takes an RFC 3339 timestamp, e.g. 2026-10-13T09:00:00Z, got %q", startBy)
				}
				input.StartBy = &startBy
			}
			if flags.Changed("finish-by") {
				if _, ok := cmdutil.ParseStrictDateTime(finishBy); !ok {
					return exitcode.Newf(exitcode.Usage, "--finish-by takes an RFC 3339 timestamp, e.g. 2099-11-02T09:00:00Z, got %q", finishBy)
				}
				input.FinishBy = &finishBy
			}
			if flags.Changed("assignee") {
				party, err := parseTaskPartyFlag("--assignee", assignee)
				if err != nil {
					return err
				}
				input.Assignee = party
			}
			if flags.Changed("close-session") {
				input.CloseSessionWhenDone = boolPtr(closeSession)
			} else if flags.Changed("no-close-session") {
				input.CloseSessionWhenDone = boolPtr(!noCloseSession)
			}
			// parentTaskId is three-state: omitted keeps the source's parent, an
			// id names another, an explicit null means none.
			switch {
			case noParent:
				input.ParentTaskId = json.RawMessage("null")
			case flags.Changed("parent"):
				id, _ := json.Marshal(strings.TrimSpace(parent)) // a string cannot fail JSON encoding
				input.ParentTaskId = id
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			mctx, sink := api.WithWarningSink(cmd.Context())
			resp, err := gen.AgentTaskClone(mctx, client, ref, &input)
			if err != nil {
				return api.WithServerCode(mapTaskError(err), err)
			}
			task := taskDTOFrom(resp.CloneAgentTask.AgentTaskFields)
			return writeTaskMutation(f, task, taskWarningsFor(sink, "cloneAgentTask"), func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ cloned task %s as %s (%s)\n", output.SafeCell(ref), taskRef(task.Handle, task.ID), task.Status); err != nil {
					return err
				}
				if task.Queued {
					if _, err := fmt.Fprintln(w, "  queued: no assignee — the platform's matcher picks one"); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(w, "  app: %s\n", output.SafeCell(task.AppID)); err != nil {
					return err
				}
				return writeCloseSessionLine(w, task)
			})
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "replace the copied title")
	cmd.Flags().StringVar(&description, "description", "", "replace the copied description")
	cmd.Flags().StringVar(&source, "source", "", "replace the copied source reference")
	// Zero suppresses a fixed help default; only a Changed flag is sent.
	cmd.Flags().IntVar(&priority, "priority", 0, "matching priority: 1-5, 1 = highest (omitted: inherit the source priority)")
	cmd.Flags().StringVar(&status, "status", "", "the clone's create-time status: backlog or pending (the default)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "party to assign the clone to (worker:<…> / user:<…>)")
	cmd.Flags().StringVar(&startBy, "start-by", "", "RFC 3339 deadline to start by")
	cmd.Flags().StringVar(&finishBy, "finish-by", "", "RFC 3339 deadline to finish by")
	cmd.Flags().StringVar(&parent, "parent", "", "another parent task (same App) instead of the source's")
	cmd.Flags().BoolVar(&noParent, "no-parent", false, "no parent: a root under AT0 (sends parentTaskId null)")
	cmd.Flags().BoolVar(&closeSession, "close-session", false, "finishing or cancelling asks the assignee to end its worker session")
	cmd.Flags().BoolVar(&noCloseSession, "no-close-session", false, "finishing or cancelling does not ask the assignee to end its worker session")
	return cmd
}
