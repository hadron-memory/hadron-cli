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
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdTeamTask(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task <command>",
		Short: "A team App's agent tasks",
		Long: `Create, carry and finish the team App's agent tasks — the server owns the
lifecycle (cor:agt:050:02: BACKLOG ⇄ PENDING → STARTED → FINISHED/CANCELLED),
matching and every gate. These commands are thin callers.

With a worktree worker-session binding, writes are made by the bound WORKER
(the server records the session); without one, by you. Task ids come from
` + "`task create`" + `, ` + "`task list`" + ` or ` + "`task tree`" + `.

A party is named worker:<id, URN or name> or user:<id or handle> — the kind
prefix is required: a worker's name and a person's handle are both bare
words, so a bare value is ambiguous and refused rather than guessed.`,
	}
	cmd.AddCommand(newCmdTeamTaskCreate(f))
	cmd.AddCommand(newCmdTeamTaskAssign(f, "assign", "Assign a queued task", false))
	cmd.AddCommand(newCmdTeamTaskAssign(f, "reassign", "Reassign an assigned task", true))
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
	if _, err := fmt.Fprintf(w, "✓ %s task %s (%s)\n", verb, task.ID, task.Status); err != nil {
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
	var tags []string
	var requireRole, familyIn, familyNotIn string
	var excludeWorkers []string
	var noCloseSession bool
	cmd := &cobra.Command{
		Use:   "create <title>",
		Short: "Create a task",
		Long: `Create an agent task in the team App. Without --assignee the task is
QUEUED (pending, no assignee) for the platform's matcher; naming an assignee
puts it in their queue. --parent makes a child task and takes the App from
the parent (an --app that disagrees is refused by the server).

--status parks the task instead of queueing it (cor:agt:050:02): BACKLOG
takes no assignee (the server refuses the combination), never enters the
queue, is never matched or waited on, and raises no alarms; assigning it
(` + "`task assign`" + `) or making it ready (` + "`task ready`" + `) moves it to PENDING. The
default is pending, so the flag is only needed to say backlog — but
pending is accepted for explicitness.

--owner defaults to you; a root task's creator may name any other
participating worker or person (#1757, cor:agt:050:01). For a child task
the owner is the parent's assignee — naming a different owner is refused
(TASK_CHILD_OWNER_MISMATCH). Requirements (--require-role,
--family-in/--family-not-in, --exclude-worker) constrain the matcher.
--start-by takes an RFC 3339 timestamp.

By default finishing or cancelling asks the assignee to end its worker
session, and the platform ends it through the worker's live host after a
10-minute grace period (cor:agt:050:10); declining the request does not
prevent that. The choice cannot be changed in place afterwards, so when the
assignee's session must outlive the task (a long-lived reviewer or seat),
pass --no-close-session at create.`,
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
			input := gen.CreateAgentTaskInput{
				Title:        title,
				Description:  optStr(description),
				ParentTaskId: optStr(parent),
				Source:       optStr(source),
				StartBy:      optStr(startBy),
				Status:       createStatus,
				Tags:         tags,
			}
			if noCloseSession {
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
				return api.MapError(err)
			}
			task := taskDTOFrom(resp.CreateAgentTask.AgentTaskFields)
			return writeTaskMutation(f, task, taskWarningsFor(sink, "createAgentTask"), func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ created task %s (%s)\n", task.ID, task.Status); err != nil {
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
	cmd.Flags().StringSliceVar(&excludeWorkers, "exclude-worker", nil, "worker the matcher must not pick (repeatable)")
	cmd.Flags().StringVar(&status, "status", "", "create-time status: backlog (parked) or pending (the default)")
	cmd.Flags().BoolVar(&noCloseSession, "no-close-session", false, "finishing or cancelling does not ask the assignee to end its worker session")
	return cmd
}

// ---- assign / reassign ----------------------------------------------------

func newCmdTeamTaskAssign(f *cmdutil.Factory, verb string, short string, reassign bool) *cobra.Command {
	var assignee string
	var closeSession, noCloseSession bool
	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s <taskId> --assignee <party>", verb),
		Short: short,
		Long: short + `

--assignee is required (worker:<id, URN or name> or user:<id or handle>).
--close-session/--no-close-session replace the task's close-when-done choice
(cor:agt:050:10); with neither, the task keeps its current value. Spell the
choice as one of the two flags: ` + "`--close-session=false`" + ` is indistinguishable from absence here and also means "keep the current
value". The flags ride on a real assignment: there is no in-place setter,
and reassigning a task to its current assignee is refused
(TASK_INVALID_TRANSITION) with the choice unchanged. A task whose assignee's
session must stay open is created with --no-close-session at create. The
server refuses a queued task to reassign and an assigned one to assign
(TASK_INVALID_TRANSITION).`,
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
					return api.MapError(err)
				}
				task = taskDTOFrom(resp.ReassignAgentTask.AgentTaskFields)
			} else {
				resp, err := gen.AgentTaskAssign(mctx, client, taskId, party, closeWhenDone)
				if err != nil {
					return api.MapError(err)
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

// ---- start / progress / cancel -------------------------------------------

// newSimpleTaskMutation builds a one-argument verb whose whole body is one
// mutation returning the task; receipt renders its human line.
func newSimpleTaskMutation(
	f *cmdutil.Factory,
	verb, short, long string,
	call func(context.Context, graphql.Client, string) (taskDTO, error),
	receipt func(io.Writer, taskDTO) error,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s <taskId>", verb),
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskId, err := taskRefArg(args)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			task, err := call(cmd.Context(), client, taskId)
			if err != nil {
				return api.MapError(err)
			}
			return output.Write(f.IOStreams, f.JSON, task, func(w io.Writer) error {
				return receipt(w, task)
			})
		},
	}
	return cmd
}

func newCmdTeamTaskStart(f *cmdutil.Factory) *cobra.Command {
	return newSimpleTaskMutation(f, "start", "Start a pending task",
		"Start the task: its assignee only (cor:agt:050:08). Records task.started.",
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
	return newSimpleTaskMutation(f, "progress", "Record progress on a started task",
		`Record progress on the task: its assignee only. Carries no text — it
resets the no-progress stall alarm's clock (cor:agt:050:15) and writes
task.progress to the event stream.`,
		func(ctx context.Context, client graphql.Client, taskId string) (taskDTO, error) {
			resp, err := gen.AgentTaskProgress(ctx, client, taskId)
			if err != nil {
				return taskDTO{}, err
			}
			return taskDTOFrom(resp.RecordAgentTaskProgress.AgentTaskFields), nil
		},
		func(w io.Writer, task taskDTO) error {
			_, err := fmt.Fprintf(w, "✓ recorded progress on task %s (%s)\n", task.ID, task.Status)
			return err
		})
}

func newCmdTeamTaskCancel(f *cmdutil.Factory) *cobra.Command {
	return newSimpleTaskMutation(f, "cancel", "Cancel a task",
		"Cancel the task: its owner, a worker the owner reports to, or whoever manages the App (cor:agt:050:09).\nCANCELLED is an end state.",
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
	return newSimpleTaskMutation(f, "park", "Park a queued task (BACKLOG)",
		`Park the task: a pending task with no assignee becomes BACKLOG —
unassigned, never matched, never waited on, no alarms (cor:agt:050:02).
Whoever may assign a task may park it: its owner, a worker the owner
reports to, or whoever manages the App (cor:agt:050:09). Make it ready
(`+"`team task ready`"+`) — or assign it — to move it back to PENDING.`,
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
	return newSimpleTaskMutation(f, "ready", "Make a backlog task ready to work",
		`Make the backlog task ready: BACKLOG becomes PENDING and the task
enters the queue for the matcher (cor:agt:050:02). Whoever may assign a
task may make it ready: its owner, a worker the owner reports to, or
whoever manages the App (cor:agt:050:09).`,
		func(ctx context.Context, client graphql.Client, taskId string) (taskDTO, error) {
			resp, err := gen.AgentTaskReady(ctx, client, taskId)
			if err != nil {
				return taskDTO{}, err
			}
			return taskDTOFrom(resp.MakeAgentTaskReady.AgentTaskFields), nil
		},
		func(w io.Writer, task taskDTO) error {
			_, err := fmt.Fprintf(w, "✓ made task %s ready (%s)\n", task.ID, task.Status)
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
		Long: `Finish the task: its assignee only (cor:agt:050:08). --outcome is one
word (letters, digits, underscore, hyphen or dot — e.g. succeeded, failed);
--summary and --evidence (a URL or node URN) are optional. FINISHED is an end
state.`,
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
				return api.MapError(err)
			}
			task := taskDTOFrom(resp.FinishAgentTask.AgentTaskFields)
			return writeTaskMutation(f, task, taskWarningsFor(sink, "finishAgentTask"), func(w io.Writer) error {
				out := ""
				if task.Result != nil {
					out = task.Result.Outcome
				}
				if _, err := fmt.Fprintf(w, "✓ finished task %s (%s)\n", task.ID, out); err != nil {
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
