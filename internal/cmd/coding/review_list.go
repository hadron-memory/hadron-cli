package coding

import (
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Statuses a listed row can carry. `broken` is defined as "the linter reports
// an error-severity finding for this node", derived from lintReview rather than
// re-implemented, so `list` and `lint` cannot disagree about what is broken.
const (
	statusOK          = "ok"
	statusBroken      = "broken"
	statusUnavailable = "unavailable"
)

// reviewItemDTO is one row of `coding review list` — the --json contract.
//
// Trigger is the label on the check's edge back to the review parent: the text
// `tasks:review-changes` matches against a diff. It is empty when the edge is
// missing or unlabelled, which is precisely what makes the check invisible, so
// the column doubles as the triage view.
type reviewItemDTO struct {
	Loc         string   `json:"loc"`
	Name        string   `json:"name"`
	Trigger     string   `json:"trigger"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Seq         *int     `json:"seq"`
	EdgeID      string   `json:"edgeId"`
	Status      string   `json:"status"` // ok | broken | unavailable
}

func newCmdReviewList(f *cmdutil.Factory) *cobra.Command {
	var memory, root string
	var brokenOnly bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the review checklist and each check's trigger",
		Long: `List every check under the review parent with the trigger it fires on.

The trigger is the label on the check's edge back to the parent — the text
` + "`tasks:review-changes`" + ` matches against a diff. A check with no
trigger is invisible to the reviewer, so it lists with status ` + "`broken`" + `.

Membership is the same rule ` + "`coding review lint`" + ` uses: a node under
the parent's loc prefix that is neither tagged ` + "`meta`" + ` nor runnable.
A check whose node cannot be read lists as ` + "`unavailable`" + ` rather than
being dropped.

This is a read-only view: a broken check is a row, not an exit code — a
listing full of them still exits 0. (A usage, auth or not-found error
exits non-zero as everywhere else.) ` + "`coding review lint`" + ` is the
command whose exit code reflects findings.`,
		Example: `  hadron coding review list -m hrn:mem:hadronmemory.com:hadron-cli
  hadron coding review list -m hrn:mem:micromentor.org:mmdata --broken
  hadron coding review list -m hrn:mem:hadronmemory.com:hadron-portal --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := codingMemoryURN(memory)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			in, err := collectReview(cmd.Context(), client, mem, root)
			if err != nil {
				return err
			}

			rows := reviewRows(in, lintReview(in))
			if brokenOnly {
				rows = keepBroken(rows)
			}

			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if len(rows) == 0 {
					fmt.Fprintf(w, "no checks under %q in %s\n", root, mem.raw)
					return nil
				}
				t := output.NewTable(w, "CHECK", "SEQ", "STATUS", "TRIGGER")
				for _, r := range rows {
					t.Row(r.Loc, seqCell(r.Seq), r.Status, dash(r.Trigger))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to list (hrn:mem:<root>:<slug>)")
	cmd.Flags().StringVar(&root, "root", reviewRootLoc, "loc of the review parent node")
	cmd.Flags().BoolVar(&brokenOnly, "broken", false, "only checks that are not ok (broken or unavailable)")
	return cmd
}

// reviewRows projects the collected tree into list rows, ordered the way a
// reviewer walks the checklist: by seq, then loc. An unset seq sorts last —
// most checks leave it unset, and they are not "first".
func reviewRows(in reviewInput, findings []findingDTO) []reviewItemDTO {
	broken := brokenNodes(findings)
	rows := []reviewItemDTO{}
	for loc, n := range in.Members {
		status := statusOK
		if broken[loc] {
			status = statusBroken
		}
		tags := n.Tags
		if tags == nil {
			tags = []string{}
		}
		rows = append(rows, reviewItemDTO{
			Loc: loc, Name: n.Name, Trigger: in.Edges[loc].Label, Description: n.Description,
			Tags: tags, Seq: n.Seq, EdgeID: in.Edges[loc].ID, Status: status,
		})
	}
	// An endpoint that could not be read is listed, not dropped: the caller
	// asked what the checklist contains, and "there is something here I cannot
	// see" is part of the answer (CLAUDE.md's list-vs-read visibility rule).
	for _, u := range sortedCopy(in.Unavailable) {
		rows = append(rows, reviewItemDTO{Loc: u, Tags: []string{}, Status: statusUnavailable})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].Seq, rows[j].Seq
		switch {
		case a != nil && b == nil:
			return true
		case a == nil && b != nil:
			return false
		case a != nil && b != nil && *a != *b:
			return *a < *b
		}
		return rows[i].Loc < rows[j].Loc
	})
	return rows
}

func keepBroken(rows []reviewItemDTO) []reviewItemDTO {
	out := []reviewItemDTO{}
	for _, r := range rows {
		if r.Status != statusOK {
			out = append(out, r)
		}
	}
	return out
}

func seqCell(seq *int) string {
	if seq == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *seq)
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
