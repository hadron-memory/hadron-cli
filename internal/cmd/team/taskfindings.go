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

const findingDispositionDoc = `Read or replace a finding's disposition beside a finished task's immutable
result (server#3258, cor:agt:050:30). Every code — fixed, accepted, deferred,
rejected — resolves the finding. The latest code/reference wins; there is no
clear. Omitting --ref on a replacement means the new value has no reference.
References are stored text, never fetched or resolved by this command.

Sign in first. Requires a provider containing server#3258. Refs are task ids or handles.
The task determines its App; --app does not qualify an ambiguous handle.
An ambiguous bare handle needs an id or a qualified handle such as
hrn:agenttask:example.com:team:AT1.2. An unreadable or missing task exits 4.
The server alone judges write authority: only task assigners after finish,
never the assignee alone, an App key or impersonation. On writes, TASK_NOT_OWNER exits 8,
TASK_NOT_FINISHED exits 5, TASK_FINDING_NOT_FOUND exits 4, BAD_USER_INPUT exits 2.
No refusal is retried. A transport failure may have committed: read the current
dispositions before deciding whether to send another write.

Human output bounds values and neutralizes terminal controls. JSON retains
original text, including reference kind, target and nullable label.`

func newCmdTaskFindingDispositions(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	c := &cobra.Command{
		Use: "finding-dispositions <task>", Short: "Read a page of the latest finding dispositions",
		Long: findingDispositionDoc + `

This read uses the task's existing readers; it writes nothing. A page is in
finding order, default 20/max 200; --limit 0 returns counts only. Follow
hasMore with --offset, never infer completion from an empty or short page.
--json is {items: [{findingId, disposition, reference}], total, hasMore};
an empty list is [], reference is null when absent. This does not list the
immutable result findings themselves (the separate findings client slice).`,
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
	var code, reference string
	c := &cobra.Command{
		Use: "dispose-finding <task> <finding-id>", Short: "Record or replace one finding's disposition",
		Long: findingDispositionDoc + `

--disposition is required and takes exactly fixed, accepted, deferred or
rejected. --ref is optional: kind=target[|label], with kind issue,
pull-request, node, task or url. A label starts at the first |; target/label
are sent unchanged, with server-owned validation. The mutation sends once
and prints the server-returned latest value, not an inferred success.
--json is {taskId, findingId, disposition, reference}; reference is null
when absent. The immutable result is neither sent nor modified.`,
		Example: "  hadron team task dispose-finding AT1.2 F1 --disposition deferred --ref 'issue=https://example.com/issues/7|Follow-up' --json",
		Args:    cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, a []string) error {
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
			r, err := gen.AgentTaskDisposeFinding(c.Context(), client, ref, a[1], gen.AgentTaskFindingDispositionCode(code), refIn)
			if err != nil {
				return mapFindingError(err)
			}
			// The server caps findings at 100; the response requests 200. Never
			// fabricate a receipt from arguments if readback is incomplete.
			if r.DisposeAgentTaskFinding != nil && r.DisposeAgentTaskFinding.Id != "" && r.DisposeAgentTaskFinding.FindingDispositions != nil {
				p := r.DisposeAgentTaskFinding.FindingDispositions
				for _, item := range p.Items {
					if item != nil && item.FindingId == a[1] {
						d := findingDispositionReceiptDTO{TaskID: r.DisposeAgentTaskFinding.Id, findingDispositionDTO: findingDispositionFrom(item.TaskFindingDispositionFields)}
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
	return c
}

// Keep structured serverCode on these new surfaces without changing legacy task envelopes.
func mapFindingError(err error) error { return api.WithServerCode(mapTaskError(err), err) }
