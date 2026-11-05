package comment

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// addDTO is the stable --json shape of comment add (cli#1690).
type addDTO struct {
	Outcome       string  `json:"outcome"` // "started" or "replied"
	ThreadID      string  `json:"threadId"`
	ComposedQuote bool    `json:"composedQuote"`
	Comment       Comment `json:"comment"`
}

// composeReplyBody folds --quote and --anchor-revision into a reply body as a
// Markdown blockquote, since replyToComment takes neither. Without either the
// body is returned byte for byte.
func composeReplyBody(body string, quote *string, anchor *int) (string, bool) {
	if quote == nil && anchor == nil {
		return body, false
	}
	var b strings.Builder
	if anchor != nil {
		fmt.Fprintf(&b, "> On revision %d:\n", *anchor)
	}
	if quote != nil {
		for _, line := range strings.Split(*quote, "\n") {
			b.WriteString("> " + line + "\n")
		}
	}
	b.WriteString("\n" + body)
	return b.String(), true
}

// addCmd is `comment add`: one call that leaves the caller's feedback on a node
// whether or not they already have an open thread there. It sends one
// createComment; only a typed COMMENT_OPEN_THREAD_EXISTS carrying a usable
// threadId turns into exactly one replyToComment. Every other failure is
// returned unchanged, with no reply and no retry.
func addCmd(f *cmdutil.Factory, connect Connect) *cobra.Command {
	var memory, body, bodyFile, quote string
	var anchor int
	cmd := &cobra.Command{
		Use:     "add <target-ref>",
		Short:   "Leave feedback on a node: start your thread or append to your open one",
		Long:    addLong,
		Example: addExample,
		Args:    cobra.ExactArgs(1),
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to resolve a bare <loc> against")
	cmd.Flags().StringVar(&body, "body", "", "comment text (Markdown), or - to read it from a pipe")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the comment text (Markdown) from a file")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	cmd.Flags().StringVar(&quote, "quote", "", "the passage of the note the comment is about (plain text)")
	cmd.Flags().IntVar(&anchor, "anchor-revision", 0, "attach to an earlier revision N of the node (default: its current revision)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		changed := cmd.Flags().Changed
		if changed("anchor-revision") && anchor < 1 {
			return exitcode.Newf(exitcode.Usage, "--anchor-revision must be positive")
		}
		bp, err := resolveBody(cmd, f, body, bodyFile, true)
		if err != nil {
			return err
		}
		var qp *string
		if quote != "" && changed("quote") {
			qp = &quote
		}
		var ap *int
		if changed("anchor-revision") {
			ap = &anchor
		}
		ref, err := cmdutil.BatchNodeRef(memory, args[0])
		if err != nil {
			return err
		}
		svc, err := connect(cmd.Context())
		if err != nil {
			return err
		}
		created, err := svc.Create(cmd.Context(), ref, *bp, qp, ap)
		if err == nil {
			if !usableID(created.ID) {
				return missingResult()
			}
			return printAdd(f, addDTO{Outcome: "started", ThreadID: created.ID, Comment: created})
		}
		thread, conflict := api.OpenThreadConflictID(err)
		if !conflict {
			return err
		}
		text, composed := composeReplyBody(*bp, qp, ap)
		replied, rerr := svc.Reply(cmd.Context(), thread, text)
		if rerr == nil && !usableID(replied.ID) {
			rerr = missingResult()
		}
		if rerr != nil {
			return api.WithThreadID(rerr, thread)
		}
		return printAdd(f, addDTO{Outcome: "replied", ThreadID: thread, ComposedQuote: composed, Comment: replied})
	}
	return cmd
}

// usableID is the minimum a success object must carry before add prints an
// outcome: a non-empty id that is safe to print. A non-null `{}` decodes to a
// zero Comment, which is not recorded feedback (cli#1700 local review).
func usableID(id string) bool {
	return id != "" && output.SafeLine(id, 256) == id
}

func printAdd(f *cmdutil.Factory, dto addDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		head := "started thread " + dto.ThreadID
		if dto.Outcome == "replied" {
			head = "replied to your open thread " + dto.ThreadID
		}
		if _, err := fmt.Fprintln(w, head); err != nil {
			return err
		}
		if dto.ComposedQuote {
			if _, err := fmt.Fprintln(w, "the quote and anchor were folded into the reply text (a reply takes neither as a field)"); err != nil {
				return err
			}
		}
		return render(w, dto.Comment)
	})
}

const addLong = `Leave feedback on a node whether or not you already have an open thread
there. It is the one command for "write this down on the node": use it at the
end of a task, where comment create would refuse a second comment with
COMMENT_OPEN_THREAD_EXISTS (exit 5).

  started    you had no open thread (including when your earlier one was
             resolved): a new thread is started, as comment create does.
  replied    you already had an open thread: your text is added to it as a
             reply, the way comment reply does.

It takes the same flags as comment create: --body "...", --body - (from a
pipe) or --body-file <path> (one is required), -m/--memory for a bare loc,
--quote and --anchor-revision. A reply has no quote or anchor field, so on the
reply path they are written into the reply's first lines as a Markdown
blockquote ("> On revision N:", then each quote line prefixed "> "); the root
comment is not edited, and the output says so (--json composedQuote: true).

It sends one createComment and, only when the server answers with the typed
open-thread conflict and the thread's id, exactly one reply. Any other
refusal or failure is returned unchanged (same exit code), with no reply and
no retry. If your thread is deleted or you lose access between the two calls,
the reply's refusal is returned: its exit code, with the thread named in the
message and in the --json error (extensions.threadId). If your thread is
resolved in that same window, the reply is recorded and the thread stays
resolved: add guarantees the feedback is recorded on the node, not that it is
listed as unresolved.

` + refsHelp + `

On success the first line is "started thread <id>" or "replied to your open
thread <id>", then the comment as comment get shows it. --json prints
{outcome, threadId, composedQuote, comment}; threadId is the new comment's id
when started and your existing thread's id when replied.`

const addExample = `  hadron comment add onboarding -m hrn:mem:acme.com:kb --body "Step 3 is out of date"
  hadron comment add hrn:node:acme.com:kb:onboarding --body-file notes.md --json
  echo "Still wrong in revision 4." | hadron comment add hrn:node:acme.com:kb:onboarding --body -`
