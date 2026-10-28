package node

import (
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

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

func enableLintWarnings(cmd *cobra.Command) {
	cmd.Flags().Bool("lint-warnings", false, "read all current source-lint warnings after success (advisory; unavailable stays null; excludes --body-only and --dry-run)")
	cmd.Long += "\n\n--lint-warnings opts into a separate, paginated source-lint read after success.\nIt reports warnings as they hold now, not a snapshot of the authored/read revision.\nThe JSON lintWarnings page is null when unavailable (including an older server),\notherwise {items,total,nextCursor:null}. Without the flag the field and extra\nread are absent. Lint never blocks a successful write or retries it. Empty items\nis a real no-warning answer. Human warning text is terminal-safe; JSON retains\nthe server's exact text. This option excludes --body-only and --dry-run."
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
	result := &lintWarningPageDTO{Items: []lintWarningDTO{}}
	var after *string
	seen := map[string]bool{}
	// Fail the advisory read closed on pathological pagination; never return a
	// partial page as a complete no-warning result or change mutation success.
	for requests := 0; requests < 1000; requests++ {
		r, err := gen.GetNodeLintWarnings(cmd.Context(), client, ref, after)
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
