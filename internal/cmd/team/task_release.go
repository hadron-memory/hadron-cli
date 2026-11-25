package team

import (
	"context"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// newCmdTeamTaskRelease is `task release` (cli#1962, hadron-server#4084,
// cor:agt:120:02): give a task's outside item back. The task keeps everything
// else; cancel keeps the item too.
func newCmdTeamTaskRelease(f *cmdutil.Factory) *cobra.Command {
	return newInstructableTaskMutation(f, "release", "releaseAgentTaskExternalId", "Give a task's outside item back",
		`Release the outside item a task claims (its external id, for example an
issue's URL): the task keeps its status, assignee and history, but no longer
holds the item, so the next `+"`task create --external-id`"+` naming it, or the
next `+"`team task find-untracked`"+` claim, gets it. The receipt gives the item
back. `+"`task cancel`"+` ends a task and keeps the item; release is the way to hand
the item to someone else. An id is never set again on the same task. Release
hands an outside item back; it does not move work between workers: for that use
`+"`task reassign`"+` (an assigner) or `+"`task hand-back`"+` (the assignee).

Who may: whoever may cancel the task, in any status. The assignee alone may
not: that is refused with TASK_NOT_OWNER (exit 8).

Refusals come from the server as they are: an unknown or unreadable task exits
4, an uninstalled App exits 5, and a task that holds no external id (a second
release included) is TASK_EXTERNAL_ID_NONE (exit 5). A server released before
2026-11-13 has no release call, and the call fails with its own error. The
next `+"`find-untracked`"+` reconcile pass tells the team's tracker adapter
(`+"`untrack`"+`) that the item was released.

Success prints the task and the item released; --json returns the task with a
`+"`releasedExternalId`"+` field and a warnings list ([] when none).`,
		"  hadron team task release AT0.7",
		func(ctx context.Context, client graphql.Client, taskId string, claim *gen.ActInstructionInput) (taskDTO, error) {
			// Read the item first: the release clears it. A failed read is not
			// a refusal; the mutation below answers with the server's own.
			var released string
			if pre, err := gen.IntakeTaskExternalId(ctx, client, taskId); err == nil && pre.AgentTask != nil && pre.AgentTask.ExternalId != nil {
				released = *pre.AgentTask.ExternalId
			}
			var fields gen.AgentTaskFields
			if claim != nil {
				resp, err := gen.AgentTaskReleaseInstructed(ctx, client, taskId, claim)
				if err != nil {
					return taskDTO{}, err
				}
				fields = resp.ReleaseAgentTaskExternalId.AgentTaskFields
			} else {
				resp, err := gen.AgentTaskRelease(ctx, client, taskId)
				if err != nil {
					return taskDTO{}, err
				}
				fields = resp.ReleaseAgentTaskExternalId.AgentTaskFields
			}
			dto, err := taskDTOFromWrite(fields)
			if err != nil {
				return taskDTO{}, err
			}
			dto.ReleasedExternalID = released
			return dto, nil
		},
		func(w io.Writer, task taskDTO) error {
			if err := writeTaskReceipt(w, "released the outside item of", task); err != nil {
				return err
			}
			if task.ReleasedExternalID != "" {
				_, err := fmt.Fprintf(w, "  released: %s\n", output.SafeLine(task.ReleasedExternalID, 2048))
				return err
			}
			return nil
		})
}
