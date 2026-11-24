package team

// `team session restart` — the dark-session restart door (hadron-cli#3050 over
// hadron-server#4930, hrn:node:hadronmemory.com:specs:cor:agt:070:08 S4a and
// cor:acl:140:02). The calling worker — through its own bound session, holding
// a live worker.session.restart grant that names the target — ends the target's
// dark open session and starts it again, in one transaction, with no person in
// the loop. Dark is the caller's judgement, stated in the required reason; the
// grant is the server's admission and is never re-implemented here: no
// client-side pre-check, and no message of this command's own about the target.

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdSessionRestart(f *cmdutil.Factory) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "restart <worker> --reason <text>",
		Short: "End a dark worker session and start the worker again (needs a signed grant)",
		Long: `End the named worker's DARK open session and start it again, in one
transaction, with no person in the loop. This is the door for a session you
cannot get going or get an answer from — chat pings unanswered, the agent
silent while work is due. It is not for a merely idle session: QUIET IS NOT
DARK, silence alone never justifies ending one, and an assignment alone
never ends a session. The worker a team dedicates to manage its agents is
the caller this door is built for; whoever's bound session holds the grant
may call it — the rule names no team role.

DARK IS YOUR JUDGEMENT, and --reason states it: what was tried and why the
session is dark (at most 500 characters). The server records the reason and
never judges darkness itself.

THE CALLER IS THIS WORKTREE'S BOUND SESSION. The restart acts only through
the calling worker's own session — there is no --session escape and no
unbound spelling, so bind one first if none is bound. The bound worker must
hold a live worker.session.restart grant that a person who manages the App
signed and that names the TARGET worker (no wildcard). The grant is the
server's admission: this command adds no check of its own, and a caller
without the covering grant gets exactly one refusal,
RESTART_GRANT_REQUIRED (exit 8), before anything about the target is read —
the server's answer names nothing about the target, and neither does this
command.

<worker> takes the target worker's id, URN or name within the App. The App
is the mutation's required appRef (--app, an App context, or this binding's
App).

WHAT A SUCCESS DOES. The target's open session is ended with endReason
"ended" and NO handoff — unlike ` + "`session end`" + `, which is how a session's own
worktree ends itself and leaves continuity, this door ends one that cannot
speak, so there is nothing to hand over. The issued start carries the
worker's oldest open task, if any, and names the calling worker as its
issuer; the host collects it and the target's agent binds as for any start.
Each restart records one use of the grant (a USE event) — the grant is
repeating, so uses are not a budget, but every one is recorded. The text
receipt first names the App the restart landed in and where it was resolved
from (--app, the App context, or this binding), because a same-named worker
in a wrong ambient App is exactly the silent hazard that line exists for.

EVERY REFUSAL IS THE SERVER'S ANSWER, each with its code and exit:
  RESTART_GRANT_REQUIRED (8)  no covering grant for this target, or none
                              at all — read before the target is
  APP_NOT_FOUND (4)           the caller is no participant of the App
  WORKER_NOT_FOUND (4)        the target is gone
  WORKER_LIVE (5)             the session answers; it is not dark
  NO_OPEN_SESSION (5)         the worker has no open session to restart
  WORKER_NOT_HELD (5)         nobody holds the target worker's name
  WORKER_RETIRED (5)          the target worker is retired
  WORKER_START_PENDING (5)    a start is already in flight, so a restart
                              and a fresh assignment cannot both launch it
  NO_LIVE_HOST (5)            no live host lists the worker with a
                              worktree to start it on
  TEAM_PAUSED (5)             the team is paused
  WORKER_STOPPED (5)          the target worker is stopped
Except the grant refusal — which reads nothing about the target — every
refusal ends nothing and spends no use of the grant.

--json prints the result: worker (the ref as passed), endedSessionIds
(never null), hostId, operationId and taskId (null when the worker has no
open task).

This command ENDS a session. It is not a context reset: your own session
keeps working under ` + "`session shed`" + `, ` + "`worker release`" + ` is the only thing that
frees a held name, and ` + "`session end`" + ` is how a session's own worktree ends
itself deliberately, with a handoff.`,
		Example: `  # the dedicated worker restarts a dark agent by name (App from context)
  hadron team session restart Kit --reason "chat pings unanswered 40m; transcript tail shows no prompt since 06:02; orca terminal idle"

  # agent parses the result: the operation id is what the host collects
  hadron team session restart hrn:worker:acme.com:eng:kit --reason "unanswered 40m, no prompt, idle terminal" --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			// Blank input is refused here, before any request (#3058): a
			// whitespace-only value is no more an answer than an empty one, and
			// the refusal names only the missing input — it says nothing about
			// the target. What is sent is the trimmed text.
			workerRef := strings.TrimSpace(args[0])
			if workerRef == "" {
				return exitcode.Newf(exitcode.Usage,
					"<worker> is required: name the worker to restart by id, URN or name (a blank value, such as an unset variable, is refused)")
			}
			reason = strings.TrimSpace(reason)
			if reason == "" {
				return exitcode.Newf(exitcode.Usage,
					"--reason is required: say what was tried and why the session is dark — the server records it and judges nothing (a blank value, such as an unset variable, is refused)")
			}
			// The caller IS this worktree's bound session (S4a: the restart
			// acts only through it). The binding is the admission, so there
			// is no --session escape; `session end --session <id>` remains
			// the recovery path for a binding that is gone.
			b, _, err := readBinding(ctx)
			if err != nil {
				return err
			}
			if b == nil {
				return exitcode.Newf(exitcode.NotFound,
					"no active session in this worktree — restart acts through the calling worker's own bound session (there is no unbound spelling); bind one with `team session start`")
			}
			if err := checkBindingServer(f, b); err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			// The scope line names WHERE the restart landed and which branch
			// answered (--app / the App context / the binding): a wrong
			// ambient App restarts a same-named worker in a team nobody was
			// looking at (review:ambient-scope-must-report-its-source).
			// lazyAppLabel reads only when the human branch prints, so --json
			// pays nothing for it.
			appLabel := lazyAppLabel(ctx, f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamSessionRestart(api.WithSession(ctx, b.SessionID), client, &gen.RestartWorkerSessionInput{
				AppRef: scope.Ref,
				Reason: reason,
				Worker: workerRef,
			})
			if err != nil {
				return api.MapError(err)
			}
			// The request was sent, so a null or absent result means the
			// outcome is unknown (#2493's shape): never a success receipt,
			// never retried here.
			if resp == nil || resp.RestartWorkerSession == nil {
				return exitcode.Newf(exitcode.Error,
					"server returned no restart result; outcome unknown — read `team session list --as <worker>` before retrying")
			}
			r := resp.RestartWorkerSession
			dto := sessionRestartDTO{
				Worker:          workerRef,
				EndedSessionIds: []string{},
				HostID:          r.HostId,
				OperationID:     r.OperationId,
				TaskID:          r.TaskId,
			}
			dto.EndedSessionIds = append(dto.EndedSessionIds, r.EndedSessionIds...)
			// The documented contract is null for "no open task" (#3058): an
			// empty string from the server is the same fact, so --json says it
			// the one way the help promises.
			if dto.TaskID != nil && *dto.TaskID == "" {
				dto.TaskID = nil
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				return renderSessionRestartReceipt(w, dto)
			})
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "required: what was tried and why the session is dark (at most 500 characters)")
	return cmd
}

// sessionRestartDTO is the stable --json shape of `team session restart`
// (hadron-cli#3050): the SDL result, never the generated struct. taskId is
// the server's nullable: null when the worker has no open task for the
// start to carry.
type sessionRestartDTO struct {
	Worker          string   `json:"worker"`
	EndedSessionIds []string `json:"endedSessionIds"`
	HostID          string   `json:"hostId"`
	OperationID     string   `json:"operationId"`
	TaskID          *string  `json:"taskId"`
}

// renderSessionRestartReceipt is the human branch. Every line is the
// result, not commentary: stdout stays parseable and --json renders the
// same facts.
func renderSessionRestartReceipt(w io.Writer, dto sessionRestartDTO) error {
	// The worker ref is the caller's own echo, not server text — it renders
	// raw (review:classify-by-provenance-before-wrapping); every id below IS
	// server-supplied and keeps its SafeCell.
	if _, err := fmt.Fprintf(w, "✓ restarted %s: ended %d session(s), start issued as operation %s\n",
		dto.Worker, len(dto.EndedSessionIds), output.SafeCell(dto.OperationID)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  host %s\n", output.SafeCell(dto.HostID)); err != nil {
		return err
	}
	if dto.TaskID != nil && *dto.TaskID != "" {
		if _, err := fmt.Fprintf(w, "  task %s — the start carries the worker's oldest open task\n", output.SafeCell(*dto.TaskID)); err != nil {
			return err
		}
	} else {
		if _, err := io.WriteString(w, "  task none — the worker has no open task; the start is taskless\n"); err != nil {
			return err
		}
	}
	for _, id := range dto.EndedSessionIds {
		if _, err := fmt.Fprintf(w, "  ended session %s\n", output.SafeCell(id)); err != nil {
			return err
		}
	}
	return nil
}
