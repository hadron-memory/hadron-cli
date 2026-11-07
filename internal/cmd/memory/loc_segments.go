package memory

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type locSegmentDTO struct {
	Segment  string `json:"segment"`
	HasNode  bool   `json:"hasNode"`
	MemoryID string `json:"memoryId"`
}
type locSegmentsPageDTO struct {
	Items     []locSegmentDTO `json:"items"`
	HasMore   bool            `json:"hasMore"`
	EndCursor *string         `json:"endCursor"`
}

func newCmdLocSegments(f *cmdutil.Factory) *cobra.Command {
	var memories []string
	var parent, after string
	var limit int
	cmd := &cobra.Command{
		Use:   "loc-segments --memory <id-or-URN> [--memory <id-or-URN> ...] [--parent <loc>] [--limit <n>] [--after <cursor>]",
		Short: "Browse one page of next loc segments, including node-less branches",
		Long: `List the next colon-separated loc segments under a parent, one level at a
time. Omit --parent for the root. A branch with only deeper nodes still appears:
HAS NODE is false unless an eligible node exists at that exact child loc.

Name each memory explicitly with repeatable --memory (no -m shorthand). Values
and --parent go to the server unchanged; there is no ambient-memory fallback or
existence lookup. Unreadable, unresolvable and empty memories give the same empty
page. Inherited rows name their source memory, which may differ from your input.
Rows keep server sibling order; equal segment names in different memories remain
separate. This is not a recursive tree or a cross-request snapshot.

Each invocation makes one query. The server defaults to 20 rows and caps a page
at 200; a larger positive --limit is sent unchanged and capped by the server.
When more remain, stderr shows a continuation: repeat the same scope with
--after <cursor>. JSON carries items, hasMore and nullable endCursor. A malformed
page is refused before any rows or continuation are printed; no fallback query
is made. Old-provider schema errors use the existing CLI/server-skew refusal.`,
		Example: `  hadron memory loc-segments --memory hrn:mem:acme.com:kb
  hadron memory loc-segments --memory hrn:mem:acme.com:kb --parent findings --limit 1 --json
  hadron memory loc-segments --memory hrn:mem:acme.com:kb --parent findings --limit 1 --after 'returned-cursor'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(memories) == 0 {
				return exitcode.Newf(exitcode.Usage, "pass at least one --memory")
			}
			for _, m := range memories {
				if strings.TrimSpace(m) == "" {
					return exitcode.Newf(exitcode.Usage, "--memory must not be empty")
				}
			}
			var pageLimit *int
			if cmd.Flags().Changed("limit") {
				if limit <= 0 {
					return exitcode.Newf(exitcode.Usage, "--limit must be positive")
				}
				pageLimit = &limit
			}
			var cursor *string
			if cmd.Flags().Changed("after") {
				if strings.TrimSpace(after) == "" {
					return exitcode.Newf(exitcode.Usage, "--after must not be empty; omit it for the first page")
				}
				cursor = &after
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.MemoryLocSegments(cmd.Context(), client, memories, parent, pageLimit, cursor)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil {
				return exitcode.Newf(exitcode.Error, "server returned no loc-segment page")
			}
			p := resp.MemoryLocSegments
			if p == nil || p.Items == nil {
				return exitcode.Newf(exitcode.Error, "server returned a loc-segment page without items")
			}
			if p.EndCursor != nil && output.SafeCell(*p.EndCursor) != *p.EndCursor {
				return exitcode.Newf(exitcode.Error, "server returned an unsafe loc-segment cursor")
			}
			if p.HasMore && (p.EndCursor == nil || *p.EndCursor == "") {
				return exitcode.Newf(exitcode.Error, "server returned more loc segments without a continuation")
			}
			dto := locSegmentsPageDTO{Items: []locSegmentDTO{}, HasMore: p.HasMore, EndCursor: p.EndCursor}
			for _, item := range p.Items {
				if item == nil || item.Segment == "" || item.MemoryId == "" {
					return exitcode.Newf(exitcode.Error, "server returned an invalid loc-segment item")
				}
				dto.Items = append(dto.Items, locSegmentDTO{Segment: item.Segment, HasNode: item.HasNode, MemoryID: item.MemoryId})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				table := output.NewTable(w, "MEMORY ID", "SEGMENT", "HAS NODE")
				for _, item := range dto.Items {
					table.Row(output.SafeCell(item.MemoryID), output.SafeCell(item.Segment), fmt.Sprint(item.HasNode))
				}
				if err := table.Flush(); err != nil {
					return err
				}
				if dto.HasMore {
					_, err := fmt.Fprintf(f.IOStreams.ErrOut, "more segments remain — continue with --after %s\n", shellQuote(*dto.EndCursor))
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringArrayVar(&memories, "memory", nil, "memory ID or URN; repeat for multiple memories (never comma-split)")
	cmd.Flags().StringVar(&parent, "parent", "", "parent loc; omit for the root")
	cmd.Flags().IntVar(&limit, "limit", 0, "positive page size; server defaults to 20 and caps at 200")
	cmd.Flags().StringVar(&after, "after", "", "opaque continuation from the previous page")
	return cmd
}

// shellQuote quotes s for a copy-pasteable command (cli#1787): bare when every
// byte is in the safe set, else single-quoted with ' escaped, so an opaque
// cursor always pastes back as one inert argument. JSON keeps the raw value.
// Same rule as the other per-package copies (app, automation).
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789:._/@-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
