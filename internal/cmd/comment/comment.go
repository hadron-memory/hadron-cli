// Package comment implements the command layer for governed comment operations.
// Its generated GraphQL adapter is pending the server #1606 schema export.
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
	AnchorApprovalHash string  `json:"anchorApprovalHash"`
	AnchorIsCurrent    bool    `json:"anchorIsCurrent"`
	Quote              *string `json:"quote"`
	Body               *string `json:"body"`
	State              *string `json:"state"`
	Retracted          bool    `json:"retracted"`
	RetractedAt        *string `json:"retractedAt"`
	Hidden             bool    `json:"hidden"`
	HiddenAt           *string `json:"hiddenAt"`
	ResolvedAt         *string `json:"resolvedAt"`
	ResolvedBy         *Actor  `json:"resolvedBy"`
	Author             Author  `json:"author"`
	ProvenanceUser     *Actor  `json:"provenanceUser"`
	CreatedAt          string  `json:"createdAt"`
	UpdatedAt          *string `json:"updatedAt"`
	Revision           int     `json:"revision"`
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
	Retract(context.Context, string, int) (Comment, error)
	Resolve(context.Context, string, int, bool) (Comment, error)
}
type Connect func(context.Context) (Service, error)

func NewCmd(f *cmdutil.Factory, connect Connect) *cobra.Command {
	cmd := &cobra.Command{Use: "comment <command>", Short: "Read and write feedback beside a node", Long: "Comments use governed operations and anchor to a target revision. Generic node writes cannot create or edit comments. Use a node URN, a node id, or a bare loc with -m/--memory."}
	cmd.AddCommand(readCmd(f, connect, false), readCmd(f, connect, true))
	for _, verb := range []string{"create", "reply", "edit", "retract", "resolve"} {
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
	cmd := &cobra.Command{Use: verb + " <node-ref>", Short: map[bool]string{true: "List threads anchored to a target", false: "Read one comment, including a hidden or retracted stub"}[list], Args: cobra.ExactArgs(1)}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory for a bare loc")
	if list {
		cmd.Aliases = []string{"ls"}
		cmd.Flags().StringSliceVar(&states, "state", nil, "OPEN or RESOLVED (comma-separated)")
		cmd.Flags().IntVar(&limit, "limit", 50, "threads in this page")
		cmd.Flags().IntVar(&offset, "offset", 0, "threads to skip")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if list && (limit < 1 || offset < 0) {
			return exitcode.Newf(exitcode.Usage, "--limit must be positive and --offset nonnegative")
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
	var reopen bool
	cmd := &cobra.Command{Use: verb + " <node-ref>", Short: verb + " a comment", Args: cobra.ExactArgs(1)}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory for a bare loc")
	text := verb == "create" || verb == "reply" || verb == "edit"
	guarded := verb == "edit" || verb == "retract" || verb == "resolve"
	if text {
		cmd.Flags().StringVar(&body, "body", "", "Markdown body, or - for piped stdin")
		cmd.Flags().StringVar(&bodyFile, "body-file", "", "read Markdown from a file")
		cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	}
	if verb == "create" || verb == "edit" {
		cmd.Flags().StringVar(&quote, "quote", "", "plain-text quote (empty clears on edit)")
	}
	if verb == "create" {
		cmd.Aliases = []string{"add"}
		cmd.Flags().IntVar(&anchor, "anchor-revision", 0, "anchor to a specific readable target revision; omit for current")
	}
	if guarded {
		cmd.Flags().IntVar(&expected, "expected-revision", 0, "comment revision observed by the caller (required)")
	}
	if verb == "resolve" {
		cmd.Flags().BoolVar(&reopen, "reopen", false, "reopen a resolved root instead")
	}
	cmd.Long = "Use the comment's own revision for --expected-revision, not its target or anchor revision. The server decides authorship, permission and thread state. Refused writes are not retried."
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		changed := cmd.Flags().Changed
		if guarded && (!changed("expected-revision") || expected < 1) {
			return exitcode.Newf(exitcode.Usage, "--expected-revision must name the positive comment revision you read")
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
		if (verb == "create" || verb == "edit") && changed("quote") {
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
		case "retract":
			c, err = svc.Retract(cmd.Context(), ref, expected)
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
	if c.Hidden {
		state = "hidden"
	} else if c.Retracted {
		state = "retracted"
	}
	freshness := "current"
	if !c.AnchorIsCurrent {
		freshness = "older target text"
	}
	if _, err := fmt.Fprintf(w, "%s — %s; author %s; revision %d; on target revision %d (%s)\n", c.URN, state, authorLabel(c.Author), c.Revision, c.AnchorRevision, freshness); err != nil {
		return err
	}
	if c.Hidden || c.Retracted {
		return nil
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

func authorLabel(a Author) string {
	label := func(ref *Actor) string {
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
	switch a.Kind {
	case "WORKER":
		return label(a.Worker) + " (worker)"
	case "USER_AGENT":
		return label(a.User) + " / " + label(a.Agent) + " (user + agent)"
	case "APP":
		return label(a.App) + " (App)"
	case "USER":
		return label(a.User) + " (user)"
	default:
		return "unknown"
	}
}
