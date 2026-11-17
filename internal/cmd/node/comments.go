package node

import (
	"context"
	"fmt"
	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"io"
)

// Feedback changes without advancing the target revision. It is an advisory
// snapshot, deliberately separate from the revision-consistent body projection.
// Old servers and unreadable/missing rows are unknown, never fabricated zeros.
func loadCommentSummaries(ctx context.Context, f *cmdutil.Factory, client graphql.Client, nodes []*nodeDetailDTO) {
	loadCommentSummariesTo(ctx, f.IOStreams.ErrOut, client, nodes)
}

// loadCommentSummariesTo is loadCommentSummaries with the note destination named,
// so a caller overlapping it with other advisories can buffer the note and emit
// it in its historical position (cli#1677).
func loadCommentSummariesTo(ctx context.Context, errOut io.Writer, client graphql.Client, nodes []*nodeDetailDTO) {
	byID := map[string]*nodeDetailDTO{}
	queue := []string{}
	for _, n := range nodes {
		if n != nil {
			byID[n.ID] = n
			queue = append(queue, n.ID)
		}
	}
	for len(queue) > 0 {
		size := min(len(queue), api.NodeBatchCap)
		chunk := queue[:size]
		queue = queue[size:]
		r, err := gen.NodeCommentSummaries(ctx, client, chunk)
		if err != nil {
			if !api.IsGraphQLValidationFor(err, "commentSummary") {
				fmt.Fprintf(errOut, "note: feedback counts unavailable (%v)\n", api.MapError(err))
			}
			return
		}
		if r == nil || r.NodeBatch == nil {
			fmt.Fprintln(errOut, "note: feedback counts unavailable: server returned no batch")
			return
		}
		for _, n := range r.NodeBatch.Nodes {
			if n == nil || n.CommentSummary == nil {
				continue
			}
			if dto := byID[n.Id]; dto != nil {
				c := n.CommentSummary
				dto.CommentSummary = &commentSummaryDTO{OpenThreads: c.OpenThreads, ResolvedThreads: c.ResolvedThreads, Comments: c.Comments}
			}
		}
		if r.NodeBatch.Truncated {
			if len(r.NodeBatch.Nodes) == 0 || len(r.NodeBatch.Omitted) >= len(chunk) {
				fmt.Fprintln(errOut, "note: feedback counts unavailable: batch made no progress")
				return
			}
			queue = append(queue, r.NodeBatch.Omitted...)
		}
	}
}
