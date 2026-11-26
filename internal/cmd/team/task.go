package team

// `hadron team task` — the Task Management epic's write surface
// (hadron-cli#830, T4; server #1705). Every verb is a thin caller over one
// server mutation; the actor is whoever the server sees: a bound worktree
// writes as its worker (the X-Hadron-Session header the authored-write
// middleware adds — createAgentTask & co. carry no sessionRef argument), and
// a person writes as themselves over plain GraphQL.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
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
  hadron team task start AT0.7                 # AT0.7 is a task handle, explained below
  hadron team task progress AT0.7              # while working; carries no text
  hadron team task finish AT0.7 --outcome succeeded --evidence https://github.com/acme/app/pull/9

Statuses: BACKLOG (parked), PENDING (queued with no assignee, or assigned and
not yet started), STARTED, then FINISHED or CANCELLED, which are end states.
Who may do what, from which status:
  start                     PENDING with an assignee, not blocked   the assignee
  progress, finish          STARTED                                 the assignee
  create --parent           (a new child task)                      the parent's assignee
  assign                    PENDING or BACKLOG with no assignee     an assigner
  reassign                  any open task with an assignee          an assigner
  park                      PENDING with no assignee                an assigner
  ready                     BACKLOG                                 an assigner
  cancel                    BACKLOG, PENDING or STARTED             an assigner
  update                    BACKLOG, PENDING or STARTED             an assigner
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
else the worktree binding; commands that take a task id, a qualified handle or
an unambiguous handle work out the App from it. A task is named by its id or its handle (AT0.7: AT0 holds
tasks outside any project, AT1, AT2, … are projects, AT1.2 is a task under AT1);
if you belong to more than one App, a bare handle is refused: pass --app <app>,
or qualify it as hrn:agenttask:acme.com:eng-team:AT1.2. --app qualifies a bare
handle on every command (in every task argument and in --parent/--blocked-by);
it must be the App's URN or <root>:<slug>, not an App id. A task id, or a qualified
handle in that same App, is sent unchanged; one naming another App is refused.

A party is named worker:<id, URN or name> or user:<id or handle> — the kind
prefix is required: a worker's name and a person's handle are both bare
words, so a bare value is ambiguous and refused rather than guessed.

A STARTED task that is stuck cannot be parked (park is for PENDING tasks with no
assignee): reassign it to another worker, have its assignee hand it back, or
cancel it (or finish it) and create a new task.

Stalled work raises alarms: a PENDING assigned task not started in time, a
STARTED task with no progress, an owner who did not react to a finished child,
a queued task nobody can take. ` + "`team stall-schedule get`" + ` shows the App's
windows, ` + "`team events --alarms`" + ` lists the alarms and ` + "`team alarm ack`" + `
acknowledges one.

Findings: a review or QA seat records what it found when it finishes, and the
owner records what became of each one.
  hadron team task finish <seat-task> --outcome approved --findings-file findings.json
  hadron team task findings <seat-task>                 # read them (immutable once finished)
  hadron team task dispose-finding <seat-task> F1 --disposition fixed --ref <kind>=<target>
  hadron team task finding-dispositions <seat-task>     # list the latest disposition of each

For AI workers: --instruction <kind>:<ref> is on every single-task write (a batch
create takes none). The holder is the person responsible for the worker; the flag
claims, for that one call, that the holder told the worker to make this change.
A person, or a worker acting on its own authority, leaves it out. The full rules:
` + taskInstructionHelp + `

Server needs: task-record commands (every command that returns a task) require a
server released on or after 2026-11-17; an older server fails each of them with
exit 2 (schema skew), even when you pass no model-group flag (--model-group,
--model-group-not). ` + "`task dependencies`" + ` runs its own query, which reads
only each task's id, handle, title and status, so an older server answers it. Ask the
server's operator when it was released (` + "`hadron server-info`" + ` shows only that it
answers). Each page below says what its command takes.`,
	}
	cmd.AddCommand(newCmdTeamTaskCreate(f))
	cmd.AddCommand(newCmdTeamTaskClone(f))
	cmd.AddCommand(newCmdTaskFindingDispositions(f))
	cmd.AddCommand(newCmdTaskResultFindings(f))
	cmd.AddCommand(newCmdTaskDisposeFinding(f))
	cmd.AddCommand(newCmdTaskLinkDefect(f))
	for _, verb := range []string{"set-priority", "add-blocker", "remove-blocker"} {
		cmd.AddCommand(newCmdTaskDependencyWrite(f, verb))
	}
	cmd.AddCommand(newCmdTaskDependencies(f))
	cmd.AddCommand(newCmdTaskFindUntracked(f))
	cmd.AddCommand(newCmdTeamTaskAssign(f, "assign", "Assign a task that has no assignee", false))
	cmd.AddCommand(newCmdTeamTaskAssign(f, "reassign", "Move a task to a different assignee", true))
	cmd.AddCommand(newCmdTeamTaskStart(f))
	cmd.AddCommand(newCmdTeamTaskProgress(f))
	cmd.AddCommand(newCmdTeamTaskFinish(f))
	cmd.AddCommand(newCmdTeamTaskHandBack(f))
	cmd.AddCommand(newCmdTeamTaskCancel(f))
	cmd.AddCommand(newCmdTeamTaskRelease(f))
	cmd.AddCommand(newCmdTeamTaskPark(f))
	cmd.AddCommand(newCmdTeamTaskReady(f))
	cmd.AddCommand(newCmdTeamTaskGet(f))
	cmd.AddCommand(newCmdTeamTaskUpdate(f))
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
		"%s names a party as worker:<id, URN or name> or user:<id or handle> — the kind prefix is required (a worker name and a user handle are both bare words); the value given is not shown",
		flag)
}

// taskRefArg validates the one positional task id every task-id verb takes.
// The TRIMMED form is both validated and returned (cycle 2, Xylon P3-1): a
// pasted id with surrounding whitespace used to be sent verbatim and fail as
// NOT_FOUND for a reason the caller never typed.
//
// With an explicit --app, a bare handle is qualified in that App
// (qualifyTaskRef, cli#1831); taking the Factory makes every positional site
// go through it.
func taskRefArg(f *cmdutil.Factory, args []string) (string, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return "", exitcode.Newf(exitcode.Usage, "pass the task id shown by `team task list`/`get`")
	}
	return qualifyTaskRef(f, strings.TrimSpace(args[0]))
}

// taskRefArgFromBinding is taskRefArg for `team task get`, which also qualifies a bare handle from the
// worktree binding when there is no --app (qualifyTaskRefFromBinding, cli#3037).
func taskRefArgFromBinding(ctx context.Context, f *cmdutil.Factory, args []string) (string, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return "", exitcode.Newf(exitcode.Usage, "pass the task id shown by `team task list`/`get`")
	}
	return qualifyTaskRefFromBinding(ctx, f, strings.TrimSpace(args[0]))
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
	var runFlags taskRunSettingsFlags
	var description, owner, assignee, parent, source, externalID, startBy, finishBy, status string
	var tags, blockedBy, refs []string
	var priority int
	var familyIn, familyNotIn, modelGroupIn, modelGroupNotIn, minTier, properties, teamLead string
	var requireRoles []string
	var match bool
	var excludeWorkers []string
	var closeSession, noCloseSession, project bool
	var batchFile, rawInstruction string
	var distinctAssignees bool
	cmd := &cobra.Command{
		Use:   "create <title> | create --batch-file <path|->",
		Short: "Create a task",
		Long: `Create an agent task in the team App and put it with someone: name an
--assignee, or leave it queued for the platform's matcher to pick a worker
(see "Who goes on it", below; examples follow the usage line). Anyone in the
App may create a root task; only the parent's assignee may create a child task
(--parent). The App comes from --app, else the configured App context, else the
worktree binding; a child task takes its App from the parent (an --app that
disagrees is refused). Pass --source (an issue URL or node URN) so readers can
trace the task: without one the server adds the warning task.create.no-source.

Who goes on it:
- --assignee worker:<name> or user:<handle> puts it straight in that party's
  queue as PENDING. They start it with ` + "`task start`" + `.
- No --assignee leaves it QUEUED (PENDING, no assignee): the platform's matcher
  picks a worker. Requirements narrow its choice: --require-role <role> (the
  App's roles: ` + "`team role list`" + `), --family-in or --family-not-in a comma
  list of model families such as claude,codex (` + "`team availability`" + `), and
  --exclude-worker <name, URN or id>, repeatable. Among queued tasks the
  matcher offers priority 1 first, then the oldest.
- --model-group <name,...> requires a match in at least one named model group;
  --model-group-not <name,...> requires a match in none. Both lists may coexist,
  even with the same name. Empty supplied lists are usage errors; other names
  go unchanged to the server for validation. Groups resolve live at match time:
  editing a group can change matches without a task edit. They are matching
  inputs only, never authorization. All task-record reads and mutation receipts
  require a server released on or after 2026-11-17, even with no group flag
  (see team task --help).
  Comma splitting keeps each item verbatim; the server trims and drops empty items.
- --status backlog parks it instead: no assignee (the server refuses both),
  never matched, no alarms, until ` + "`task assign`" + ` or ` + "`task ready`" + `
  moves it to PENDING. --status pending is the default.

Where it lands: without --parent the task goes under AT0, the reserved
"no project", as AT0.<n>. --project makes a top-level project AT<n> instead.
--parent <task> makes a child (AT1.<n> under the project AT1, AT1.2.<n> under
AT1.2); the parent needs an assignee first, and that assignee creates the
children. --blocked-by <task> makes it wait for another task (see
` + "`task add-blocker`" + `).

--match tries once, right away, to find a worker for a queued task: one created
PENDING with no --assignee that is not blocked. The CLI sends the flag as given
and the server decides what it does for a task that has an assignee or is
blocked; leaving it out keeps ordinary creation.
Success prints the selected worker. If no worker matches, the task is STILL
CREATED unassigned: stdout includes its id plus matchFailure reason and time;
exit 5 follows only after the receipt is written (writer failure takes
precedence). Below that line it lists every skipped candidate with every
reason, in the server's order (matchSkipped; --json carries it as an array,
[] when nothing was skipped). Do not retry creation: use that task id.
Automatic matchers skip a marked task; assignment, cancellation, or park then
ready clears its mark.
Ready resumes regular matching, not a second match-now attempt. A server refusal
(BAD_USER_INPUT, exit 2) creates no task. A transport failure can leave the
outcome unknown; inspect task list before retrying.

--require-role accepts comma-separated or repeated roles; any one suffices.
One role uses the existing single-role requirement. The server checks the
1–32 roles, nonblank/128-character limits and matches case-insensitively.
--team <lead worker id, URN or name> restricts the pool to that active same-App
lead, its reporting descendants and workers reporting to nobody. Other
requirements still combine with AND. This is unrelated to task list --team's
coordinator view. Unknown, retired or other-App leads are BAD_USER_INPUT.

--min-tier low|mid|high sets a minimum model tier. The matcher prefers the
lowest sufficient tier; workers with an unknown tier are ineligible when a
minimum is set. Tier is a matching input, never an authorization rule.

--start-by <RFC 3339 time> sets when the task should be started. Once it passes,
` + "`task list --overdue`" + ` lists the task while it is still pending, whether or
not it is assigned or blocked; that is a filter, not an alarm (this is the
start-by arm — a passed --finish-by lists a task under the same filter; see
below). The not-started stall alarm is separate: it fires
only for an assigned, unblocked PENDING task, and its clock starts at the
release if the task was blocked. The deadline is the start-by when that falls
after the clock starts; otherwise, and with no --start-by, it is the App's
not-started window (` + "`team stall-schedule get`" + `) counted from then. So a
task released or reassigned after its start-by gets a fresh window.

--finish-by <RFC 3339 time> sets a deadline to finish. While set, ordinary
stall windows do not apply; stall.finish-by is the deadline alarm for an
unfinished task (a backlog task raises none, a blocked task waits for
release). Once the finish-by passes, ` + "`task list --overdue`" + ` also lists the
task, pending or started, until it ends — the same filter, not an alarm (this
arm needs a server released on or after 2026-11-17; an older one filters by
start-by only). The server refuses a past value or one earlier than start-by
(BAD_USER_INPUT, exit 2); equal times are allowed. It owns the clock
and all alarm behavior. With no flag the key is omitted. Change or
clear it with task update.

--family, --model or --tier, and --effort set the task's run settings: what to
launch, not whom to match (that is --family-in and --min-tier). --model and
--tier exclude each other. They apply only when the assigned worker's Agent is
short-lived; a platform start of a long-lived worker uses the worker's own
record and ignores them, so --family codex does not relaunch a long-lived
Claude worker. They are set at creation and never edited,
and a clone copies none. The CLI refuses a model with a tier and an effort of
NOT_APPLICABLE (exit 2); anything depending on the task's requirements is the
server's, shown as TASK_RUN_SETTINGS_INVALID (exit 2) with its reason.

Close-session: by default finishing or cancelling does not ask the assignee to end its
worker session. Pass --close-session to ask; the platform
may end it through the worker's live host after a grace period, 10 minutes by
default, set per App, and declining the request does not prevent that.
--no-close-session states the default explicitly. ` + "`task update`" + ` changes
the choice later; to set it up front, when the assignee's session should end
with the task, pass --close-session at create, or on the assign that first
names the assignee of a queued task.

Owner: --owner defaults to you (the bound worker, or you as a person when no session
is bound); a root task's creator may name any other participating worker or
person. The owner, a worker the owner reports to and a
person who manages the App may then assign, reassign, park, cancel, prioritize
and block it. For a child task the owner is the parent's assignee — naming a
different owner is refused (TASK_CHILD_OWNER_MISMATCH).

Outside items: --external-id <id> records the outside item the task CLAIMS, such as the
issue URL. At most one ACTIVE task (backlog, pending or
started) per App holds a given value, and it is set once, never edited. A
finished or cancelled task keeps its value but no longer blocks a new claim. A
claim on a value an active task holds is refused TASK_EXTERNAL_ID_TAKEN (exit
5), which names no task; find the holder with
` + "`task list --external-id <id> --status BACKLOG --status PENDING --status STARTED`" + `
(the unfiltered ` + "`task list --external-id <id>`" + ` also lists ended tasks, newest
first). The value is sent as given; only the server judges its form.

--ref <kind>=<target>[|<label>] (repeatable, kept in order) records what the
task points at: kind is issue, pull-request, node, task or url, and the label
starts at the first |.
` + taskRefCapsSentence + `
The CLI checks that shape and the label length; the server owns the form of
each target and the other caps (BAD_USER_INPUT, exit 2, naming the item's
position).

Properties: --properties <json|@file> gives it its own JSON object (the team's schedule
and review notes, for example), which the platform stores and never reads. It
is set only when the creator may write properties (the owner, someone above
the owner, a manager, or the assignee named here); any other creator gets the
task without them, not a refusal. The server checks it is an object, at most
16 KiB and valid against the team's task-properties schema
(PROPERTIES_NOT_AN_OBJECT, PROPERTIES_TOO_LARGE, PROPERTIES_SCHEMA_VIOLATION,
PROPERTIES_SCHEMA_UNUSABLE). Change it later with ` + "`task update --properties`" + `.

The server owns eligibility, authority and matching; the CLI never selects a
worker. Every task command reads fields that older servers lack, whichever flags
you pass: the run settings, the finish-by time, the matching metadata, the
minimum tier and the model-group records. The newest of them needs a server
released on or after 2026-11-17, which is the floor for every task command. A
server that lacks any one of them refuses every task command, even a plain
create; there is no fallback.

Several at once: --batch-file <path|-> creates 1 to 25 tasks in one call, all or
none (a path, or - for piped stdin). The file is a JSON array; each item is
{"task": {...}, "blockedByPositions": [...]}. "task" takes the same fields as the
flags above, by their API names (title, assignee, description, tags, ...): for
example {"title": "Fix login", "assignee": {"worker": "Iris"}}. Every field is
listed in the published schema: an item is CreateAgentTasksItemInput and its
"task" is CreateAgentTaskInput
(https://github.com/hadron-memory/hadron-cli/blob/main/schema/schema.graphql;
a server newer than that file can have fields it does not list yet).
"blockedByPositions" lists the 1-based positions, in this same file, of tasks
that must end first; a self-block or a cycle is refused (TASK_BLOCKER_CYCLE). The
single-create flags do not combine with it (--distinct-assignees does), and the
call either creates every task or none, naming the failing position. The
batch takes no --instruction.

Success prints the task's handle, id and status, "queued" when it has no
assignee, the App and the close-session choice; --json returns the task with a
warnings list.` + "\n\n" + taskInstructionHelpShort(taskInstructionFloor),
		Example: `  # assign to a named worker, with a deadline to start
  hadron team task create "Fix login redirect" --app acme.com:eng-team --assignee worker:Iris \
    --priority 2 --start-by 2027-01-15T09:00:00Z --source https://github.com/acme/app/issues/7

  # queue it for any QA engineer not running as codex
  hadron team task create "Retest the export fix" --app acme.com:eng-team \
    --require-role qa-engineer --family-not-in codex --description "Run the export suite on staging"

  # park an idea in the backlog
  hadron team task create "Tidy the release notes" --app acme.com:eng-team --status backlog

  # a project, then (as its assignee) a child under it
  hadron team task create "Q4 billing" --app acme.com:eng-team --project --assignee worker:Iris
  hadron team task create "Draft the invoice schema" --parent AT1 --assignee worker:Uma

  # two tasks in one call, the second blocked by the first (positions are 1-based)
  # tasks.json: [{"task": {"title": "Build the export"}},
  #              {"task": {"title": "Retest the export"}, "blockedByPositions": [1]}]
  hadron team task create --app acme.com:eng-team --batch-file tasks.json`,
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("batch-file") {
				if len(args) != 0 {
					return exitcode.Newf(exitcode.Usage, "--batch-file takes no title: each task's title is in the file")
				}
				return nil
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("batch-file") && cmd.Flags().Changed("instruction") {
				return errNoTaskInstructionForBatch
			}
			claim, err := taskClaim(cmd, rawInstruction)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("batch-file") {
				var clash []string
				// Only this command's own flags: --json, --app and --server are
				// persistent and do combine with a batch.
				cmd.LocalNonPersistentFlags().VisitAll(func(fl *pflag.Flag) {
					if fl.Name != "batch-file" && fl.Name != "distinct-assignees" && cmd.Flags().Changed(fl.Name) {
						clash = append(clash, "--"+fl.Name)
					}
				})
				if len(clash) > 0 {
					return exitcode.Newf(exitcode.Usage, "--batch-file does not combine with the single-create flags (%s): put each task's fields in the file", strings.Join(clash, ", "))
				}
				var distinct *bool
				if cmd.Flags().Changed("distinct-assignees") {
					distinct = &distinctAssignees // presence, not value: a given =false is sent as false
				}
				return runTaskBatchCreate(cmd, f, batchFile, distinct)
			}
			if cmd.Flags().Changed("distinct-assignees") {
				return exitcode.Newf(exitcode.Usage, "--distinct-assignees needs --batch-file: it is an option of the batch create")
			}
			if cmd.Flags().Changed("min-tier") {
				if err := validateTaskMinTier(minTier); err != nil {
					return err
				}
			}
			roles, err := taskRoleFlags(requireRoles, cmd.Flags().Changed("require-role"))
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("team") && strings.TrimSpace(teamLead) == "" {
				return exitcode.Newf(exitcode.Usage, "--team needs a lead worker reference")
			}
			ctx := cmd.Context()
			title := strings.TrimSpace(args[0])
			if title == "" {
				return exitcode.Newf(exitcode.Usage, "the title is empty")
			}
			if startBy != "" {
				if _, err := time.Parse(time.RFC3339, startBy); err != nil {
					return exitcode.Newf(exitcode.Usage, "--start-by takes an RFC 3339 timestamp, e.g. 2026-10-13T09:00:00Z")
				}
			}
			if cmd.Flags().Changed("finish-by") {
				if _, ok := cmdutil.ParseStrictDateTime(finishBy); !ok {
					return exitcode.Newf(exitcode.Usage, "--finish-by takes an RFC 3339 timestamp, e.g. 2099-11-02T09:00:00Z")
				}
			}
			groupsIn, groupsNotIn, err := taskModelGroupFlags(cmd, modelGroupIn, modelGroupNotIn)
			if err != nil {
				return err
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
					return exitcode.Newf(exitcode.Usage, "--status is backlog or pending at creation (every other status is reached by a move)")
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
				if blockedBy[i], err = qualifyTaskRef(f, blockedBy[i]); err != nil {
					return err
				}
			}
			if parent != "" {
				if parent, err = qualifyTaskRef(f, parent); err != nil {
					return err
				}
			}
			// A supplied blank is a usage slip, refused here. Every other
			// check on the value (length, characters, the duplicate) is the
			// server's (cor:agt:120:02, cor:api:240:01): it is sent as given.
			if cmd.Flags().Changed("external-id") && strings.TrimSpace(externalID) == "" {
				return exitcode.Newf(exitcode.Usage, "--external-id needs a value, such as the issue URL the task claims")
			}
			refInputs, err := parseTaskRefFlags(refs)
			if err != nil {
				return err
			}
			input := gen.CreateAgentTaskInput{Priority: priorityArg, BlockedBy: blockedBy,
				Title:        title,
				Description:  optStr(description),
				ParentTaskId: optStr(parent),
				Source:       optStr(source),
				ExternalId:   optStr(externalID),
				StartBy:      optStr(startBy),
				FinishBy:     optStr(finishBy),
				Status:       createStatus,
				Tags:         tags,
			}
			// cli#2206: the run settings, create-only; nil without a flag.
			if input.RunSettings, err = runFlags.build(cmd); err != nil {
				return err
			}
			// False and omission keep the legacy input unchanged.
			if match {
				input.Match = boolPtr(true)
			}
			// Omitted unless the flag is given: the server reads an absent
			// project as "no project" (AT0) and an explicit false the same, so
			// presence, not value, is what is sent (cor:agt:050:01).
			if cmd.Flags().Changed("project") {
				input.Project = &project
			}
			if cmd.Flags().Changed("properties") {
				p, err := parsePropertiesFlag(properties)
				if err != nil {
					return err
				}
				input.Properties = &p
			}
			if closeSession && noCloseSession {
				return exitcode.Newf(exitcode.Usage, "--close-session and --no-close-session contradict each other")
			}
			if len(refs) > 0 {
				input.References = refInputs
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
			if len(roles) > 0 || cmd.Flags().Changed("team") || familyIn != "" || familyNotIn != "" || len(excludeWorkers) > 0 || (cmd.Flags().Changed("min-tier") || cmd.Flags().Changed("model-group") || cmd.Flags().Changed("model-group-not")) {
				input.Requirements = &gen.AgentTaskRequirementsInput{
					ExcludeWorkers:  excludeWorkers,
					FamilyIn:        commaList(familyIn),
					FamilyNotIn:     commaList(familyNotIn),
					ModelGroupIn:    groupsIn,
					ModelGroupNotIn: groupsNotIn,
					Role:            json.RawMessage("null"),
					Team:            optStr(teamLead),
					MinTier:         optStr(minTier),
				}
			}
			if input.Requirements != nil {
				if len(roles) == 1 {
					role, _ := json.Marshal(roles[0]) // a string cannot fail JSON encoding
					input.Requirements.Role = role
				} else {
					input.Requirements.Roles = roles
					if len(roles) > 1 {
						input.Requirements.Role = nil
					}
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
			var fields gen.AgentTaskFields
			var skipped []*gen.AgentTaskCreateCreateAgentTaskMatchSkippedAgentTaskSkippedCandidate
			var resp *gen.AgentTaskCreateResponse
			if claim != nil {
				ir, ierr := gen.AgentTaskCreateInstructed(mctx, client, &input, claim)
				err = ierr
				if ierr == nil {
					fields = ir.CreateAgentTask.AgentTaskFields
					for _, c := range ir.CreateAgentTask.MatchSkipped {
						if c == nil {
							skipped = append(skipped, nil)
							continue
						}
						conv := gen.AgentTaskCreateCreateAgentTaskMatchSkippedAgentTaskSkippedCandidate(*c)
						skipped = append(skipped, &conv)
					}
				}
			} else {
				resp, err = gen.AgentTaskCreate(mctx, client, &input)
				if err == nil {
					fields, skipped = resp.CreateAgentTask.AgentTaskFields, resp.CreateAgentTask.MatchSkipped
				}
			}
			if err != nil {
				// The new requirement refusals retain their code at the JSON sink,
				// and so does a claim's TASK_EXTERNAL_ID_TAKEN (cli#1524), so a
				// script tells the duplicate from another conflict. Keep ordinary
				// task-write error output unchanged when neither is named.
				if match || len(roles) > 1 || cmd.Flags().Changed("team") || groupsIn != nil || groupsNotIn != nil || cmd.Flags().Changed("external-id") {
					return api.WithServerCode(mapTaskError(err), err)
				}
				return mapTaskError(err)
			}
			task, err := taskDTOFromWrite(fields)
			if err != nil {
				return err
			}
			if match {
				task.MatchSkipped = matchSkippedDTO(skipped)
			}
			err = writeTaskMutation(f, task, taskWarningsFor(sink, "createAgentTask"), func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ created task %s (%s)\n", taskRef(task.Handle, task.ID), task.Status); err != nil {
					return err
				}
				if task.Queued && task.MatchFailure == nil {
					if _, err := fmt.Fprintln(w, "  queued: no assignee — the platform's matcher picks one"); err != nil {
						return err
					}
				}
				if match {
					if task.Assignee != nil {
						if _, err := fmt.Fprintf(w, "  matched: %s (id %s; URN %s)\n", partyCell(task.Assignee), output.SafeCell(task.Assignee.ID), output.SafeCell(strOr(task.Assignee.Urn))); err != nil {
							return err
						}
					} else if task.MatchFailure != nil {
						if err := writeTaskMatchFailure(w, task.MatchFailure); err != nil {
							return err
						}
						if err := writeTaskMatchSkipped(w, *task.MatchSkipped); err != nil {
							return err
						}
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
			if err != nil {
				return err
			}
			if match && task.MatchFailure != nil {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&priority, "priority", 1, "matching priority: 1-5, 1 = highest")
	cmd.Flags().StringArrayVar(&blockedBy, "blocked-by", nil, "task id or handle that blocks this task (repeatable; same-App/cycle rules are server-owned)")
	cmd.Flags().StringVar(&description, "description", "", "what the task asks for")
	cmd.Flags().StringVar(&assignee, "assignee", "", "party to assign it to (worker:<…> / user:<…>); omit to leave it queued")
	cmd.Flags().StringVar(&owner, "owner", "", "task owner (defaults to you)")
	cmd.Flags().StringVar(&parent, "parent", "", "parent task id (child task)")
	cmd.Flags().StringVar(&startBy, "start-by", "", "RFC 3339 deadline to start by")
	cmd.Flags().StringVar(&finishBy, "finish-by", "", "RFC 3339 deadline to finish by; server validates it against now and start-by")
	cmd.Flags().StringVar(&source, "source", "", "origin reference (an issue URL, a node URN)")
	runFlags.register(cmd)
	cmd.Flags().StringVar(&externalID, "external-id", "", "the outside item this task claims, such as its issue URL; one ACTIVE task per App holds a value (finished and cancelled tasks keep it); set once, never edited")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tag (repeatable)")
	cmd.Flags().StringArrayVar(&refs, "ref", nil, "typed reference <kind>=<target>[|<label>], repeatable, kept in order; kind is "+taskRefKindsDoc+taskRefCapsFlagDoc)
	cmd.Flags().StringVar(&minTier, "min-tier", "", "minimum model tier: low, mid or high (lowest sufficient preferred; unknown ineligible)")
	cmd.Flags().StringArrayVar(&requireRoles, "require-role", nil, "eligible role, comma-separated or repeatable; any one suffices")
	cmd.Flags().BoolVar(&match, "match", false, "run one blocking match now; failed match prints the created task then exits 5")
	cmd.Flags().StringVar(&teamLead, "team", "", "lead worker reference restricting the matching pool (server-owned)")
	addTaskModelGroupFlags(cmd, &modelGroupIn, &modelGroupNotIn)
	cmd.Flags().StringVar(&familyIn, "family-in", "", "comma list of model families the assignee must run as")
	cmd.Flags().StringVar(&familyNotIn, "family-not-in", "", "comma list of model families to exclude")
	cmd.Flags().StringSliceVar(&excludeWorkers, "exclude-worker", nil, "worker (name, URN or id) the matcher must not pick (repeatable)")
	cmd.Flags().BoolVar(&project, "project", false, "make a top-level project (AT1, AT2, …) instead of a task under the reserved project AT0 (\"no project\"); not with --parent")
	cmd.Flags().StringVar(&status, "status", "", "create-time status: backlog (parked) or pending (the default)")
	addTaskInstructionFlag(cmd, &rawInstruction)
	cmd.Flags().BoolVar(&closeSession, "close-session", false, "finishing or cancelling asks the assignee to end its worker session")
	cmd.Flags().BoolVar(&noCloseSession, "no-close-session", false, "finishing or cancelling does not ask the assignee to end its worker session (the default)")
	cmd.Flags().StringVar(&properties, "properties", "", "the task's own JSON object, or @file")
	cmd.Flags().StringVar(&batchFile, "batch-file", "", "create 1-25 tasks in one call from a JSON array of {task, blockedByPositions} items (a path, or - for piped stdin); not with the single-create flags")
	cmd.Flags().BoolVar(&distinctAssignees, "distinct-assignees", false, "with --batch-file: every task that asks for a match goes to a different worker; any failed match refuses the whole call, naming the task and the reason")
	return cmd
}

// ---- assign / reassign ----------------------------------------------------

func newCmdTeamTaskAssign(f *cmdutil.Factory, verb string, short string, reassign bool) *cobra.Command {
	var assignee, rawInstruction string
	var closeSession, noCloseSession bool
	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s <taskId> --assignee <party>", verb),
		Short: short,
		Long:  assignLong(verb) + "\n\n" + taskInstructionHelpShort(taskInstructionFloor),
		Example: map[bool]string{
			false: "  hadron team task assign AT0.7 --assignee worker:Iris\n  hadron team task assign AT0.7 --assignee worker:Iris --close-session",
			true:  "  hadron team task reassign AT0.7 --assignee worker:Uma",
		}[reassign],
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			claim, err := taskClaim(cmd, rawInstruction)
			if err != nil {
				return err
			}
			taskId, err := taskRefArg(f, args)
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
			// Presence, not value (as `task update` does, #1137): a plain bool
			// cannot tell `--close-session=false` from an absent flag, so the
			// =false spelling is refused before any request instead of silently
			// keeping the stored choice.
			for _, c := range []struct {
				name string
				on   bool
				use  string
			}{{"close-session", closeSession, "--no-close-session"}, {"no-close-session", noCloseSession, "--close-session"}} {
				if cmd.Flags().Changed(c.name) && !c.on {
					return exitcode.Newf(exitcode.Usage, "--%s=false changes nothing; to say the opposite pass %s", c.name, c.use)
				}
			}
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
			switch {
			case reassign && claim != nil:
				key = "reassignAgentTask"
				resp, err := gen.AgentTaskReassignInstructed(mctx, client, taskId, party, closeWhenDone, claim)
				if err != nil {
					return mapTaskError(err)
				}
				if task, err = taskDTOFromWrite(resp.ReassignAgentTask.AgentTaskFields); err != nil {
					return err
				}
			case reassign:
				key = "reassignAgentTask"
				resp, err := gen.AgentTaskReassign(mctx, client, taskId, party, closeWhenDone)
				if err != nil {
					return mapTaskError(err)
				}
				if task, err = taskDTOFromWrite(resp.ReassignAgentTask.AgentTaskFields); err != nil {
					return err
				}
			case claim != nil:
				resp, err := gen.AgentTaskAssignInstructed(mctx, client, taskId, party, closeWhenDone, claim)
				if err != nil {
					return mapTaskError(err)
				}
				if task, err = taskDTOFromWrite(resp.AssignAgentTask.AgentTaskFields); err != nil {
					return err
				}
			default:
				resp, err := gen.AgentTaskAssign(mctx, client, taskId, party, closeWhenDone)
				if err != nil {
					return mapTaskError(err)
				}
				if task, err = taskDTOFromWrite(resp.AssignAgentTask.AgentTaskFields); err != nil {
					return err
				}
			}
			return writeTaskMutation(f, task, taskWarningsFor(sink, key), func(w io.Writer) error {
				if err := writeTaskReceipt(w, verb+"ed", task); err != nil {
					return err
				}
				return writeCloseSessionLine(w, task)
			})
		},
	}
	addTaskInstructionFlag(cmd, &rawInstruction)
	cmd.Flags().StringVar(&assignee, "assignee", "", "party to assign the task to (worker:<…> / user:<…>)")
	cmd.Flags().BoolVar(&closeSession, "close-session", false, "finishing or cancelling asks the assignee to end its worker session")
	cmd.Flags().BoolVar(&noCloseSession, "no-close-session", false, "finishing or cancelling does not ask the assignee to end its worker session")
	return cmd
}

// closeSessionVerbNote is the one sentence on assign/reassign's close-session
// flags that differs by verb.
func closeSessionVerbNote(verb string) string {
	if verb == "reassign" {
		return "A choice you give applies to the new assignee; reassigning a task to its\ncurrent assignee is refused (TASK_INVALID_TRANSITION) with the choice unchanged."
	}
	return "A choice you give is set on this assignment; reassigning a task to its\ncurrent assignee is refused (TASK_INVALID_TRANSITION) with the choice unchanged."
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

Who may: ` + taskAssignerDoc + `.
Anyone else is refused with TASK_NOT_OWNER (exit 8). An ended task is refused
(exit 5). --assignee is required (worker:<id, URN or name> or
user:<id or handle>). The App comes from the task; --app qualifies a bare handle.

The server may add a warning that does not block the assignment, for example
when the assignee is unavailable, busy or paused (task.assign.assignee-*).
Success prints the task, its status, the assignee and the close-session
choice; --json returns the task with a warnings list.

Close-session choice (should the assignee's session end when it finishes the task):
--close-session turns it on, --no-close-session turns it off, and giving neither
keeps the task's current value. Give at most one; both together is a usage error,
and ` + "`--close-session=false`" + ` (or --no-close-session=false) is refused with exit 2,
because it cannot be told from leaving the flag out: say no with
--no-close-session. ` + closeSessionVerbNote(verb) + `
The default of not asking applies at creation; assign and reassign with neither
flag keep the value the task already has. To change the choice without assigning,
use ` + "`task update`" + `. To have the assignee's session end with the task from the start,
pass --close-session at create, or on the assign that first names the assignee of a
queued task.`
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
	return newTaskMutation(f, verb, key, short, long, example, false,
		func(ctx context.Context, client graphql.Client, taskId string, _ *gen.ActInstructionInput) (taskDTO, error) {
			return call(ctx, client, taskId)
		}, receipt)
}

// newInstructableTaskMutation is newSimpleTaskMutation for a verb that takes
// --instruction (cli#2422): call gets the parsed claim, nil without the flag,
// and sends its plain document when it is nil.
func newInstructableTaskMutation(
	f *cmdutil.Factory,
	verb, key, short, long, example string,
	call func(context.Context, graphql.Client, string, *gen.ActInstructionInput) (taskDTO, error),
	receipt func(io.Writer, taskDTO) error,
) *cobra.Command {
	return newTaskMutation(f, verb, key, short, long, example, true, call, receipt)
}

func newTaskMutation(
	f *cmdutil.Factory,
	verb, key, short, long, example string,
	instructable bool,
	call func(context.Context, graphql.Client, string, *gen.ActInstructionInput) (taskDTO, error),
	receipt func(io.Writer, taskDTO) error,
) *cobra.Command {
	var rawInstruction string
	if instructable {
		long += "\n\n" + taskInstructionHelpShort(taskInstructionFloor)
	}
	cmd := &cobra.Command{
		Use:     fmt.Sprintf("%s <taskId>", verb),
		Short:   short,
		Long:    long,
		Example: example,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			claim, err := taskClaim(cmd, rawInstruction)
			if err != nil {
				return err
			}
			taskId, err := taskRefArg(f, args)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			mctx, sink := api.WithWarningSink(cmd.Context())
			task, err := call(mctx, client, taskId, claim)
			if err != nil {
				return mapTaskError(err)
			}
			return writeTaskMutation(f, task, taskWarningsFor(sink, key), func(w io.Writer) error {
				return receipt(w, task)
			})
		},
	}
	if instructable {
		addTaskInstructionFlag(cmd, &rawInstruction)
	}
	return cmd
}

func newCmdTeamTaskStart(f *cmdutil.Factory) *cobra.Command {
	return newSimpleTaskMutation(f, "start", "startAgentTask", "Start a pending task",
		`Start a task assigned to you: PENDING becomes STARTED and the server records
task.started. Only the assignee may start it — with a
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
			return taskDTOFromWrite(resp.StartAgentTask.AgentTaskFields)
		},
		func(w io.Writer, task taskDTO) error {
			return writeTaskReceipt(w, "started", task)
		})
}

func newCmdTeamTaskProgress(f *cmdutil.Factory) *cobra.Command {
	return newSimpleTaskMutation(f, "progress", "recordAgentTaskProgress", "Record progress on a started task",
		`Record that you are still working on a STARTED task: its assignee only. It
carries no text. It writes task.progress to the event stream and resets the
no-progress stall alarm's clock, which fires when a STARTED task with no open
child sees no activity from its assignee for the App's window
(`+"`team stall-schedule get`"+`; alarms in `+"`team events --alarms`"+`).

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
			return taskDTOFromWrite(resp.RecordAgentTaskProgress.AgentTaskFields)
		},
		func(w io.Writer, task taskDTO) error {
			_, err := fmt.Fprintf(w, "✓ recorded progress on task %s (%s)\n", taskRef(task.Handle, task.ID), task.Status)
			return err
		})
}

func newCmdTeamTaskCancel(f *cmdutil.Factory) *cobra.Command {
	return newInstructableTaskMutation(f, "cancel", "cancelAgentTask", "Cancel a task",
		`Cancel a task that is no longer wanted, from BACKLOG, PENDING or STARTED.
CANCELLED is an end state: nobody can start, progress or finish it afterwards,
and it no longer blocks the tasks waiting for it. The command does not ask for
confirmation.

Who may: `+taskAssignerDoc+`;
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
		func(ctx context.Context, client graphql.Client, taskId string, claim *gen.ActInstructionInput) (taskDTO, error) {
			if claim != nil {
				resp, err := gen.AgentTaskCancelInstructed(ctx, client, taskId, claim)
				if err != nil {
					return taskDTO{}, err
				}
				return taskDTOFromWrite(resp.CancelAgentTask.AgentTaskFields)
			}
			resp, err := gen.AgentTaskCancel(ctx, client, taskId)
			if err != nil {
				return taskDTO{}, err
			}
			return taskDTOFromWrite(resp.CancelAgentTask.AgentTaskFields)
		},
		func(w io.Writer, task taskDTO) error {
			return writeTaskReceipt(w, "cancelled", task)
		})
}

// ---- park / ready (the BACKLOG moves, #1754 / cor:agt:050:02) --------------

func newCmdTeamTaskPark(f *cmdutil.Factory) *cobra.Command {
	return newInstructableTaskMutation(f, "park", "parkAgentTask", "Park a queued task (BACKLOG)",
		`Park the task: a pending task with no assignee becomes BACKLOG —
unassigned, never matched, never waited on, no alarms.
A task that has an assignee, or is STARTED, is refused (exit 5); to stop
started work for good, cancel it. Make it ready (`+"`team task ready`"+`) — or
assign it — to move it back to PENDING. Success prints the task and BACKLOG;
--json returns the task with a warnings list, and a server warning also
prints on stderr.

Who may: `+taskAssignerDoc+`.`,
		"  hadron team task park AT0.7",
		func(ctx context.Context, client graphql.Client, taskId string, claim *gen.ActInstructionInput) (taskDTO, error) {
			if claim != nil {
				resp, err := gen.AgentTaskParkInstructed(ctx, client, taskId, claim)
				if err != nil {
					return taskDTO{}, err
				}
				return taskDTOFromWrite(resp.ParkAgentTask.AgentTaskFields)
			}
			resp, err := gen.AgentTaskPark(ctx, client, taskId)
			if err != nil {
				return taskDTO{}, err
			}
			return taskDTOFromWrite(resp.ParkAgentTask.AgentTaskFields)
		},
		func(w io.Writer, task taskDTO) error {
			return writeTaskReceipt(w, "parked", task)
		})
}

func newCmdTeamTaskReady(f *cmdutil.Factory) *cobra.Command {
	return newInstructableTaskMutation(f, "ready", "makeAgentTaskReady", "Make a backlog task ready to work",
		`Make the backlog task ready: BACKLOG becomes PENDING with no assignee and the
task enters the queue, where the platform's matcher picks a worker. To choose
the worker yourself, `+"`task assign`"+` the BACKLOG task instead. Success prints
the task, PENDING, and "queued" while no one has it; --json returns the task
with a warnings list, and a server warning also prints on stderr.

Who may: `+taskAssignerDoc+`.`,
		"  hadron team task ready AT0.7",
		func(ctx context.Context, client graphql.Client, taskId string, claim *gen.ActInstructionInput) (taskDTO, error) {
			if claim != nil {
				resp, err := gen.AgentTaskReadyInstructed(ctx, client, taskId, claim)
				if err != nil {
					return taskDTO{}, err
				}
				return taskDTOFromWrite(resp.MakeAgentTaskReady.AgentTaskFields)
			}
			resp, err := gen.AgentTaskReady(ctx, client, taskId)
			if err != nil {
				return taskDTO{}, err
			}
			return taskDTOFromWrite(resp.MakeAgentTaskReady.AgentTaskFields)
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
	var outcome, summary, evidence, findingsFile string
	var workedFrom []string
	cmd := &cobra.Command{
		Use:   "finish <taskId> --outcome <word>",
		Short: "Finish a started task with its result",
		Long: `Finish a STARTED task with its result: its assignee only.
FINISHED is an end state, and the tasks it blocked stop waiting for it.

--outcome is one word, up to 64 letters, digits, underscores, hyphens or dots,
e.g. succeeded or failed. --summary (one line) and --evidence (a URL or node
URN) are optional; they are the place for a written account of the work.

Refusals: a PENDING task exits 5 ("start it first"): run ` + "`task start`" + `,
then finish. A review-seat task with no verdict recorded exits 5
(SEAT_VERDICT_REQUIRED): record it with ` + "`team pr verdict`" + ` while the task is
started, then finish (a seat never bound, a superseded one, and outcome failed or
cancelled are exempt). Someone else's task exits 8; an ended task exits 5. Open child
tasks do not stop it: the server adds the warning task.finish.open-children
and the children stay open. If the task was created with --close-session,
finishing asks the assignee to end its worker session (see ` + "`task create --help`" + `);
the command itself ends nothing.

Opt in with --findings-file <path|-> to attach immutable findings. The file is
one UTF-8 JSON array of {"id": "F1", "severity": "P2", "text": "..."}; omit
all ids (or use null) for server F1/F2/... ids, or supply every id. Ids are
unique case-sensitive ASCII letters/digits/_/-, 1–32 characters. Severity is
exactly P1/P2/P3/note, a label that never changes the exit code or outcome.
At most 100 findings, each text at most 2,000 Unicode code points, no trimming;
empty text is allowed. Reject duplicate/unknown keys, NUL, invalid UTF-8 or
unpaired surrogate escapes, null/non-array roots and trailing JSON. File input is capped at
4 MiB; - reads piped stdin once (interactive stdin refuses). Local refusals
exit 2 before any mutation and never echo input text.

Omitting this flag keeps the ordinary finish request and output unchanged;
explicit [] is a present empty list. Only opt-in findings finish and the new
findings read need a server released on or after 2026-11-02.
Opt-in finish sends one mutation and shows its first page (20) with total and
hasMore; use team task findings for continuation, never task get or a finish
retry. JSON keeps the task/warnings shape and adds result.findings as a page.
After a lost/malformed answer state may have changed: inspect the immutable
findings before deciding what to do. No automatic retry or page drain.
Human findings neutralize controls; JSON retains original valid text.

--worked-from REF records the role tasks you followed: repeat it
for each, at most 10 (an eleventh exits 2 before any request). REF is a node id
or URN of a live runnable node you can read, and may not repeat. The server
keeps each as node id, URN and revision, in the order given, and never changes
them. Any it refuses (TASK_ROLE_TASK_REFS_INVALID, exit 2) leaves the task
started, and the message names no reference: the server answers the same for a
node you cannot read and one that does not exist. Combines with --findings-file.
Omitting the flag keeps the request and output as they were.

Success prints the task, the outcome and the summary; --json returns the task
with its result and a warnings list. The stored refs show under "worked from"
and in --json as result.workedFromRoleTaskRefs [{nodeId, urn, revision}].`,
		Example: `  hadron team task finish AT0.7 --outcome succeeded --summary "Login redirect fixed" --evidence https://github.com/acme/app/pull/9
  hadron team task finish AT0.8 --outcome failed --summary "Cannot reproduce on staging"
  hadron team task finish AT0.7 --outcome changes-requested --findings-file findings.json
  hadron team task finish AT0.9 --outcome succeeded --worked-from hrn:node:acme.com:eng-system:tasks:build-it --worked-from hrn:node:acme.com:eng-system:tasks:review-it`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			taskId, err := taskRefArg(f, args)
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
				return exitcode.Newf(exitcode.Usage, "--outcome is one word of letters, digits, underscore, hyphen or dot (up to 64 characters); the value given is not shown")
			}
			refs, err := workedFromRefs(workedFrom)
			if err != nil {
				return err
			}
			var findings []*gen.AgentTaskFindingInput
			withFindings := cmd.Flags().Changed("findings-file")
			if withFindings {
				findings, err = readTaskFindings(f, findingsFile)
				if err != nil {
					return err
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if withFindings {
				return finishTaskWithFindings(ctx, client, f, taskId, outcome, summary, evidence, refs, findings)
			}
			result := gen.AgentTaskResultInput{
				Outcome:                outcome,
				Summary:                optStr(summary),
				Evidence:               optStr(evidence),
				WorkedFromRoleTaskRefs: refs,
			}
			mctx, sink := api.WithWarningSink(ctx)
			resp, err := gen.AgentTaskFinish(mctx, client, taskId, &result)
			if err != nil {
				return mapTaskError(err)
			}
			task, err := taskDTOFromWrite(resp.FinishAgentTask.AgentTaskFields)
			if err != nil {
				return err
			}
			return writeTaskMutation(f, task, taskWarningsFor(sink, "finishAgentTask"), func(w io.Writer) error {
				out := ""
				if task.Result != nil {
					out = task.Result.Outcome
				}
				if _, err := fmt.Fprintf(w, "✓ finished task %s (%s)\n", taskRef(task.Handle, task.ID), out); err != nil {
					return err
				}
				if task.Result != nil && task.Result.Summary != nil {
					if _, err := fmt.Fprintf(w, "  %s\n", *task.Result.Summary); err != nil {
						return err
					}
				}
				return workedFromLines(w, task.Result, "  ")
			})
		},
	}
	cmd.Flags().StringVar(&outcome, "outcome", "", "one-word result, e.g. succeeded or failed (required)")
	cmd.Flags().StringVar(&summary, "summary", "", "one-line result summary")
	cmd.Flags().StringVar(&evidence, "evidence", "", "URL or node URN backing the result")
	cmd.Flags().StringVar(&findingsFile, "findings-file", "", "JSON findings array file, or - for piped stdin (needs a server from 2026-11-02)")
	cmd.Flags().StringArrayVar(&workedFrom, "worked-from", nil, "role task node id or URN you followed; repeat for each, at most 10 (live, runnable, readable, no repeats)")
	return cmd
}

// maxWorkedFrom is the server's cap on workedFromRoleTaskRefs (cor:agt:050:30).
const maxWorkedFrom = 10

// workedFromRefs checks --worked-from before any request: at most 10, none blank. Everything else (readable,
// runnable, no repeat) is the server's call and comes back as TASK_ROLE_TASK_REFS_INVALID. Nil when the flag
// is absent, so the request omits the field.
func workedFromRefs(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxWorkedFrom {
		return nil, exitcode.Newf(exitcode.Usage, "--worked-from: at most %d references, got %d", maxWorkedFrom, len(in))
	}
	out := make([]string, 0, len(in))
	for _, r := range in {
		r = strings.TrimSpace(r)
		if r == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--worked-from: a reference must not be empty")
		}
		out = append(out, r)
	}
	return out, nil
}

// ---- hand-back --------------------------------------------------------------

// handBackCodes is the closed set the server's AgentTaskHandBackCode enum
// carries (cor:agt:050:29), in the order the refusal lists it.
var handBackCodes = []string{"missing-input", "cannot-do", "refused", "out-of-scope", "duplicate"}

// handBackCodeFrom accepts the kebab spelling and the enum names, any case.
func handBackCodeFrom(s string) (gen.AgentTaskHandBackCode, error) {
	norm := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", "_"))
	for _, c := range handBackCodes {
		if strings.ToUpper(strings.ReplaceAll(c, "-", "_")) == norm && norm != "" {
			return gen.AgentTaskHandBackCode(norm), nil
		}
	}
	return "", exitcode.Newf(exitcode.Usage, "--code is required and must be one of %s", strings.Join(handBackCodes, " | "))
}

func newCmdTeamTaskHandBack(f *cmdutil.Factory) *cobra.Command {
	var code, text string
	cmd := &cobra.Command{
		Use:   "hand-back <taskRef> --code <code> [--text <text>]",
		Short: "Hand a started task back with a reason",
		Long: `Hand a STARTED task back to the queue with a reason: its assignee only.
The task returns to PENDING for the platform's matcher; nothing is finished
and nothing is lost.

--code is required, one of: ` + strings.Join(handBackCodes, ", ") + `
(case-insensitive; the enum names such as MISSING_INPUT are accepted too).
--text is an optional account, up to 2,000 characters after trimming; the
server is the authority on the limit and the CLI does not pre-trim it.

Refusals: an unknown task exits 4; someone else's task exits 8; an ended task
or a task that is not started exits 5; an uninstalled App exits 5; no bound
worker session exits 2.

Success prints the task and the "Handed back:" line; --json returns the task
with its handBack {code, text, handedBackAt} and a warnings list. ` + "`task get`" + `
shows the same line later; handBack is null for a task never handed back.`,
		Example: `  hadron team task hand-back AT0.7 --code missing-input --text "no fixture to reproduce with"
  hadron team task hand-back AT0.8 --code duplicate`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := taskRefArg(f, args)
			if err != nil {
				return err
			}
			c, err := handBackCodeFrom(code)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			reason := gen.AgentTaskHandBackInput{Code: c, Text: optStr(text)}
			mctx, sink := api.WithWarningSink(cmd.Context())
			resp, err := gen.AgentTaskHandBack(mctx, client, ref, &reason)
			if err != nil {
				return mapTaskError(err)
			}
			task, err := taskDTOFromWrite(resp.HandBackAgentTask.AgentTaskFields)
			if err != nil {
				return err
			}
			return writeTaskMutation(f, task, taskWarningsFor(sink, "handBackAgentTask"), func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ handed back task %s (%s)\n", taskRef(task.Handle, task.ID), task.Status); err != nil {
					return err
				}
				if task.HandBack != nil {
					_, err := fmt.Fprintf(w, "  %s\n", handBackLine(task.HandBack))
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&code, "code", "", "why: "+strings.Join(handBackCodes, " | ")+" (required)")
	cmd.Flags().StringVar(&text, "text", "", "optional account of the reason (up to 2,000 characters)")
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
