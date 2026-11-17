package team

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type defectLinkDTO struct {
	TaskID           string  `json:"taskId"`
	Defect           string  `json:"defect"`
	IntroducedAtHead *string `json:"introducedAtHead"`
	EventID          string  `json:"eventId"`
	LinkedAt         string  `json:"linkedAt"`
	Created          bool    `json:"created"`
}

func newCmdTaskLinkDefect(f *cmdutil.Factory) *cobra.Command {
	var defect, head string
	cmd := &cobra.Command{
		Use:   "link-defect <pr-build-task-ref> --defect <issue URL>",
		Short: "Link a post-merge defect to its PR build task",
		Long: `Record a post-merge defect against a PR build task through
linkDefectToPullRequest (server #4280). The server owns admission and value
checks. Follows built server behavior; cor:agt:130:05 is Proposed, not a
confirmed minted citation.

--defect is required: the defect's issue URL. --introduced-at-head optionally
names a full 40-hex head already frozen on that PR; omitted sends no head.
Task IDs and qualified handles pass through; --app qualifies a bare task
handle using the usual team task rules. No task/PR lookup is added.

A bound QA/coordinator worker acts through its existing verified worktree
session. An unbound person must manage the App. This command never sends an
instruction and does not expose that instruction-based authority path.
It sends once; a refusal is never retried as the person.

Success prints the returned task, defect, introduced head (or none), event id
and linked-at time. An existing pair says "already linked" and exits 0 with
the original link. --json contains taskId, defect, introducedAtHead (null
when absent), eventId, linkedAt and created, exactly as returned.

DEFECT_LINK_FORBIDDEN: exit 8 (may read but may not link).
TASK_NOT_FOUND: exit 4, identical for missing, foreign-App or unreadable tasks.
PR_REFERENCE_MISSING and DEFECT_HEAD_NOT_FROZEN: exit 5; correct the task's
PR reference or choose a frozen head. BAD_USER_INPUT: exit 2; correct the
defect URL/head form. Empty --defect is refused locally before any request.
Unknown transport outcomes follow shared error mapping; never resend blindly.
A link changes no verdict, review seat, readiness or task lifecycle.`,
		Example: "  hadron team task link-defect AT1.2 --defect https://github.com/acme/project/issues/17\n  hadron team task link-defect AT1.2 --defect https://github.com/acme/project/issues/17 --introduced-at-head 0123456789abcdef0123456789abcdef01234567 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(defect) == "" {
				return exitcode.Newf(exitcode.Usage, "--defect requires an issue URL (an empty variable?): pass --defect <issue URL>")
			}
			ref, err := taskRefArg(f, args)
			if err != nil {
				return err
			}
			var introduced *string
			if cmd.Flags().Changed("introduced-at-head") {
				introduced = &head
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.LinkDefectToPullRequest(cmd.Context(), client, ref, defect, introduced)
			if err != nil {
				return mapPRError(err)
			}
			v := resp.LinkDefectToPullRequest
			if v == nil || v.TaskId == "" || v.Defect == "" || v.EventId == "" || v.LinkedAt == "" {
				return exitcode.Newf(exitcode.Error, "server returned an incomplete defect-link receipt; outcome unknown, do not resend blindly")
			}
			dto := defectLinkDTO{v.TaskId, v.Defect, v.IntroducedAtHead, v.EventId, v.LinkedAt, v.Created}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				verb := "linked"
				if !dto.Created {
					verb = "already linked"
				}
				shownHead := "none"
				if dto.IntroducedAtHead != nil {
					shownHead = *dto.IntroducedAtHead
				}
				_, err := fmt.Fprintf(w, "%s defect\n  task: %s\n  defect: %s\n  introduced head: %s\n  event: %s\n  linked at: %s\n", verb, output.SafeCell(dto.TaskID), output.SafeCell(dto.Defect), output.SafeCell(shownHead), output.SafeCell(dto.EventID), output.SafeCell(dto.LinkedAt))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&defect, "defect", "", "required defect issue URL (validated by the server)")
	cmd.Flags().StringVar(&head, "introduced-at-head", "", "optional full 40-hex head already frozen on this PR")
	_ = cmd.MarkFlagRequired("defect")
	return cmd
}
