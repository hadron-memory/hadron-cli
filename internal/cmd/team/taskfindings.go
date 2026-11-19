package team

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
			// Either document answers the same shape; read it into one.
			var taskID string
			var items []gen.TaskFindingDispositionFields
			var haveReadback bool
			if claim != nil {
				r, err := gen.AgentTaskDisposeFindingInstructed(c.Context(), client, ref, a[1], gen.AgentTaskFindingDispositionCode(code), refIn, claim)
				if err != nil {
					return mapFindingError(err)
				}
				if d := r.DisposeAgentTaskFinding; d != nil && d.Id != "" && d.FindingDispositions != nil {
					taskID, haveReadback = d.Id, true
					for _, item := range d.FindingDispositions.Items {
						if item != nil {
							items = append(items, item.TaskFindingDispositionFields)
						}
					}
				}
			} else {
				r, err := gen.AgentTaskDisposeFinding(c.Context(), client, ref, a[1], gen.AgentTaskFindingDispositionCode(code), refIn)
				if err != nil {
					return mapFindingError(err)
				}
				if d := r.DisposeAgentTaskFinding; d != nil && d.Id != "" && d.FindingDispositions != nil {
					taskID, haveReadback = d.Id, true
					for _, item := range d.FindingDispositions.Items {
						if item != nil {
							items = append(items, item.TaskFindingDispositionFields)
						}
					}
				}
			}
			// The server caps findings at 100; the response requests 200. Never
			// fabricate a receipt from arguments if readback is incomplete.
			if haveReadback {
				for i := range items {
					if items[i].FindingId == a[1] {
						d := findingDispositionReceiptDTO{TaskID: taskID, findingDispositionDTO: findingDispositionFrom(items[i])}
						return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
							if _, err := fmt.Fprintf(w, "task: %s\n", output.SafeCell(d.TaskID)); err != nil {
								return err
							}
							return renderFindingDispositions(w, []findingDispositionDTO{d.findingDispositionDTO})
						})
					}
				}
			}
			return exitcode.Newf(exitcode.Error, "disposition may have been recorded but its readback is unavailable; read finding-dispositions before retrying")
		},
	}
	c.Flags().StringVar(&code, "disposition", "", "required: fixed, accepted, deferred or rejected")
	c.Flags().StringVar(&reference, "ref", "", "optional kind=target[|label]; omission replaces any prior reference with none")
	addTaskInstructionFlag(c, &rawInstruction)
	return c
}

// Keep structured serverCode on these new surfaces without changing legacy task envelopes.
// mapFindingError is a task-family mapper, not a findings-only one: it wraps
// mapTaskError, which consults taskErrorCodes, and adds the structured server
// code. The findings verbs and `task link-defect` use it (hadron-cli#2296).
func mapFindingError(err error) error { return api.WithServerCode(mapTaskError(err), err) }
