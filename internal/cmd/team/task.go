package team

// `hadron team task` — the Task Management epic's write surface
// (hadron-cli#830, T4; server #1705). Every verb is a thin caller over one
// server mutation; the actor is whoever the server sees: a bound worktree
// writes as its worker (the X-Hadron-Session header the authored-write
// middleware adds — createAgentTask & co. carry no sessionRef argument), and
// a person writes as themselves over plain GraphQL.

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// taskAssignerDoc is the assigner authority (cor:agt:050:09) every
// assigner-only page states, kept in one place so the pages cannot drift.
const taskAssignerDoc = `the task's owner, a worker the owner reports to
(directly or further up the reportsTo chain), or a person who
manages the App — a worker session never counts as managing the App`

func newCmdTeamTask(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task <command>",
		Short: "A team App's agent tasks",
		Long: `Create, assign, carry and finish the team App's agent tasks. The server
owns the lifecycle, the matcher and every permission check; each lifecycle
write sends one mutation and prints the task the server returns, and each read
prints the view its page describes.

A typical flow, from the person or worker handing out the work to the
assignee working in its bound worktree (the handles here and on every page are
illustrative: use the one create, list or mine prints):
  hadron team task create "Fix login" --app acme.com:eng-team --assignee worker:Iris --source https://github.com/acme/app/issues/7
  hadron team task mine                        # as Iris: the PENDING and STARTED tasks
  hadron team task start AT0.7
  hadron team task progress AT0.7              # while working; carries no text
  hadron team task finish AT0.7 --outcome succeeded --evidence https://github.com/acme/app/pull/9

Statuses: BACKLOG (parked), PENDING (queued with no assignee, or assigned and
not yet started), STARTED, then FINISHED or CANCELLED, which are end states.
Who may do what, from which status (cor:agt:050:08-:09):
  start                     PENDING with an assignee, not blocked   the assignee
  progress, finish          STARTED                                 the assignee
  create --parent           (a new child task)                      the parent's assignee
  assign                    PENDING or BACKLOG with no assignee     an assigner
  reassign                  any open task with an assignee          an assigner
  park                      PENDING with no assignee                an assigner
  ready                     BACKLOG                                 an assigner
  cancel                    BACKLOG, PENDING or STARTED             an assigner
  set-priority              BACKLOG, PENDING or STARTED             an assigner
  add-/remove-blocker       BACKLOG or PENDING                      an assigner
An assigner is ` + taskAssignerDoc + `.
Anyone in the App may create a root task.
Lacking the authority exits 8 (TASK_NOT_OWNER for an assigner's move); a
status that does not allow the move exits 5 (TASK_INVALID_TRANSITION, or "an
ended task never moves again"). Run ` + "`team task get <task>`" + ` to see the status,
parties and blockers before you retry.

Who acts: with a worktree worker-session binding, writes are made by the bound
WORKER (the server records the session); without one, by you, the signed-in
person. The App: create and list use --app, else the configured App context,
else the worktree binding; commands that take a task id or handle work out the
App from the task. A task is named by its id or its handle (AT0.7: AT0 holds
tasks outside any project, AT1, AT2, … are projects, AT1.2 is a task under AT1);
when a bare handle matches tasks in more than one of your Apps, qualify it as
hrn:agenttask:acme.com:eng-team:AT1.2.

A party is named worker:<id, URN or name> or user:<id or handle> — the kind
prefix is required: a worker's name and a person's handle are both bare
words, so a bare value is ambiguous and refused rather than guessed.

Stalled work raises alarms: a PENDING assigned task not started in time, a
STARTED task with no progress, an owner who did not react to a finished child,
a queued task nobody can take. ` + "`team stall-schedule get`" + ` shows the App's
windows, ` + "`team events --alarms`" + ` lists the alarms and ` + "`team alarm ack`" + `
acknowledges one.`,
	}
	cmd.AddCommand(newCmdTeamTaskCreate(f))
	for _, verb := range []string{"set-priority", "add-blocker", "remove-blocker"} {
		cmd.AddCommand(newCmdTaskDependencyWrite(f, verb))
	}
	cmd.AddCommand(newCmdTaskDependencies(f))
	cmd.AddCommand(newCmdTeamTaskAssign(f, "assign", "Assign a task that has no assignee", false))
	cmd.AddCommand(newCmdTeamTaskAssign(f, "reassign", "Move a task to a different assignee", true))
	cmd.AddCommand(newCmdTeamTaskStart(f))
	cmd.AddCommand(newCmdTeamTaskProgress(f))
	cmd.AddCommand(newCmdTeamTaskFinish(f))
	cmd.AddCommand(newCmdTeamTaskCancel(f))
	cmd.AddCommand(newCmdTeamTaskPark(f))
	cmd.AddCommand(newCmdTeamTaskReady(f))
	cmd.AddCommand(newCmdTeamTaskGet(f))
	cmd.AddCommand(newCmdTeamTaskList(f))
	cmd.AddCommand(newCmdTeamTaskMine(f))
	cmd.AddCommand(newCmdTeamTaskTree(f))
	return cmd
}

// parseTaskPartyFlag turns worker:<ref> / user:<ref> into the input the
// server takes. A bare value is refused: both kinds are bare words, and the
// wrong guess routes the task to the wrong party with no way back.
func parseTaskPartyFlag(flag, value string) (*gen.AgentTaskPartyInput, error) {
	kind, ref, ok := strings.Cut(value, ":")
	if ok && ref != "" {
		switch strings.ToLower(kind) {
		case "worker":
			return &gen.AgentTaskPartyInput{Worker: &ref}, nil
		case "user":
			return &gen.AgentTaskPartyInput{User: &ref}, nil
		}
	}
	return nil, exitcode.Newf(exitcode.Usage,
		"%s names a party as worker:<id, URN or name> or user:<id or handle> — the kind prefix is required (a worker name and a user handle are both bare words), got %q",
		flag, value)
}

// taskRefArg validates the one positional task id every task-id verb takes.
// The TRIMMED form is both validated and returned (cycle 2, Xylon P3-1): a
// pasted id with surrounding whitespace used to be sent verbatim and fail as
// NOT_FOUND for a reason the caller never typed.
func taskRefArg(args []string) (string, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return "", exitcode.Newf(exitcode.Usage, "pass the task id shown by `team task list`/`get`")
	}
	return strings.TrimSpace(args[0]), nil
}

// taskOutcomeRe is the one-word outcome contract (cor:agt:050:01): the
// event stream copies it verbatim, so it excludes everything else.
var taskOutcomeRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// writeTaskReceipt prints the one-line mutation receipt plus the assignee,
// when there is one.
func writeTaskReceipt(w io.Writer, verb string, task taskDTO) error {
	if _, err := fmt.Fprintf(w, "✓ %s task %s (%s)\n", verb, taskRef(task.Handle, task.ID), task.Status); err != nil {
		return err
	}
	if task.Assignee != nil && task.Assignee.Name != nil {
		_, err := fmt.Fprintf(w, "  assignee: %s\n", *task.Assignee.Name)
		return err
	}
	return nil
}

// writeCloseSessionLine shows the task's effective close-when-done choice
// (cor:agt:050:10) as the server returned it — never the flag the caller
// passed, so a default or a server-side override reads truthfully. Only yes
// explains itself — what finishing will REQUEST; the platform, not this
// command, ends the session, and only through a live host ("may"). The spec
// cite lives in the help, not at runtime (#979 UX P3s).
func writeCloseSessionLine(w io.Writer, task taskDTO) error {
	line := "  close session when done: no\n"
	if task.CloseSessionWhenDone {
		line = "  close session when done: yes — finishing or cancelling asks the assignee to end its worker session; the platform may end it after a grace period\n"
	}
	_, err := io.WriteString(w, line)
	return err
}

// ---- create ---------------------------------------------------------------

func newCmdTeamTaskCreate(f *cmdutil.Factory) *cobra.Command {
	var description, owner, assignee, parent, source, startBy, status string
	var tags, blockedBy []string
	var priority int
	var requireRole, familyIn, familyNotIn string
	var excludeWorkers []string
	var closeSession, noCloseSession, project bool
	cmd := &cobra.Command{
		Use:   "create <title>",
		Short: "Create a task",
		Long: `Create an agent task in the team App. Anyone in the App may create a root
task; only the parent's assignee may create a child task (--parent). The App
comes from --app, else the configured App context, else the worktree binding;
a child task takes its App from the parent (an --app that disagrees is
refused). Pass --source (an issue URL or node URN) so readers can trace the
task: without one the server adds the warning task.create.no-source.

Who goes on it:
- --assignee worker:<name> or user:<handle> puts it straight in that party's
  queue as PENDING. They start it with ` + "`task start`" + `.
- No --assignee leaves it QUEUED (PENDING, no assignee): the platform's matcher
  picks a worker. Requirements narrow its choice: --require-role <role> (the
  App's roles: ` + "`team role list`" + `), --family-in or --family-not-in a comma
  list of model families such as claude,codex (` + "`team availability`" + `), and
  --exclude-worker <name, URN or id>, repeatable. Among queued tasks the
  matcher offers priority 1 first, then the oldest.
- --status backlog parks it instead: no assignee (the server refuses both),
  never matched, no alarms, until ` + "`task assign`" + ` or ` + "`task ready`" + `
  moves it to PENDING. --status pending is the default.

--owner defaults to you (the bound worker, or you as a person when no session
is bound); a root task's creator may name any other participating worker or
person (#1757, cor:agt:050:01). The owner, a worker the owner reports to and a
person who manages the App may then assign, reassign, park, cancel, prioritize
and block it. For a child task the owner is the parent's assignee — naming a
different owner is refused (TASK_CHILD_OWNER_MISMATCH).

Where it lands: without --parent the task goes under AT0, the reserved
"no project", as AT0.<n>. --project makes a top-level project AT<n> instead.
--parent <task> makes a child (AT1.<n> under the project AT1, AT1.2.<n> under
AT1.2); the parent needs an assignee first, and that assignee creates the
children. --blocked-by <task> makes it wait for another task (see
` + "`task add-blocker`" + `).

--start-by <RFC 3339 time> sets when the task should be started. Once it passes,
` + "`task list --overdue`" + ` lists the task while it is still pending, whether or
not it is assigned or blocked; that is a filter, not an alarm. The not-started
stall alarm is separate (cor:agt:050:14): it fires only for an assigned,
unblocked PENDING task, and its clock starts at the assignment, or at the
release if the task was blocked. The deadline is the start-by when that falls
after the clock starts; otherwise, and with no --start-by, it is the App's
not-started window (` + "`team stall-schedule get`" + `) counted from then. So a
task released or reassigned after its start-by gets a fresh window.

By default finishing or cancelling does not ask the assignee to end its
worker session. Pass --close-session to ask (cor:agt:050:10); the platform
may end it through the worker's live host after a grace period, 10 minutes by
default, set per App, and declining the request does not prevent that.
--no-close-session states the default explicitly. The choice cannot be
changed in place afterwards, so when the assignee's session should end with
the task, pass --close-session at create, or on the assign that first names
the assignee of a queued task.

Success prints the task's handle, id and status, "queued" when it has no
assignee, the App and the close-session choice; --json returns the task with a
warnings list.`,
		Example: `  # assign to a named worker, with a deadline to start
  hadron team task create "Fix login redirect" --app acme.com:eng-team --assignee worker:Iris \
    --priority 2 --start-by 2026-11-02T09:00:00Z --source https://github.com/acme/app/issues/7

  # queue it for any QA engineer not running as codex
  hadron team task create "Retest the export fix" --app acme.com:eng-team \
    --require-role qa-engineer --family-not-in codex --description "Run the export suite on staging"

  # park an idea in the backlog
  hadron team task create "Tidy the release notes" --app acme.com:eng-team --status backlog

  # a project, then (as its assignee) a child under it
  hadron team task create "Q4 billing" --app acme.com:eng-team --project --assignee worker:Iris
  hadron team task create "Draft the invoice schema" --parent AT1 --assignee worker:Uma`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			title := strings.TrimSpace(args[0])
			if title == "" {
				return exitcode.Newf(exitcode.Usage, "the title is empty")
			}
			if startBy != "" {
				if _, err := time.Parse(time.RFC3339, startBy); err != nil {
					return exitcode.Newf(exitcode.Usage, "--start-by takes an RFC 3339 timestamp, e.g. 2026-10-13T09:00:00Z, got %q", startBy)
				}
			}
			if familyIn != "" && familyNotIn != "" {
				return exitcode.Newf(exitcode.Usage, "--family-in and --family-not-in: give one, not both")
			}
			// The two create-time statuses (cor:agt:050:02); every other
			// status is reached by a move, so the flag cannot spell them.
			// PRESENCE, not value: an explicit --status "" (a script's
			// --status "$VAR" with an unset VAR) is a supplied value outside
			// the two, refused here rather than silently queued — the
			// an-empty-flag-is-not-an-absent-flag rule (cycle 0, Anne P2-1).
			var createStatus *gen.AgentTaskCreateStatus
			if cmd.Flags().Changed("status") {
				switch strings.ToLower(status) {
				case "backlog":
					s := gen.AgentTaskCreateStatusBacklog
					createStatus = &s
				case "pending":
					s := gen.AgentTaskCreateStatusPending
					createStatus = &s
				default:
					return exitcode.Newf(exitcode.Usage, "--status is backlog or pending at creation (every other status is reached by a move), got %q", status)
				}
			}
			var priorityArg *int
			if cmd.Flags().Changed("priority") {
				priorityArg = &priority
			}
			for i, v := range blockedBy {
				blockedBy[i] = strings.TrimSpace(v)
				if blockedBy[i] == "" {
					return exitcode.Newf(exitcode.Usage, "--blocked-by must name a task")
				}
			}
			input := gen.CreateAgentTaskInput{Priority: priorityArg, BlockedBy: blockedBy,
				Title:        title,
				Description:  optStr(description),
				ParentTaskId: optStr(parent),
				Source:       optStr(source),
				StartBy:      optStr(startBy),
				Status:       createStatus,
				Tags:         tags,
			}
			// Omitted unless the flag is given: the server reads an absent
			// project as "no project" (AT0) and an explicit false the same, so
			// presence, not value, is what is sent (cor:agt:050:01).
			if cmd.Flags().Changed("project") {
				input.Project = &project
			}
			if closeSession && noCloseSession {
				return exitcode.Newf(exitcode.Usage, "--close-session and --no-close-session contradict each other")
			}
			if closeSession {
				input.CloseSessionWhenDone = boolPtr(true)
			} else if noCloseSession {
				input.CloseSessionWhenDone = boolPtr(false)
			}
			if assignee != "" {
				party, err := parseTaskPartyFlag("--assignee", assignee)
				if err != nil {
					return err
				}
				input.Assignee = party
			}
			if owner != "" {
				party, err := parseTaskPartyFlag("--owner", owner)
				if err != nil {
					return err
				}
				input.Owner = party
			}
			if requireRole != "" || familyIn != "" || familyNotIn != "" || len(excludeWorkers) > 0 {
				input.Requirements = &gen.AgentTaskRequirementsInput{
					ExcludeWorkers: excludeWorkers,
					FamilyIn:       commaList(familyIn),
					FamilyNotIn:    commaList(familyNotIn),
					Role:           optStr(requireRole),
				}
			}
			// The App: a root task needs one; a child task may take it from
			// the parent. resolveTeamAppScope's refusal names the remedies.
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			var appLabel func() string
			if scope, err := resolveTeamAppScope(ctx, f, b); err == nil {
				// A root task needs the App; a child takes it from the
				// parent when this does not resolve — so the refusal below
				// is skipped for children, deliberately.
				input.AppRef = &scope.Ref
				appLabel = lazyAppLabel(ctx, f, scope)
			} else if parent == "" {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			mctx, sink := api.WithWarningSink(ctx)
			resp, err := gen.AgentTaskCreate(mctx, client, &input)
			if err != nil {
				return mapTaskError(err)
			}
			task := taskDTOFrom(resp.CreateAgentTask.AgentTaskFields)
			return writeTaskMutation(f, task, taskWarningsFor(sink, "createAgentTask"), func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ created task %s (%s)\n", taskRef(task.Handle, task.ID), task.Status); err != nil {
					return err
				}
				if task.Queued {
					if _, err := fmt.Fprintln(w, "  queued: no assignee — the platform's matcher picks one"); err != nil {
						return err
					}
				}
				// The receipt names where the task landed, in readable form
				// when the scope was ours to resolve (#470), else the id the
				// parent implied.
				app := task.AppID
				if appLabel != nil {
					app = appLabel()
				}
				if _, err := fmt.Fprintf(w, "  app: %s\n", app); err != nil {
					return err
				}
				return writeCloseSessionLine(w, task)
			})
		},
	}
	cmd.Flags().IntVar(&priority, "priority", 1, "matching priority: 1-5, 1 = highest")
	cmd.Flags().StringArrayVar(&blockedBy, "blocked-by", nil, "task id or handle that blocks this task (repeatable; same-App/cycle rules are server-owned)")
	cmd.Flags().StringVar(&description, "description", "", "what the task asks for")
	cmd.Flags().StringVar(&assignee, "assignee", "", "party to assign it to (worker:<…> / user:<…>); omit to leave it queued")
	cmd.Flags().StringVar(&owner, "owner", "", "task owner (defaults to you)")
	cmd.Flags().StringVar(&parent, "parent", "", "parent task id (child task)")
	cmd.Flags().StringVar(&startBy, "start-by", "", "RFC 3339 deadline to start by")
	cmd.Flags().StringVar(&source, "source", "", "origin reference (an issue URL, a node URN)")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tag (repeatable)")
	cmd.Flags().StringVar(&requireRole, "require-role", "", "matcher must pick this role")
	cmd.Flags().StringVar(&familyIn, "family-in", "", "comma list of model families the assignee must run as")
	cmd.Flags().StringVar(&familyNotIn, "family-not-in", "", "comma list of model families to exclude")
	cmd.Flags().StringSliceVar(&excludeWorkers, "exclude-worker", nil, "worker (name, URN or id) the matcher must not pick (repeatable)")
	cmd.Flags().BoolVar(&project, "project", false, "make a top-level project (AT1, AT2, …) instead of a task under the reserved project AT0 (\"no project\"); not with --parent")
	cmd.Flags().StringVar(&status, "status", "", "create-time status: backlog (parked) or pending (the default)")
	cmd.Flags().BoolVar(&closeSession, "close-session", false, "finishing or cancelling asks the assignee to end its worker session")
	cmd.Flags().BoolVar(&noCloseSession, "no-close-session", false, "finishing or cancelling does not ask the assignee to end its worker session (the default)")
	return cmd
}

// ---- assign / reassign ----------------------------------------------------

func newCmdTeamTaskAssign(f *cmdutil.Factory, verb string, short string, reassign bool) *cobra.Command {
	var assignee string
	var closeSession, noCloseSession bool
	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s <taskId> --assignee <party>", verb),
		Short: short,
		Long:  assignLong(verb),
		Example: map[bool]string{
			false: "  hadron team task assign AT0.7 --assignee worker:Iris\n  hadron team task assign AT0.7 --assignee worker:Iris --close-session",
			true:  "  hadron team task reassign AT0.7 --assignee worker:Uma",
		}[reassign],
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			taskId, err := taskRefArg(args)
			if err != nil {
				return err
			}
			if assignee == "" {
				return exitcode.Newf(exitcode.Usage, "--assignee is required (worker:<…> / user:<…>)")
			}
			party, err := parseTaskPartyFlag("--assignee", assignee)
			if err != nil {
				return err
			}
			var closeWhenDone *bool
			if closeSession && noCloseSession {
				return exitcode.Newf(exitcode.Usage, "--close-session and --no-close-session contradict each other")
			}
			if closeSession {
				closeWhenDone = boolPtr(true)
			} else if noCloseSession {
				closeWhenDone = boolPtr(false)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var task taskDTO
			mctx, sink := api.WithWarningSink(ctx)
			key := "assignAgentTask"
			if reassign {
				key = "reassignAgentTask"
				resp, err := gen.AgentTaskReassign(mctx, client, taskId, party, closeWhenDone)
				if err != nil {
					return mapTaskError(err)
				}
				task = taskDTOFrom(resp.ReassignAgentTask.AgentTaskFields)
			} else {
				resp, err := gen.AgentTaskAssign(mctx, client, taskId, party, closeWhenDone)
				if err != nil {
					return mapTaskError(err)
				}
				task = taskDTOFrom(resp.AssignAgentTask.AgentTaskFields)
			}
			return writeTaskMutation(f, task, taskWarningsFor(sink, key), func(w io.Writer) error {
				if err := writeTaskReceipt(w, verb+"ed", task); err != nil {
					return err
				}
				return writeCloseSessionLine(w, task)
			})
		},
	}
	cmd.Flags().StringVar(&assignee, "assignee", "", "party to assign the task to (worker:<…> / user:<…>)")
	cmd.Flags().BoolVar(&closeSession, "close-session", false, "finishing or cancelling asks the assignee to end its worker session")
	cmd.Flags().BoolVar(&noCloseSession, "no-close-session", false, "finishing or cancelling does not ask the assignee to end its worker session")
	return cmd
}

// assignLong is the assign/reassign page: what each verb accepts, who may
// run it (cor:agt:050:09), then the shared close-when-done paragraph.
func assignLong(verb string) string {
	head := `Assign a task that has no assignee: a PENDING task waiting in the queue, or
a BACKLOG task, which assigning moves to PENDING. The assignee then starts it
with ` + "`task start`" + `. A task that already has an assignee is refused
(TASK_INVALID_TRANSITION, exit 5): use ` + "`task reassign`" + `.`
	if verb == "reassign" {
		head = `Move a task that has an assignee, PENDING or STARTED, to a different one. A
STARTED task stays STARTED: the new assignee carries on with progress and
finish, and the former assignee can no longer move it. A task with no
assignee is refused (TASK_INVALID_TRANSITION, exit 5): use ` + "`task assign`" + `.`
	}
	return head + `

Who may: ` + taskAssignerDoc + ` (cor:agt:050:09).
Anyone else is refused with TASK_NOT_OWNER (exit 8). An ended task is refused
(exit 5). --assignee is required (worker:<id, URN or name> or
user:<id or handle>). The App comes from the task.

The server may add a warning that does not block the assignment, for example
when the assignee is unavailable, busy or paused (task.assign.assignee-*).
Success prints the task, its status, the assignee and the close-session
choice; --json returns the task with a warnings list.

--close-session/--no-close-session replace the task's close-when-done choice
(cor:agt:050:10); with neither, the task keeps its current value. Give at most
one: both together is a usage error, and ` + "`--close-session=false`" + ` is
indistinguishable from absence here and also means "keep the current value".
The flags ride on a real assignment: there is no in-place setter, and
reassigning a task to its current assignee is refused (TASK_INVALID_TRANSITION)
with the choice unchanged. To have the assignee's session end with the task,
pass --close-session at create, or on the assign that first names the assignee
of a queued task. The default of not asking applies at creation; assign and
reassign with neither flag keep the value the task already has. What the
request does is described under ` + "`task create --help`" + `.`
}

// ---- start / progress / cancel -------------------------------------------

// newSimpleTaskMutation builds a one-argument verb whose whole body is one
// mutation returning the task; receipt renders its human line. key is the
// mutation's response field, under which its warnings are filed (cli#1283).
func newSimpleTaskMutation(
	f *cmdutil.Factory,
	verb, key, short, long, example string,
	call func(context.Context, graphql.Client, string) (taskDTO, error),
	receipt func(io.Writer, taskDTO) error,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:     fmt.Sprintf("%s <taskId>", verb),
		Short:   short,
		Long:    long,
		Example: example,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskId, err := taskRefArg(args)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			mctx, sink := api.WithWarningSink(cmd.Context())
			task, err := call(mctx, client, taskId)
			if err != nil {
				return mapTaskError(err)
			}
			return writeTaskMutation(f, task, taskWarningsFor(sink, key), func(w io.Writer) error {
				return receipt(w, task)
			})
		},
	}
	return cmd
}

func newCmdTeamTaskStart(f *cmdutil.Factory) *cobra.Command {
	return newSimpleTaskMutation(f, "start", "startAgentTask", "Start a pending task",
		`Start a task assigned to you: PENDING becomes STARTED and the server records
task.started. Only the assignee may start it (cor:agt:050:08) — with a
worktree binding that is the bound worker. Find yours with `+"`task mine`"+`.

Refusals: someone else's task exits 8. A task that is already started is
TASK_INVALID_TRANSITION (exit 5). A task with an open blocker is TASK_BLOCKED
(exit 5): `+"`task dependencies <task>`"+` shows what it waits for, and it
becomes startable when that finishes or is cancelled. A queued or BACKLOG task
has no assignee to start it: someone with assigner authority runs
`+"`task assign`"+` first (see `+"`team task --help`"+`).

Once started, record `+"`task progress`"+` while you work and `+"`task finish`"+`
when done. Success prints the task, STARTED and the assignee; --json returns
the task with a warnings list, and a server warning also prints on stderr.`,
		"  hadron team task mine\n  hadron team task start AT0.7",
		func(ctx context.Context, client graphql.Client, taskId string) (taskDTO, error) {
			resp, err := gen.AgentTaskStart(ctx, client, taskId)
			if err != nil {
				return taskDTO{}, err
			}
			return taskDTOFrom(resp.StartAgentTask.AgentTaskFields), nil
		},
		func(w io.Writer, task taskDTO) error {
			return writeTaskReceipt(w, "started", task)
		})
}

func newCmdTeamTaskProgress(f *cmdutil.Factory) *cobra.Command {
	return newSimpleTaskMutation(f, "progress", "recordAgentTaskProgress", "Record progress on a started task",
		`Record that you are still working on a STARTED task: its assignee only
(cor:agt:050:08). It carries no text. It writes task.progress to the event
stream and resets the no-progress stall alarm's clock (cor:agt:050:15), which
fires when a STARTED task with no open child sees no activity from its
assignee for the App's window (`+"`team stall-schedule get`"+`; alarms in
`+"`team events --alarms`"+`).

To leave a written account, put it in the result: `+"`task finish --summary`"+`
and --evidence (a PR, an issue comment or a node URN). There is no free-text
progress note.

Refusals: a task that is not STARTED exits 5 ("Start the task before
recording progress"): run `+"`task start`"+` first. Someone else's task exits 8.
Success prints the task and STARTED; --json returns the task with a warnings
list, and a server warning also prints on stderr.`,
		"  hadron team task progress AT0.7",
		func(ctx context.Context, client graphql.Client, taskId string) (taskDTO, error) {
			resp, err := gen.AgentTaskProgress(ctx, client, taskId)
			if err != nil {
				return taskDTO{}, err
			}
			return taskDTOFrom(resp.RecordAgentTaskProgress.AgentTaskFields), nil
		},
		func(w io.Writer, task taskDTO) error {
			_, err := fmt.Fprintf(w, "✓ recorded progress on task %s (%s)\n", taskRef(task.Handle, task.ID), task.Status)
			return err
		})
}

func newCmdTeamTaskCancel(f *cmdutil.Factory) *cobra.Command {
	return newSimpleTaskMutation(f, "cancel", "cancelAgentTask", "Cancel a task",
		`Cancel a task that is no longer wanted, from BACKLOG, PENDING or STARTED.
CANCELLED is an end state: nobody can start, progress or finish it afterwards,
and it no longer blocks the tasks waiting for it. The command does not ask for
confirmation.

Who may: `+taskAssignerDoc+` (cor:agt:050:09);
anyone else is refused with TASK_NOT_OWNER (exit 8). The assignee finishes its
own work with `+"`task finish`"+` instead, and a task that should wait rather
than end is parked (`+"`task park`"+`, queued tasks only).

Cancelling a parent does not end its children: they stay open, each to be
cancelled or finished on its own, and the server adds the warning
task.cancel.open-children. If the task was created with
--close-session, cancelling asks the assignee to end its worker session, just
as finishing does (see `+"`task create --help`"+`). An already-ended task is
refused (exit 5). Success prints the task and CANCELLED. A warning prints on
stderr after the receipt and does not change the exit code; --json returns the
task with a warnings list ([] when none).`,
		"  hadron team task cancel AT0.7",
		func(ctx context.Context, client graphql.Client, taskId string) (taskDTO, error) {
			resp, err := gen.AgentTaskCancel(ctx, client, taskId)
			if err != nil {
				return taskDTO{}, err
			}
			return taskDTOFrom(resp.CancelAgentTask.AgentTaskFields), nil
		},
		func(w io.Writer, task taskDTO) error {
			return writeTaskReceipt(w, "cancelled", task)
		})
}

// ---- park / ready (the BACKLOG moves, #1754 / cor:agt:050:02) --------------

func newCmdTeamTaskPark(f *cmdutil.Factory) *cobra.Command {
	return newSimpleTaskMutation(f, "park", "parkAgentTask", "Park a queued task (BACKLOG)",
		`Park the task: a pending task with no assignee becomes BACKLOG —
unassigned, never matched, never waited on, no alarms (cor:agt:050:02).
A task that has an assignee, or is STARTED, is refused (exit 5); to stop
started work for good, cancel it. Make it ready (`+"`team task ready`"+`) — or
assign it — to move it back to PENDING. Success prints the task and BACKLOG;
--json returns the task with a warnings list, and a server warning also
prints on stderr.

Who may: `+taskAssignerDoc+` (cor:agt:050:09).`,
		"  hadron team task park AT0.7",
		func(ctx context.Context, client graphql.Client, taskId string) (taskDTO, error) {
			resp, err := gen.AgentTaskPark(ctx, client, taskId)
			if err != nil {
				return taskDTO{}, err
			}
			return taskDTOFrom(resp.ParkAgentTask.AgentTaskFields), nil
		},
		func(w io.Writer, task taskDTO) error {
			return writeTaskReceipt(w, "parked", task)
		})
}

func newCmdTeamTaskReady(f *cmdutil.Factory) *cobra.Command {
	return newSimpleTaskMutation(f, "ready", "makeAgentTaskReady", "Make a backlog task ready to work",
		`Make the backlog task ready: BACKLOG becomes PENDING with no assignee and the
task enters the queue, where the platform's matcher picks a worker
(cor:agt:050:02). To choose the worker yourself, `+"`task assign`"+` the
BACKLOG task instead. Success prints the task, PENDING, and "queued" while
no one has it; --json returns the task with a warnings list, and a server
warning also prints on stderr.

Who may: `+taskAssignerDoc+` (cor:agt:050:09).`,
		"  hadron team task ready AT0.7",
		func(ctx context.Context, client graphql.Client, taskId string) (taskDTO, error) {
			resp, err := gen.AgentTaskReady(ctx, client, taskId)
			if err != nil {
				return taskDTO{}, err
			}
			return taskDTOFrom(resp.MakeAgentTaskReady.AgentTaskFields), nil
		},
		func(w io.Writer, task taskDTO) error {
			_, err := fmt.Fprintf(w, "✓ made task %s ready (%s)\n", taskRef(task.Handle, task.ID), task.Status)
			if err == nil && task.Queued {
				_, err = fmt.Fprintln(w, "  queued: no assignee — the platform's matcher picks one")
			}
			return err
		})
}

// ---- finish ---------------------------------------------------------------

func newCmdTeamTaskFinish(f *cmdutil.Factory) *cobra.Command {
	var outcome, summary, evidence string
	cmd := &cobra.Command{
		Use:   "finish <taskId> --outcome <word>",
		Short: "Finish a started task with its result",
		Long: `Finish a STARTED task with its result: its assignee only (cor:agt:050:08).
FINISHED is an end state, and the tasks it blocked stop waiting for it.

--outcome is one word, up to 64 letters, digits, underscores, hyphens or dots,
e.g. succeeded or failed. --summary (one line) and --evidence (a URL or node
URN) are optional; they are the place for a written account of the work.

Refusals: a PENDING task exits 5 ("start it first"): run ` + "`task start`" + `,
then finish. Someone else's task exits 8; an ended task exits 5. Open child
tasks do not stop it: the server adds the warning task.finish.open-children
and the children stay open. If the task was created with --close-session,
finishing asks the assignee to end its worker session (see ` + "`task create --help`" + `);
the command itself ends nothing.

Success prints the task, the outcome and the summary; --json returns the task
with its result and a warnings list.`,
		Example: `  hadron team task finish AT0.7 --outcome succeeded --summary "Login redirect fixed" --evidence https://github.com/acme/app/pull/9
  hadron team task finish AT0.8 --outcome failed --summary "Cannot reproduce on staging"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			taskId, err := taskRefArg(args)
			if err != nil {
				return err
			}
			if strings.TrimSpace(outcome) == "" {
				return exitcode.Newf(exitcode.Usage, "--outcome is required — one word, e.g. succeeded or failed")
			}
			// The advertised charset, enforced (cycle 2, Xylon P3-2): the
			// help and the server's own input doc promise letters, digits,
			// underscore, hyphen or dot up to 64 characters — the event
			// stream never holds free text, so neither does this word.
			if !taskOutcomeRe.MatchString(outcome) {
				return exitcode.Newf(exitcode.Usage, "--outcome is one word of letters, digits, underscore, hyphen or dot (up to 64 characters), got %q", outcome)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			result := gen.AgentTaskResultInput{
				Outcome:  outcome,
				Summary:  optStr(summary),
				Evidence: optStr(evidence),
			}
			mctx, sink := api.WithWarningSink(ctx)
			resp, err := gen.AgentTaskFinish(mctx, client, taskId, &result)
			if err != nil {
				return mapTaskError(err)
			}
			task := taskDTOFrom(resp.FinishAgentTask.AgentTaskFields)
			return writeTaskMutation(f, task, taskWarningsFor(sink, "finishAgentTask"), func(w io.Writer) error {
				out := ""
				if task.Result != nil {
					out = task.Result.Outcome
				}
				if _, err := fmt.Fprintf(w, "✓ finished task %s (%s)\n", taskRef(task.Handle, task.ID), out); err != nil {
					return err
				}
				if task.Result != nil && task.Result.Summary != nil {
					_, err := fmt.Fprintf(w, "  %s\n", *task.Result.Summary)
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&outcome, "outcome", "", "one-word result, e.g. succeeded or failed (required)")
	cmd.Flags().StringVar(&summary, "summary", "", "one-line result summary")
	cmd.Flags().StringVar(&evidence, "evidence", "", "URL or node URN backing the result")
	return cmd
}

// commaList splits "a,b,c" into [a b c]; empty stays empty.
func commaList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
