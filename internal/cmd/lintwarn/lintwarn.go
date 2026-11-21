// Package lintwarn is the shared core of the CLI's advisory lint reads
// (cor:agt:040:13): the page DTOs, the fixed "unavailable" stderr class, and
// a fail-closed cursor loop that never returns a partial page as a complete
// one. Callers own their operation, flag and rendering (cli#1167, cli#2007).
package lintwarn

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
)

// UnavailableClass is the one stderr line an unavailable advisory read prints,
// once per command. Callers may append fixed field labels, never the underlying error.
const UnavailableClass = "lint warnings: unavailable (advisory read did not complete)"

type unavailableKey struct{}

// ReportUnavailable prints UnavailableClass with optional fixed field labels once
// per command. Callers pass only labels they own, never server text: never expose the
// provider error or infer why the advisory is unavailable. A diagnostic write
// failure cannot undo the command's success. The first call's labels win; callers
// with several fields must aggregate their labels before calling.
func ReportUnavailable(cmd *cobra.Command, fields ...string) {
	if cmd.Context().Value(unavailableKey{}) != nil {
		return
	}
	cmd.SetContext(context.WithValue(cmd.Context(), unavailableKey{}, true))
	line := UnavailableClass
	if len(fields) != 0 {
		line += " [" + strings.Join(fields, ", ") + "]"
	}
	cmd.PrintErrln(line)
}

// Warning is one lint warning as the CLI prints it (stable --json item shape).
type Warning struct {
	Kind              string  `json:"kind"`
	Position          int     `json:"position"`
	Text              string  `json:"text"`
	TargetURN         *string `json:"targetUrn"`
	PinnedRevision    *int    `json:"pinnedRevision"`
	NewerRevision     *int    `json:"newerRevision"`
	OverriddenNodeLoc *string `json:"overriddenNodeLoc"`
	WorkerField       *string `json:"workerField"`
}

// Page is a completed read: every page drained, never partial.
type Page struct {
	Items      []Warning `json:"items"`
	Total      int       `json:"total"`
	NextCursor *string   `json:"nextCursor"`
}

// Raw is one server page, converted by the caller's fetch. A nil Items slice or
// a nil element means the server broke its contract; the read is then
// unavailable, never partial.
type Raw struct {
	Items      []*Warning
	Total      int
	NextCursor *string
}

// Fetch reads one page after the cursor (nil for the first). Return nil, nil
// for a null page, or an error for any failure or identity mismatch: all of
// them make the whole read unavailable.
type Fetch func(ctx context.Context, after *string) (*Raw, error)

// Drain follows the cursor to the end and returns nil (unavailable) on any
// error, null page, nil items, negative or drifting total, empty or repeated
// cursor, short final page, or when allow reports the request budget spent.
// allow is asked before every request.
func Drain(ctx context.Context, allow func() bool, fetch Fetch) *Page {
	result := &Page{Items: []Warning{}}
	var after *string
	seen := map[string]bool{}
	for allow() {
		p, err := fetch(ctx, after)
		if err != nil || p == nil {
			return nil
		}
		if p.Items == nil || p.Total < 0 || (after != nil && p.Total != result.Total) {
			return nil
		}
		result.Total = p.Total
		for _, v := range p.Items {
			if v == nil {
				return nil
			}
			result.Items = append(result.Items, *v)
		}
		if len(result.Items) > result.Total {
			return nil
		}
		if p.NextCursor == nil {
			if len(result.Items) != result.Total {
				return nil
			}
			return result
		}
		if *p.NextCursor == "" || seen[*p.NextCursor] || len(p.Items) == 0 {
			return nil
		}
		seen[*p.NextCursor] = true
		next := *p.NextCursor
		after = &next
	}
	return nil
}
