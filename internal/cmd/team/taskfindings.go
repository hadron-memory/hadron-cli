package team

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type findingDispositionDTO struct {
	FindingID   string            `json:"findingId"`
	Disposition string            `json:"disposition"`
	Reference   *taskReferenceDTO `json:"reference"`
}

type findingDispositionPageDTO struct {
	Items   []findingDispositionDTO `json:"items"`
	Total   int                     `json:"total"`
	HasMore bool                    `json:"hasMore"`
}

type findingDispositionReceiptDTO struct {
	TaskID string `json:"taskId"`
	findingDispositionDTO
}

func findingDispositionFrom(v gen.TaskFindingDispositionFields) findingDispositionDTO {
	d := findingDispositionDTO{FindingID: v.FindingId, Disposition: string(v.Disposition)}
	if r := v.Reference; r != nil {
		d.Reference = &taskReferenceDTO{Kind: string(r.Kind), Target: r.Target, Label: r.Label}
	}
	return d
}

func renderFindingDispositions(w io.Writer, rows []findingDispositionDTO) error {
	t := output.NewTable(w, "FINDING", "DISPOSITION", "REF KIND", "TARGET", "LABEL")
	for _, d := range rows {
		kind, target, label := "—", "—", "—"
		if r := d.Reference; r != nil {
			kind, target = output.SafeCell(r.Kind), output.SafeCell(r.Target)
			if r.Label != nil {
				label = output.SafeCell(*r.Label)
			}
		}
		t.Row(output.SafeCell(d.FindingID), output.SafeCell(d.Disposition), kind, target, label)
	}
	return t.Flush()
}

// findingDispositionSharedDoc is the paragraph both disposition pages share: who
// may read, how a task is named, the read refusal, and how output is rendered.
const findingDispositionSharedDoc = `Sign in first. The task may be an id, a handle or a URN; it decides the App, so
--app does not qualify an ambiguous handle (use an id, or a qualified handle such
as hrn:agenttask:example.com:team:AT1.2). A missing or unreadable task exits 4.
A server without finding dispositions refuses the command.

Human output bounds values and neutralizes terminal controls. JSON retains
original text, including reference kind, target and nullable label.`

// findingWords says once what a finding and a disposition are; every findings
// page opens with it so none of them has to be read first.
const findingWords = `A finding is an issue noted when a task finished (a reviewer's defect, an
evaluator's failing page), with an id such as F1, a severity (P1, P2, P3 or
note) and text; findings are written once and never change. A disposition is
what became of one: fixed, accepted, deferred or rejected, with an optional
link.`

// findingDispositionWrite is the authority and refusal paragraph of the write.
const findingDispositionWrite = `Only the task's assigners may record a disposition, and only after the task
finished (never the assignee alone, an App key or an impersonation); the server
judges it. Refusals: TASK_NOT_OWNER exits 8, TASK_NOT_FINISHED exits 5,
TASK_FINDING_NOT_FOUND exits 4, BAD_USER_INPUT exits 2. No refusal is retried.
A transport failure may have committed: list the current dispositions before
sending another write.`

func newCmdTaskFindingDispositions(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	c := &cobra.Command{
		Use: "finding-dispositions <task>", Short: "Read a page of the latest finding dispositions",
		Long: `List what became of a finished task's findings: for each finding its id, its
disposition (fixed, accepted, deferred or rejected) and the link recorded with
it, if any. It changes nothing. The findings themselves are listed by "hadron
team task findings <task>"; "hadron team task dispose-finding" records or
replaces a disposition.

` + findingWords + `

A page is in finding order; --limit is its size (default 20, max 200; 0 prints
only the counts) and --offset starts later. Follow hasMore with --offset: an
empty or short page does not mean the list is complete. --json is {items:
[{findingId, disposition, reference}], total, hasMore}; an empty list is [],
reference is null when absent.

` + findingDispositionSharedDoc,
		Example: "  hadron team task finding-dispositions AT1.2 --limit 20 --offset 20 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			if limit < 0 || limit > 200 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit takes 0 to 200 and --offset must be non-negative")
			}
			ref, err := taskRefArg(f, a)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.AgentTaskFindingDispositions(c.Context(), client, ref, limit, offset)
			if err != nil {
				return mapFindingError(err)
			}
			if r.AgentTask == nil {
				return exitcode.Newf(exitcode.NotFound, "task not found")
			}
			p := r.AgentTask.FindingDispositions
			if p == nil {
				return exitcode.Newf(exitcode.Error, "finding disposition page unavailable")
			}
			d := findingDispositionPageDTO{Items: []findingDispositionDTO{}, Total: p.Total, HasMore: p.HasMore}
			for _, item := range p.Items {
				if item != nil {
					d.Items = append(d.Items, findingDispositionFrom(item.TaskFindingDispositionFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				if err := renderFindingDispositions(w, d.Items); err != nil {
					return err
				}
				_, err := fmt.Fprintf(w, "%d shown of %d; more: %t\n", len(d.Items), d.Total, d.HasMore)
				return err
			})
		},
	}
	c.Flags().IntVar(&limit, "limit", 20, "page size (0 count-only, default 20, max 200)")
	c.Flags().IntVar(&offset, "offset", 0, "offset in finding order")
	return c
}

func newCmdTaskDisposeFinding(f *cmdutil.Factory) *cobra.Command {
	var code, reference, rawInstruction string
	c := &cobra.Command{
		Use: "dispose-finding <task> <finding-id>", Short: "Record or replace one finding's disposition",
		Long: `Record what became of one finding on a finished task: fixed, accepted,
deferred or rejected, optionally with a link (the fixing PR, a follow-up issue).
List the findings and their ids with "hadron team task findings <task>", and the
current dispositions with "hadron team task finding-dispositions <task>".

` + findingWords + `

--disposition is required. --ref is optional: kind=target[|label], with kind
issue, pull-request, node, task or url; a label starts at the first |, and the
link is stored as text, never fetched. Writing again replaces the earlier
disposition (the latest wins; there is no clear), and omitting --ref on a
replacement drops the old link. Any of the four codes counts as resolving the
finding. The result itself is never changed. It prints the disposition the
server now holds; --json is {taskId, findingId, disposition, reference}
(reference null when absent).

` + findingDispositionSharedDoc + `

` + findingDispositionWrite + `

` + taskInstructionHelpShort("ba077810"),
		Example: "  hadron team task dispose-finding AT1.2 F1 --disposition deferred --ref 'issue=https://example.com/issues/7|Follow-up' --json",
		Args:    cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, a []string) error {
			claim, err := taskClaim(c, rawInstruction)
			if err != nil {
				return err
			}
			ref, err := taskRefArg(f, a[:1])
			if err != nil {
				return err
			}
			if strings.TrimSpace(a[1]) == "" {
				return exitcode.Newf(exitcode.Usage, "pass a finding id")
			}
			switch code {
			case "fixed", "accepted", "deferred", "rejected":
			default:
				return exitcode.Newf(exitcode.Usage, "--disposition takes fixed, accepted, deferred or rejected")
			}
			var refIn *gen.AgentTaskReferenceInput
			if c.Flags().Changed("ref") {
				refs, err := parseTaskRefFlags([]string{reference})
				if err != nil {
					return err
				}
				refIn = refs[0]
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Either document answers the same shape; read it into one. The
			// readback is the one targeted field for the finding just written
			// (cli#1709): the latest disposition when the field resolved, not an
			// atomic receipt of this commit, so a concurrent replacement may
			// show here. Never a scan of the paged list, never a second request.
			var taskID string
			var got *gen.TaskFindingDispositionFields
			if claim != nil {
				r, err := gen.AgentTaskDisposeFindingInstructed(c.Context(), client, ref, a[1], gen.AgentTaskFindingDispositionCode(code), refIn, claim)
				if err != nil {
					return disposeFindingError(err)
				}
				if d := r.DisposeAgentTaskFinding; d != nil {
					taskID = d.Id
					if d.FindingDisposition != nil {
						got = &d.FindingDisposition.TaskFindingDispositionFields
					}
				}
			} else {
				r, err := gen.AgentTaskDisposeFinding(c.Context(), client, ref, a[1], gen.AgentTaskFindingDispositionCode(code), refIn)
				if err != nil {
					return disposeFindingError(err)
				}
				if d := r.DisposeAgentTaskFinding; d != nil {
					taskID = d.Id
					if d.FindingDisposition != nil {
						got = &d.FindingDisposition.TaskFindingDispositionFields
					}
				}
			}
			// Never fabricate a receipt from arguments: the readback must name
			// a task id, this exact finding id (no trimming or case folding),
			// one of the four codes, and a complete reference when it has one.
			if taskID != "" && validFindingReadback(got, a[1]) {
				d := findingDispositionReceiptDTO{TaskID: taskID, findingDispositionDTO: findingDispositionFrom(*got)}
				return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
					if _, err := fmt.Fprintf(w, "task: %s\n", output.SafeCell(d.TaskID)); err != nil {
						return err
					}
					return renderFindingDispositions(w, []findingDispositionDTO{d.findingDispositionDTO})
				})
			}
			return errDispositionReadbackUnavailable()
		},
	}
	c.Flags().StringVar(&code, "disposition", "", "required: fixed, accepted, deferred or rejected")
	c.Flags().StringVar(&reference, "ref", "", "optional kind=target[|label]; omission replaces any prior reference with none")
	addTaskInstructionFlag(c, &rawInstruction)
	return c
}

func errDispositionReadbackUnavailable() error {
	return exitcode.Newf(exitcode.Error, "disposition may have been recorded but its readback is unavailable; read finding-dispositions before retrying")
}

// disposeFindingError maps a failed dispose-finding call. The post-write error
// classes, each exit 1 with the same recovery guidance as a missing readback
// and never a claim that the write failed (cli#1709, only this verb):
//   - a success body the client cannot decode (wrong field type, malformed,
//     blank or truncated JSON): it does not establish whether the write
//     executed, and an EOF may also come from the transport, so it is treated
//     as ambiguous on purpose;
//   - a GraphQL error beneath the write's result (id, findingDisposition),
//     whatever the HTTP status.
//
// Typed server refusals, pre-execution validation refusals and gateway or
// transport failures keep their usual mapping.
func disposeFindingError(err error) error {
	// A typed refusal on the write field is definitive (the write did not
	// happen) and wins, in either order, even beside a readback error: map that
	// error alone so its code, and so its exit status, survive.
	if refusal := writeRefusal(err); refusal != nil {
		return mapFindingError(refusal)
	}
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	if errors.As(err, &typeErr) || errors.As(err, &syntaxErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || readbackFieldFailed(err) {
		return errDispositionReadbackUnavailable()
	}
	return mapFindingError(err)
}

// gqlErrorsOf returns the GraphQL error list an error carries and, for a
// non-200 answer, the HTTP wrapper to rebuild it in.
func gqlErrorsOf(err error) (gqlerror.List, *graphql.HTTPError) {
	var httpErr *graphql.HTTPError
	var list gqlerror.List
	var one *gqlerror.Error
	switch {
	case errors.As(err, &httpErr):
		return httpErr.Response.Errors, httpErr
	case errors.As(err, &list):
		return list, nil
	case errors.As(err, &one):
		return gqlerror.List{one}, nil
	}
	return nil, nil
}

// isWriteRefusal reports a typed refusal of the write field itself: an error
// with no path, or whose path stops at disposeAgentTaskFinding, that carries one
// of the task refusal codes (TASK_NOT_OWNER, TASK_NOT_FINISHED,
// TASK_FINDING_NOT_FOUND, BAD_USER_INPUT, ...; taskErrorCodes). Only such a code
// says the write was refused before it committed. A root-path error without one
// (generic, INTERNAL_SERVER_ERROR, any message) only names the field that
// failed: it does not say the transaction rolled back, so it is not a refusal.
func isWriteRefusal(e *gqlerror.Error) bool {
	if e == nil || len(e.Path) > 1 {
		return false
	}
	if len(e.Path) == 1 {
		if root, _ := e.Path[0].(ast.PathName); root != "disposeAgentTaskFinding" {
			return false
		}
	}
	code, _ := e.Extensions["code"].(string)
	return slices.Contains(taskErrorCodes, code)
}

// writeRefusal returns err reduced to its first write-field refusal (keeping
// the HTTP status for a non-200 answer), or nil when the list has none.
func writeRefusal(err error) error {
	list, httpErr := gqlErrorsOf(err)
	for _, e := range list {
		if isWriteRefusal(e) {
			if httpErr != nil {
				return &graphql.HTTPError{StatusCode: httpErr.StatusCode, Response: graphql.Response{Errors: gqlerror.List{e}}}
			}
			return gqlerror.List{e}
		}
	}
	return nil
}

// readbackFieldFailed reports the unconfirmed-write class: a GraphQL error whose
// path runs through disposeAgentTaskFinding (the write field itself, or the
// receipt fields id, findingDisposition and anything beneath them) that is not
// a typed refusal of the write. A path names the field that failed; it does not
// say the transaction rolled back, so only a typed refusal code
// (isWriteRefusal) shows the write did not happen, and it wins over everything
// else in the list. Whatever else the list carries (pathless or foreign-root
// errors) does not change the class, at any HTTP status. An empty list, pathless
// validation refusals and foreign roots are not this class. Gateway and
// transport failures are classified earlier, by the transport layer, with their
// own may-have-been-applied advice.
func readbackFieldFailed(err error) bool {
	list, _ := gqlErrorsOf(err)
	unconfirmed := false
	for _, e := range list {
		if e == nil {
			continue
		}
		if isWriteRefusal(e) {
			return false // the write was refused: typed mapping, handled first
		}
		if len(e.Path) == 0 {
			continue // pathless (a validation refusal before execution): not this class
		}
		if root, _ := e.Path[0].(ast.PathName); root == "disposeAgentTaskFinding" {
			unconfirmed = true // the write field or a field beneath it, without a typed refusal code
		}
	}
	return unconfirmed
}

// validFindingReadback reports whether the targeted readback can be shown as the
// receipt: it is present, names exactly the finding that was written, carries
// one of the four disposition codes, and any reference has its kind and target.
func validFindingReadback(d *gen.TaskFindingDispositionFields, findingID string) bool {
	if d == nil || d.FindingId != findingID {
		return false
	}
	switch d.Disposition {
	case gen.AgentTaskFindingDispositionCodeFixed, gen.AgentTaskFindingDispositionCodeAccepted,
		gen.AgentTaskFindingDispositionCodeDeferred, gen.AgentTaskFindingDispositionCodeRejected:
	default:
		return false
	}
	if r := d.Reference; r != nil && (r.Kind == "" || r.Target == "") {
		return false
	}
	return true
}

// Keep structured serverCode on these new surfaces without changing legacy task envelopes.
// mapFindingError is a task-family mapper, not a findings-only one: it wraps
// mapTaskError, which consults taskErrorCodes, and adds the structured server
// code. The findings verbs and `task link-defect` use it (hadron-cli#2296).
func mapFindingError(err error) error { return api.WithServerCode(mapTaskError(err), err) }
