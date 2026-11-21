package team

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// defectLinkDTO is the stable --json shape of `team task link-defect`: the
// server's DefectLink, all six fields, exactly as returned.
type defectLinkDTO struct {
	TaskID           string  `json:"taskId"`
	Defect           string  `json:"defect"`
	IntroducedAtHead *string `json:"introducedAtHead"`
	EventID          string  `json:"eventId"`
	LinkedAt         string  `json:"linkedAt"`
	Created          bool    `json:"created"`
}

// newCmdTaskLinkDefect is the S12b verb (hadron-cli#2189 over
// hadron-server#4280; schema-defined defect-link door): a thin client over
// linkDefectToPullRequest. The server owns who may link, which task is a PR
// build task, which heads were frozen and the idempotence; the CLI sends the
// plain arguments once, optionally adding the holder claim, and prints the receipt.
func newCmdTaskLinkDefect(f *cmdutil.Factory) *cobra.Command {
	var defect, head, rawInstruction string
	c := &cobra.Command{
		Use:   "link-defect <pr-build-task> --defect <issue URL> [--introduced-at-head <sha>] [--instruction <kind>:<ref>]",
		Short: "Link a defect found after merge to the PR build task it escaped from",
		Long: `Record that a defect found after a pull request merged traces back to that PR
(server #4280/#4284). <pr-build-task> is the PR's build task: a task with
a pull_request reference. --defect is the defect's issue URL, stored as given.
--introduced-at-head names the full 40-hex head, frozen on that PR, that
introduced the defect; the review-quality report counts the defect against
approving reviews at that head or a later one.

The link is one review.defect-linked event on the build task. It never changes
a verdict, a seat, readiness or the task. Linking the same defect and task
again writes nothing, prints the existing link as "already linked" and exits 0.

Who may link: a bound worker whose role is qa-engineer or coordinator, or a
person who manages the App; with --instruction, a worker whose holder manages
the App may also link. The App's key and a run may not.

Refusals, each sent once and never retried:
  exit 8  DEFECT_LINK_FORBIDDEN: you may read the task but may not link.
  exit 4  TASK_NOT_FOUND: no such task, a task in another App, or one you may
          not read; all three read the same.
  exit 5  PR_REFERENCE_MISSING: the task has no pull_request reference.
  exit 5  DEFECT_HEAD_NOT_FROZEN: the head was never frozen on that PR.
  exit 2  BAD_USER_INPUT: a defect URL or head in the wrong form. An empty
          --defect or --introduced-at-head is refused before any request.
  exit 1  the server answered with no link: it may have been recorded, so
          read the task's events (the message names the task) before retrying.

--json is {taskId, defect, introducedAtHead, eventId, linkedAt, created};
introducedAtHead is null when the link names no head, and created is false
for an existing link.` + "\n\n" + taskInstructionHelpFor("2026-11-16") + "\nAn unavailable or changed worktree binding sends as the signed-in person,\nwith the instruction checked but not recorded; it borrows no worker session.",
		Example: "  hadron team task link-defect AT0.7850 --defect https://github.com/acme/app/issues/77 --introduced-at-head 0123456789abcdef0123456789abcdef01234567",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(defect) == "" {
				return exitcode.Newf(exitcode.Usage, "pass --defect <issue URL>: the defect's issue")
			}
			claim, err := taskClaim(cmd, rawInstruction)
			if err != nil {
				return err
			}
			// Snapshot before client creation or any later lookup — but only
			// when there is a claim to carry: the plain path never consults
			// it, so it skips the binding read (cli#2504 F4).
			var instructed func(context.Context) (context.Context, bool, error)
			if claim != nil {
				instructed = BeginServerTargetInstructedWrite(f)(cmd.Context())
			}
			ref, err := taskRefArg(f, args)
			if err != nil {
				return err
			}
			// Only emptiness is checked here: the head's form and whether it was
			// frozen on the PR stay the server's (hadron-cli#2296).
			var headArg *string
			if cmd.Flags().Changed("introduced-at-head") {
				if strings.TrimSpace(head) == "" {
					return exitcode.Newf(exitcode.Usage, "pass --introduced-at-head <sha> with the full 40-hex head, or leave the flag out")
				}
				headArg = &head
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var l *gen.TaskLinkDefectLinkDefectToPullRequestDefectLink
			if claim == nil {
				r, e := gen.TaskLinkDefect(cmd.Context(), client, ref, defect, headArg)
				if e != nil {
					return mapFindingError(e)
				}
				if r != nil {
					l = r.LinkDefectToPullRequest
				}
			} else {
				// Prevent authored-write middleware from borrowing a new binding
				// after this command's earlier snapshot. The helper alone selects
				// the verified session; its fallback is the signed-in person.
				ctx, _, e := instructed(withAsPerson(cmd.Context()))
				if e != nil {
					return e
				}
				r, e := gen.TaskLinkDefectInstructed(ctx, client, claim, ref, defect, headArg)
				if e != nil {
					return mapFindingError(e)
				}
				if r != nil {
					l = (*gen.TaskLinkDefectLinkDefectToPullRequestDefectLink)(r.LinkDefectToPullRequest)
				}
			}
			if l == nil || l.TaskId == "" || l.EventId == "" {
				// The ref goes alone on the last line, in full: a ref can be any
				// length and hold shell metacharacters (cor:urn:010:01), e.g.
				// AT1.2;rm -rf $(x), so it is never cut short (SanitizeLine does not
				// truncate) and never spliced into a command line the CLI composed;
				// the reader quotes it when passing it back to a shell. Control
				// characters are still neutralised (hadron-cli#2329).
				return exitcode.Newf(exitcode.Error, "the link may have been recorded but the server returned no link; read the task's events before retrying, with hadron team events --task and this task ref:\n  %s", output.SanitizeLine(ref))
			}
			dto := defectLinkDTO{TaskID: l.TaskId, Defect: l.Defect, IntroducedAtHead: l.IntroducedAtHead, EventID: l.EventId, LinkedAt: l.LinkedAt, Created: l.Created}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return renderDefectLink(w, dto) })
		},
	}
	c.Flags().StringVar(&defect, "defect", "", "required: the defect's issue URL")
	c.Flags().StringVar(&head, "introduced-at-head", "", "the full 40-hex PR head, frozen on the task, that introduced the defect")
	addTaskInstructionFlag(c, &rawInstruction)
	c.Flags().Lookup("instruction").Usage = taskInstructionFlagUsageFor("2026-11-16")
	return c
}

func renderDefectLink(w io.Writer, d defectLinkDTO) error {
	verb := "✓ linked defect to task"
	if !d.Created {
		verb = "= already linked (nothing written): task"
	}
	introduced := "none"
	if d.IntroducedAtHead != nil && *d.IntroducedAtHead != "" {
		introduced = output.SafeCell(*d.IntroducedAtHead)
	}
	_, err := fmt.Fprintf(w, "%s %s\n  defect: %s\n  introduced at head: %s\n  event: %s\n  linked at: %s\n",
		verb, output.SafeCell(d.TaskID), output.SafeCell(d.Defect), introduced, output.SafeCell(d.EventID), output.SafeCell(d.LinkedAt))
	return err
}
