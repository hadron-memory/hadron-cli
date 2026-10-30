// Package comment implements the command layer for governed comment operations.
// All transport calls use the generated server comment operations.
package comment

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// Actor is a public identity projection; no nested user/account object is exposed.
type Actor struct {
	ID     string  `json:"id"`
	Name   *string `json:"name"`
	Handle *string `json:"handle"`
	URN    *string `json:"urn"`
}
type Author struct {
	Kind   string `json:"kind"`
	Worker *Actor `json:"worker"`
	User   *Actor `json:"user"`
	Agent  *Actor `json:"agent"`
	App    *Actor `json:"app"`
}
type Target struct {
	ID       string `json:"id"`
	URN      string `json:"urn"`
	Name     string `json:"name"`
	NodeType string `json:"nodeType"`
	Revision int    `json:"revision"`
}

// Comment is the stable CLI output contract. Nullable bodies preserve stubs.
type Comment struct {
	ID                 string  `json:"id"`
	URN                string  `json:"urn"`
	PortalURL          *string `json:"portalUrl"`
	Target             Target  `json:"target"`
	ThreadRootID       *string `json:"threadRootId"`
	IsTopLevel         bool    `json:"isTopLevel"`
	AnchorRevision     int     `json:"anchorRevision"`
	AnchorApprovalHash *string `json:"anchorApprovalHash"`
	AnchorIsCurrent    bool    `json:"anchorIsCurrent"`
	Quote              *string `json:"quote"`
	Body               *string `json:"body"`
	State              *string `json:"state"`
	Deleted            bool    `json:"deleted"`
	DeletedAt          *string `json:"deletedAt"`
	DeletedBy          *Actor  `json:"deletedBy"`
	EditedAt           *string `json:"editedAt"`
	EditedBy           *Actor  `json:"editedBy"`
	ResolvedAt         *string `json:"resolvedAt"`
	ResolvedBy         *Actor  `json:"resolvedBy"`
	Author             Author  `json:"author"`
	ProvenanceUser     *Actor  `json:"provenanceUser"`
	CreatedAt          string  `json:"createdAt"`
	UpdatedAt          *string `json:"updatedAt"`
	RevSeq             int     `json:"revSeq"`
}
type Thread struct {
	Root       Comment   `json:"root"`
	Replies    []Comment `json:"replies"`
	ReplyCount int       `json:"replyCount"`
}
type Page struct {
	Items []Thread `json:"items"`
	Total int      `json:"total"`
}

// Service is the seam for the generated adapter. Nil edit fields mean preserve;
// an explicit empty quote means clear. Implementations return mapped CLI errors.
type Service interface {
	List(context.Context, string, []string, int, int) (Page, error)
	Get(context.Context, string) (*Comment, error)
	Create(context.Context, string, string, *string, *int) (Comment, error)
	Reply(context.Context, string, string) (Comment, error)
	Edit(context.Context, string, *string, *string, int) (Comment, error)
	Delete(context.Context, string, int) (Comment, error)
	Resolve(context.Context, string, int, bool) (Comment, error)
}
type Connect func(context.Context) (Service, error)

func NewCmd(f *cmdutil.Factory, connect Connect) *cobra.Command {
	cmd := &cobra.Command{Use: "comment <command>", Short: "Read and write feedback beside a node", Long: groupLong, Example: groupExample}
	cmd.AddCommand(readCmd(f, connect, false), readCmd(f, connect, true))
	for _, verb := range []string{"create", "reply", "edit", "delete", "resolve"} {
		cmd.AddCommand(writeCmd(f, connect, verb))
	}
	return cmd
}
func readCmd(f *cmdutil.Factory, connect Connect, list bool) *cobra.Command {
	var memory string
	var states []string
	var limit, offset int
	verb := "get"
	if list {
		verb = "list"
	}
	refLabel := "<comment-ref>"
	if list {
		refLabel = "<target-ref>"
	}
	cmd := &cobra.Command{Use: verb + " " + refLabel, Short: map[bool]string{true: "List the comment threads on a node", false: "Show one comment and its write counter"}[list], Long: verbLong[verb], Example: verbExample[verb], Args: cobra.ExactArgs(1)}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to resolve a bare <loc> against")
	if list {
		cmd.Aliases = []string{"ls"}
		cmd.Flags().StringSliceVar(&states, "state", nil, "OPEN or RESOLVED (comma-separated)")
		cmd.Flags().IntVar(&limit, "limit", 20, "threads per page, 1 to 200")
		cmd.Flags().IntVar(&offset, "offset", 0, "threads to skip")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if list && cmd.Flags().Changed("state") && len(states) == 0 {
			return exitcode.Newf(exitcode.Usage, "--state must name OPEN or RESOLVED; omit it to read all states")
		}
		if list && (limit < 1 || limit > 200 || offset < 0) {
			return exitcode.Newf(exitcode.Usage, "--limit must be 1 to 200 and --offset nonnegative")
		}
		for _, state := range states {
			if state != "OPEN" && state != "RESOLVED" {
				return exitcode.Newf(exitcode.Usage, "--state must be OPEN or RESOLVED")
			}
		}
		ref, err := cmdutil.BatchNodeRef(memory, args[0])
		if err != nil {
			return err
		}
		svc, err := connect(cmd.Context())
		if err != nil {
			return err
		}
		if !list {
			c, err := svc.Get(cmd.Context(), ref)
			if err != nil {
				return err
			}
			if c == nil {
				return exitcode.Newf(exitcode.NotFound, "comment not found or not visible")
			}
			return printComment(f, *c)
		}
		page, err := svc.List(cmd.Context(), ref, states, limit, offset)
		if err != nil {
			return err
		}
		if page.Items == nil {
			page.Items = []Thread{}
		}
		for i := range page.Items {
			if page.Items[i].Replies == nil {
				page.Items[i].Replies = []Comment{}
			}
		}
		return output.Write(f.IOStreams, f.JSON, page, func(w io.Writer) error {
			if _, err := fmt.Fprintf(w, "Threads: %d (showing %d from offset %d)\n", page.Total, len(page.Items), offset); err != nil {
				return err
			}
			for _, thread := range page.Items {
				if err := render(w, thread.Root); err != nil {
					return err
				}
				if thread.ReplyCount > len(thread.Replies) {
					if _, err := fmt.Fprintf(w, "Replies: %d (showing the oldest %d; later replies are not available through this API)\n", thread.ReplyCount, len(thread.Replies)); err != nil {
						return err
					}
				}
				for _, reply := range thread.Replies {
					if err := render(w, reply); err != nil {
						return err
					}
				}
			}
			return nil
		})
	}
	return cmd
}
func writeCmd(f *cmdutil.Factory, connect Connect, verb string) *cobra.Command {
	var memory, body, bodyFile, quote string
	var expected, anchor int
	var reopen, yes bool
	refLabel := "<comment-ref>"
	if verb == "create" {
		refLabel = "<target-ref>"
	}
	cmd := &cobra.Command{Use: verb + " " + refLabel, Short: map[string]string{"create": "Start a comment thread on a node", "reply": "Reply in a comment thread", "edit": "Change the text or quote of your comment", "delete": "Delete a comment (yours, or any as a memory manager)", "resolve": "Mark a thread resolved, or reopen it"}[verb], Long: verbLong[verb], Example: verbExample[verb], Args: cobra.ExactArgs(1)}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to resolve a bare <loc> against")
	text := verb == "create" || verb == "reply" || verb == "edit"
	guarded := verb == "edit" || verb == "delete" || verb == "resolve"
	if verb == "delete" {
		cmd.Flags().BoolVar(&yes, "yes", false, "delete without asking (required when not on a terminal)")
	}
	if text {
		cmd.Flags().StringVar(&body, "body", "", "comment text (Markdown), or - to read it from a pipe")
		cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the comment text (Markdown) from a file")
		cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	}
	if verb == "create" || verb == "edit" {
		usage := "the passage of the note the comment is about (plain text)"
		if verb == "edit" {
			usage = `a new quote (plain text); "" removes the quote`
		}
		cmd.Flags().StringVar(&quote, "quote", "", usage)
	}
	if verb == "create" {
		cmd.Aliases = []string{"add"}
		cmd.Flags().IntVar(&anchor, "anchor-revision", 0, "attach to an earlier revision N of the node (default: its current revision)")
	}
	if guarded {
		cmd.Flags().IntVar(&expected, "expected-revision", 0, "the comment's current write counter, from comment get (required)")
	}
	if verb == "resolve" {
		cmd.Flags().BoolVar(&reopen, "reopen", false, "reopen a resolved thread instead")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		changed := cmd.Flags().Changed
		if guarded && (!changed("expected-revision") || expected < 1) {
			return exitcode.Newf(exitcode.Usage, "--expected-revision must name the positive comment write counter you read")
		}
		if verb == "create" && changed("anchor-revision") && anchor < 1 {
			return exitcode.Newf(exitcode.Usage, "--anchor-revision must be positive")
		}
		var bp, qp *string
		var ap *int
		if text {
			if changed("body") && changed("body-file") {
				return exitcode.Newf(exitcode.Usage, "--body and --body-file are mutually exclusive")
			}
			supplied := changed("body") || changed("body-file")
			if !supplied && verb != "edit" {
				return exitcode.Newf(exitcode.Usage, "pass --body or --body-file")
			}
			if supplied {
				if body == "-" && changed("body") {
					if err := cmdutil.RefuseDocumentStdinFromTerminal(f.IOStreams.IsInputTerminal(), "--body -", "--body-file"); err != nil {
						return err
					}
				}
				value, err := cmdutil.ResolveTextInput("body", body, bodyFile, f.IOStreams.In)
				if err != nil {
					return err
				}
				if strings.TrimSpace(value) == "" {
					return exitcode.Newf(exitcode.Usage, "comment body must not be empty")
				}
				bp = &value
			}
		}
		if (verb == "edit" || verb == "create" && quote != "") && changed("quote") {
			qp = &quote
		}
		if verb == "edit" && bp == nil && qp == nil {
			return exitcode.Newf(exitcode.Usage, "nothing to edit — pass --body, --body-file or --quote")
		}
		if verb == "create" && changed("anchor-revision") {
			ap = &anchor
		}
		ref, err := cmdutil.BatchNodeRef(memory, args[0])
		if err != nil {
			return err
		}
		if verb == "delete" {
			if err := cmdutil.Confirm(f.IOStreams, yes, "Delete comment "+ref+"? Its text is removed and cannot be restored."); err != nil {
				return err
			}
		}
		svc, err := connect(cmd.Context())
		if err != nil {
			return err
		}
		var c Comment
		switch verb {
		case "create":
			c, err = svc.Create(cmd.Context(), ref, *bp, qp, ap)
		case "reply":
			c, err = svc.Reply(cmd.Context(), ref, *bp)
		case "edit":
			c, err = svc.Edit(cmd.Context(), ref, bp, qp, expected)
		case "delete":
			c, err = svc.Delete(cmd.Context(), ref, expected)
		case "resolve":
			c, err = svc.Resolve(cmd.Context(), ref, expected, reopen)
		}
		if err != nil {
			return err
		}
		return printComment(f, c)
	}
	return cmd
}
func printComment(f *cmdutil.Factory, c Comment) error {
	return output.Write(f.IOStreams, f.JSON, c, func(w io.Writer) error { return render(w, c) })
}
func render(w io.Writer, c Comment) error {
	state := "reply"
	if c.State != nil {
		state = *c.State
	}
	if c.Deleted {
		state = "deleted"
	}
	freshness := "current"
	if !c.AnchorIsCurrent {
		freshness = "older target text"
	}
	if _, err := fmt.Fprintf(w, "%s — %s; author %s; write counter %d; on target revision %d (%s)\n", c.URN, state, authorLabel(c.Author), c.RevSeq, c.AnchorRevision, freshness); err != nil {
		return err
	}
	if c.Author.Kind == "WORKER" && c.ProvenanceUser != nil {
		if _, err := fmt.Fprintf(w, "  session bound by %s (provenance)\n", actorLabel(c.ProvenanceUser)); err != nil {
			return err
		}
	}
	if c.Deleted {
		return nil
	}
	if c.EditedAt != nil {
		if _, err := fmt.Fprintf(w, "Edited: %s by %s\n", *c.EditedAt, actorLabel(c.EditedBy)); err != nil {
			return err
		}
	}
	if c.Quote != nil {
		if _, err := fmt.Fprintf(w, "Quote: %s\n", *c.Quote); err != nil {
			return err
		}
	}
	if c.Body != nil {
		_, err := fmt.Fprintln(w, *c.Body)
		return err
	}
	return nil
}

func actorLabel(ref *Actor) string {
	if ref == nil {
		return "unknown"
	}
	if ref.Name != nil && *ref.Name != "" {
		return *ref.Name
	}
	if ref.Handle != nil && *ref.Handle != "" {
		return "@" + *ref.Handle
	}
	return ref.ID
}

func authorLabel(a Author) string {
	switch a.Kind {
	case "WORKER":
		return actorLabel(a.Worker) + " (worker)"
	case "USER_AGENT":
		return actorLabel(a.User) + " / " + actorLabel(a.Agent) + " (user + agent)"
	case "APP":
		return actorLabel(a.App) + " (App)"
	case "USER":
		return actorLabel(a.User) + " (user)"
	default:
		return "unknown"
	}
}
