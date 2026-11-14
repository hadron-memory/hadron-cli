package node

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/lintwarn"
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

func reportLintUnavailable(cmd *cobra.Command) { lintwarn.ReportUnavailable(cmd) }

type lintWarningPageDTO = lintwarn.Page

func enableLintWarnings(cmd *cobra.Command, incompatible ...string) {
	usage := "read all current source-lint warnings after success (advisory; unavailable stays null)"
	if len(incompatible) > 0 {
		usage += "; excludes " + strings.Join(incompatible, " and ")
	}
	cmd.Flags().Bool("lint-warnings", false, usage)
	subject := "the node's"
	if cmd.Name() == "get" {
		subject = "each returned node's"
	}
	cmd.Long += "\n\n" + detailsHeading(cmd.Long) + `--lint-warnings: after the command succeeds, also list ` + subject + ` current
lint warnings (advisory checks on its source, such as a link pinned to an
older revision). They describe the node as it is now, never block or undo the
command, and are not retried. With --json they are "lintWarnings":
{items, total, nextCursor: null}. items: [] means the completed check found no warnings;
lintWarnings: null means the check could not answer (for example, an older
server or the 5-second budget shared across every node ran out). Without the flag the
field is absent. An unavailable read also prints one fixed advisory line on
stderr, without its underlying error; the command result and exit are unchanged.`
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
	defer func() {
		if page == nil {
			reportLintUnavailable(cmd)
		}
	}()
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
	requests := 0
	allow := func() bool { requests++; return requests <= lintWarningMaxRequests }
	// Fail the advisory read closed on pathological pagination; never return a
	// partial page as a complete no-warning result or change mutation success.
	page = lintwarn.Drain(ctx, allow, func(ctx context.Context, after *string) (*lintwarn.Raw, error) {
		r, err := gen.GetNodeLintWarnings(ctx, client, ref, after)
		if err != nil || r.Node == nil || r.Node.LintWarnings == nil {
			return nil, err
		}
		p := r.Node.LintWarnings
		raw := &lintwarn.Raw{Total: p.Total, NextCursor: p.NextCursor}
		if p.Items != nil {
			raw.Items = make([]*lintwarn.Warning, len(p.Items))
			for i, v := range p.Items {
				if v != nil {
					raw.Items[i] = &lintwarn.Warning{Kind: string(v.Kind), Position: v.Position, Text: v.Text, TargetURN: v.TargetUrn, PinnedRevision: v.PinnedRevision, NewerRevision: v.NewerRevision, OverriddenNodeLoc: v.OverriddenNodeLoc, WorkerField: v.WorkerField}
				}
			}
		}
		return raw, nil
	})
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
