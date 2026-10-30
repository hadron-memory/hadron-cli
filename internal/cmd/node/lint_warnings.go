package node

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// One budget for the entire opt-in command, including all nodes in batch get.
// It starts after the successful main operation, never constrains a mutation,
// and is not reset by paging or by loading the next node.
const lintWarningReadTimeout = 5 * time.Second
const lintWarningMaxRequests = 1000

type lintWarningDeadlineKey struct{}

type lintWarningDTO struct {
	Kind              string  `json:"kind"`
	Position          int     `json:"position"`
	Text              string  `json:"text"`
	TargetURN         *string `json:"targetUrn"`
	PinnedRevision    *int    `json:"pinnedRevision"`
	NewerRevision     *int    `json:"newerRevision"`
	OverriddenNodeLoc *string `json:"overriddenNodeLoc"`
	WorkerField       *string `json:"workerField"`
}
type lintWarningPageDTO struct {
	Items      []lintWarningDTO `json:"items"`
	Total      int              `json:"total"`
	NextCursor *string          `json:"nextCursor"`
}

func enableLintWarnings(cmd *cobra.Command, incompatible ...string) {
	usage := "read all current source-lint warnings after success (advisory; unavailable stays null)"
	if len(incompatible) > 0 {
		usage += "; excludes " + strings.Join(incompatible, " and ")
	}
	cmd.Flags().Bool("lint-warnings", false, usage)
	cmd.Long += "\n\n" + detailsHeading(cmd.Long) + `--lint-warnings: after the command succeeds, also list the node's current
lint warnings (advisory checks on its source, such as a link pinned to an
older revision). They describe the node as it is now, never block or undo the
command, and are not retried. With --json they are "lintWarnings":
{items, total, nextCursor: null}; empty items means no warnings, and null
means the check could not answer (an older server, or the 5-second budget the
check shares across every node of the command ran out). Without the flag the
field is absent.`
	if len(incompatible) > 0 {
		cmd.Long += " This option excludes " + strings.Join(incompatible, " and ") + "."
	}
}

func wantsLintWarnings(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("lint-warnings")
	return v
}

// A nil outer pointer omits the unrequested field. A nonnil outer pointer
// containing nil emits the requested-but-unavailable page as JSON null.
func loadLintWarnings(cmd *cobra.Command, client graphql.Client, ref string) **lintWarningPageDTO {
	if !wantsLintWarnings(cmd) {
		return nil
	}
	var page *lintWarningPageDTO
	slot := &page
	parent := cmd.Context()
	deadline, ok := parent.Value(lintWarningDeadlineKey{}).(time.Time)
	if !ok {
		deadline = time.Now().Add(lintWarningReadTimeout)
		parent = context.WithValue(parent, lintWarningDeadlineKey{}, deadline)
		cmd.SetContext(parent)
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	if ctx.Err() != nil {
		return slot
	}
	result := &lintWarningPageDTO{Items: []lintWarningDTO{}}
	var after *string
	seen := map[string]bool{}
	// Fail the advisory read closed on pathological pagination; never return a
	// partial page as a complete no-warning result or change mutation success.
	for requests := 0; requests < lintWarningMaxRequests; requests++ {
		r, err := gen.GetNodeLintWarnings(ctx, client, ref, after)
		if err != nil || r.Node == nil || r.Node.LintWarnings == nil {
			return slot
		}
		p := r.Node.LintWarnings
		if p.Items == nil || p.Total < 0 || (after != nil && p.Total != result.Total) {
			return slot
		}
		result.Total = p.Total
		for _, v := range p.Items {
			if v == nil {
				return slot
			}
			result.Items = append(result.Items, lintWarningDTO{string(v.Kind), v.Position, v.Text, v.TargetUrn, v.PinnedRevision, v.NewerRevision, v.OverriddenNodeLoc, v.WorkerField})
		}
		if len(result.Items) > result.Total {
			return slot
		}
		if p.NextCursor == nil {
			if len(result.Items) != result.Total {
				return slot
			}
			page = result
			return slot
		}
		if *p.NextCursor == "" || seen[*p.NextCursor] || len(p.Items) == 0 {
			return slot
		}
		seen[*p.NextCursor] = true
		next := *p.NextCursor
		after = &next
	}
	return slot
}

func renderLintWarnings(w io.Writer, slot **lintWarningPageDTO) error {
	if slot == nil {
		return nil
	}
	if *slot == nil {
		_, err := fmt.Fprintln(w, "lint warnings: unavailable (no conclusion about warnings)")
		return err
	}
	p := *slot
	if _, err := fmt.Fprintf(w, "lint warnings: %d (current advisory read)\n", p.Total); err != nil {
		return err
	}
	for _, item := range p.Items {
		if _, err := fmt.Fprintf(w, "  %s at %d: %s\n", output.SafeCell(item.Kind), item.Position, output.SafeCell(item.Text)); err != nil {
			return err
		}
	}
	return nil
}

func writeWithLint(f *cmdutil.Factory, dto any, slot **lintWarningPageDTO, render func(io.Writer) error) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if err := render(w); err != nil {
			return err
		}
		return renderLintWarnings(w, slot)
	})
}

// detailsHeading returns "Details:\n\n" unless the page already has a
// Details block, so a shared paragraph lands inside it rather than above.
func detailsHeading(long string) string {
	if strings.Contains(long, "\nDetails:\n") || strings.HasPrefix(long, "Details:\n") {
		return ""
	}
	return "Details:\n\n"
}
