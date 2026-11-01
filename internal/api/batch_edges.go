package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// ReadNodeBatch preserves the NodeBatch projection when its selected-edge
// budget is exceeded. Only the provider's structured edge-cap refusal is
// retried, by bisecting explicit refs with the SAME query. A k-ref chunk costs
// at most 2k-1 requests, before CollectNodeBatch's existing byte-cap requeues.
// Prefixes and an indivisible ref refuse explicitly; partial data from any
// failed request is discarded. No ordinary edges() projection is substituted.
func ReadNodeBatch(ctx context.Context, client graphql.Client, refs []string, memory, prefix *string) (*gen.NodeBatchResponse, error) {
	resp, err := gen.NodeBatch(ctx, client, refs, memory, prefix)
	max, capError := batchEdgeCap(err)
	if !capError {
		return resp, err
	}
	if memory != nil || prefix != nil || len(refs) < 2 {
		subject := "the requested batch"
		if len(refs) == 1 {
			subject = "node " + output.SafeLine(refs[0], len(refs[0])+1)
		}
		if prefix != nil {
			subject = "prefix " + output.SafeLine(*prefix, len(*prefix)+1)
		}
		return nil, &nodeBatchEdgeCapRefusal{exitcode.Newf(exitcode.Usage, "%s exceeds the nodeBatch selected-edge cap (%s); no complete edge set was read. Read each direction in pages with hadron edge ls --source <node-ref> and hadron edge ls --target <node-ref>; for a prefix, use narrower prefixes or explicit refs", subject, max)}
	}
	mid := len(refs) / 2
	left, err := ReadNodeBatch(ctx, client, refs[:mid], nil, nil)
	if err != nil {
		return nil, err
	}
	right, err := ReadNodeBatch(ctx, client, refs[mid:], nil, nil)
	if err != nil {
		return nil, err
	}
	if left == nil || right == nil || left.NodeBatch == nil || right.NodeBatch == nil {
		return nil, fmt.Errorf("nodeBatch returned no result while splitting an edge-capped batch")
	}
	a, b := left.NodeBatch, right.NodeBatch
	if (a.Truncated && len(a.Nodes) == 0) || (b.Truncated && len(b.Nodes) == 0) {
		return nil, fmt.Errorf("nodeBatch truncated without returning any node while splitting")
	}
	// The provider deduplicates resolved ids (including PK/URN aliases). A split
	// must not introduce duplicate rows that an unsplit call would not return.
	seen := map[string]bool{}
	nodes := make([]*gen.NodeBatchNodeBatchNodeBatchResultNodesNode, 0, len(a.Nodes)+len(b.Nodes))
	for _, n := range append(a.Nodes, b.Nodes...) {
		if n == nil {
			return nil, fmt.Errorf("nodeBatch returned a null node while splitting")
		}
		if !seen[n.Id] {
			seen[n.Id] = true
			nodes = append(nodes, n)
		}
	}
	return &gen.NodeBatchResponse{NodeBatch: &gen.NodeBatchNodeBatchNodeBatchResult{
		Nodes: nodes, Truncated: a.Truncated || b.Truncated, Omitted: batchUnique(append(a.Omitted, b.Omitted...)), Unavailable: batchUnique(append(a.Unavailable, b.Unavailable...)),
	}}, nil
}

func batchUnique(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// A mixed error list or an HTTP/transport/decode failure is not an edge-cap
// refusal. Read before MapError so the structured extensions remain available.
func batchEdgeCap(err error) (string, bool) {
	list, ok := bareGraphQLList(err)
	if !ok || len(list) == 0 {
		return "", false
	}
	max := "maximum not supplied"
	for _, e := range list {
		if e == nil || extensionCode(e) != "BAD_USER_INPUT" || e.Extensions["limit"] != "edges" {
			return "", false
		}
		// The wire JSON number is float64. Never echo arbitrary extension text.
		if n, ok := e.Extensions["max"].(float64); ok && n > 0 && n <= float64(1<<53) && math.Trunc(n) == n {
			max = "maximum " + strconv.FormatFloat(n, 'f', 0, 64)
		}
	}
	return max, true
}

// ReadNodeBatchContent is the same stored-content projection without either
// edge direction. It adapts the generated edge-free type for existing content
// consumers; it is never used by callers whose output depends on edges.
func ReadNodeBatchContent(ctx context.Context, client graphql.Client, refs []string) (*gen.NodeBatchResponse, error) {
	resp, err := gen.NodeBatchContent(ctx, client, refs, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.NodeBatch == nil {
		return &gen.NodeBatchResponse{}, nil
	}
	r := resp.NodeBatch
	nodes := make([]*gen.NodeBatchNodeBatchNodeBatchResultNodesNode, 0, len(r.Nodes))
	for _, n := range r.Nodes {
		if n == nil {
			nodes = append(nodes, nil)
			continue
		}
		nodes = append(nodes, &gen.NodeBatchNodeBatchNodeBatchResultNodesNode{
			Id: n.Id, Urn: n.Urn, ActionUrn: n.ActionUrn, PortalUrl: n.PortalUrl, MemoryId: n.MemoryId, Loc: n.Loc, Name: n.Name, Alias: n.Alias, NodeType: n.NodeType, Role: n.Role, ObjectType: n.ObjectType, IsRunnable: n.IsRunnable, Description: n.Description, Abstract: n.Abstract, AbstractOriginHash: n.AbstractOriginHash, Tags: n.Tags, Seq: n.Seq, Data: n.Data, Properties: n.Properties, Content: n.Content, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
		})
	}
	return &gen.NodeBatchResponse{NodeBatch: &gen.NodeBatchNodeBatchNodeBatchResult{Nodes: nodes, Truncated: r.Truncated, Omitted: r.Omitted, Unavailable: r.Unavailable}}, nil
}

// IsNodeBatchEdgeCapRefusal identifies the sanitized terminal cap refusal, even
// after a caller maps its exit code. No raw provider prose enters relint DTOs.
func IsNodeBatchEdgeCapRefusal(err error) bool {
	var cap *nodeBatchEdgeCapRefusal
	return errors.As(err, &cap)
}

type nodeBatchEdgeCapRefusal struct{ err error }

func (e *nodeBatchEdgeCapRefusal) Error() string { return e.err.Error() }
func (e *nodeBatchEdgeCapRefusal) Unwrap() error { return e.err }
