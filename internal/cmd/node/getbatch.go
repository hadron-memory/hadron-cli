package node

import (
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// batchNode aliases the deeply-nested generated type, per the repo convention.
type batchNode = gen.NodeBatchNodeBatchNodeBatchResultNodesNode

// nodeBatchDTO is the stable --json shape when `node get` reads MORE than one
// node. A single ref keeps returning the bare node object, so the existing
// contract is untouched; the envelope only appears when the caller asked for a
// set, which it always knows it did.
//
// Unavailable is load-bearing, not decoration: the server reports a denied and
// a missing node identically, and a node can be listed yet be unreadable. A
// fan-out that silently returned fewer nodes than asked for would hide that.
type nodeBatchDTO struct {
	Nodes       []nodeDetailDTO `json:"nodes"`
	Unavailable []string        `json:"unavailable"`
}

// fetchNodeBatch reads many nodes in ONE call — whether the caller named a
// subtree with locPrefix or an explicit set of refs. Since hadron-server#813
// nodeBatch(refs:) takes a PK or a fully-qualified URN, so the refs go straight
// out; before that they were matched on the primary key alone and every URN
// needed its own resolve first, which made an N-node read cost N+1 round trips.
func fetchNodeBatch(
	cmd *cobra.Command, client graphql.Client, memory, locPrefix string, refs []string, prefixMode bool,
) ([]*batchNode, []string, error) {
	fetch := func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
		resp, err := gen.NodeBatch(cmd.Context(), client, chunk, nil, nil)
		if err != nil {
			return nil, api.MapError(err)
		}
		return resp.NodeBatch, nil
	}

	if prefixMode {
		memRef := cmdutil.CanonicalMemoryRef(memory)
		resp, err := gen.NodeBatch(cmd.Context(), client, nil, &memRef, &locPrefix)
		if err != nil {
			return nil, nil, api.MapError(err)
		}
		if resp.NodeBatch == nil {
			return nil, nil, exitcode.Newf(exitcode.Error, "nodeBatch returned no result")
		}
		// The subtree form has no truncation loop to run: the server caps the
		// node COUNT with a loud error, and byte-cap spillover re-queues by id.
		nodes := resp.NodeBatch.Nodes
		unavailable := resp.NodeBatch.Unavailable
		if resp.NodeBatch.Truncated {
			more, un, err := api.CollectNodeBatch(resp.NodeBatch.Omitted, fetch)
			if err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, more...)
			unavailable = append(unavailable, un...)
		}
		return nodes, unavailable, nil
	}

	// Explicit set — canonicalized locally, then sent in one batched read. A
	// ref that is malformed is reported as unavailable rather than failing the
	// whole batch (the server errors loudly on one, so it never gets sent):
	// one bad ref in twenty should not cost the other nineteen.
	sendable := make([]string, 0, len(refs))
	var unresolved []string
	seen := map[string]bool{}
	for _, ref := range refs {
		out, err := cmdutil.BatchNodeRef(memory, ref)
		if err != nil {
			// ONLY a locally-invalid ref is "unavailable". Anything else is a
			// real failure: swallowing it would emit a plausible partial result
			// and exit 4, hiding an auth or operational error behind a
			// not-found (review on #303).
			if exitcode.FromError(err) == exitcode.Usage {
				unresolved = append(unresolved, ref)
				continue
			}
			return nil, nil, err
		}
		if seen[out] {
			continue // a repeated ref must not yield the node twice
		}
		seen[out] = true
		sendable = append(sendable, out)
	}
	nodes, unavailable, err := api.CollectNodeBatch(sendable, fetch)
	if err != nil {
		return nil, nil, err
	}
	// The server echoes the refs as sent, so nothing has to be translated back
	// — what the caller sees named is what the caller typed (modulo the
	// canonical hrn:node: prefix).
	unresolved = append(unresolved, unavailable...)
	return nodes, unresolved, nil
}

// batchDetailDTO maps the batch projection onto the same per-node shape the
// single read emits, so the FIELDS match `node get <one-ref>` exactly.
//
// One value does differ, by server design: nodeBatch returns content RAW —
// Mustache templates are not compiled — whereas the single node(ref:) read
// compiles them, because the batch is meant as a bulk source read for
// lint/audit/migration and compiling per node would reintroduce the N+1 it
// exists to remove. For a node without templates the two are identical; for a
// template node the batch yields the source. The command help says so.
func batchDetailDTO(n *batchNode) nodeDetailDTO {
	dto := nodeDetailDTO{
		nodeDTO: nodeDTO{
			ID:         n.Id,
			MemoryID:   n.MemoryId,
			Loc:        n.Loc,
			Name:       n.Name,
			NodeType:   n.NodeType,
			Tags:       n.Tags,
			IsRunnable: boolVal(n.IsRunnable),
			UpdatedAt:  n.UpdatedAt,
		},
		ObjectType:    n.ObjectType,
		Description:   n.Description,
		Abstract:      n.Abstract,
		Content:       n.Content,
		Data:          n.Data,
		Properties:    n.Properties,
		Seq:           n.Seq,
		CreatedAt:     n.CreatedAt,
		OutgoingEdges: []edgeRefDTO{},
		IncomingEdges: []edgeRefDTO{},
	}
	for _, e := range n.OutgoingEdges {
		// #781: the far endpoint is null when its memory is unreadable.
		tid, tloc, tmem := "", "", ""
		if e.Target != nil {
			tid, tloc, tmem = e.Target.Id, e.Target.Loc, e.Target.MemoryId
		}
		dto.OutgoingEdges = append(dto.OutgoingEdges,
			edgeRefOf(e.Id, e.Name, e.Loc, e.IsRunnable, e.Priority, tid, tloc, tmem))
	}
	for _, e := range n.IncomingEdges {
		sid, sloc, smem := "", "", ""
		if e.Source != nil {
			sid, sloc, smem = e.Source.Id, e.Source.Loc, e.Source.MemoryId
		}
		dto.IncomingEdges = append(dto.IncomingEdges,
			edgeRefOf(e.Id, e.Name, e.Loc, e.IsRunnable, e.Priority, sid, sloc, smem))
	}
	return dto
}

// renderNodeBatch prints the human form: each node in the same layout the
// single read uses, separated by a rule, then the unavailable refs.
func renderNodeBatch(w io.Writer, dto nodeBatchDTO) error {
	for i, n := range dto.Nodes {
		if i > 0 {
			fmt.Fprintln(w, "\n———")
		}
		if err := renderNodeDetail(w, n); err != nil {
			return err
		}
	}
	if len(dto.Unavailable) > 0 {
		if len(dto.Nodes) > 0 {
			fmt.Fprintln(w)
		}
		// Say why it's ambiguous: the server refuses to distinguish denied
		// from missing, so neither the CLI nor the caller can.
		fmt.Fprintf(w, "unavailable (%d) — not found, or not readable by you:\n", len(dto.Unavailable))
		for _, ref := range dto.Unavailable {
			fmt.Fprintf(w, "  %s\n", ref)
		}
	}
	return nil
}

// emitNodeBatch writes the multi-node result. It exits non-zero when any ref
// was unavailable: a caller that asked for five nodes and got three must not
// read exit 0 as "all five are here" (the same reasoning as the partial-write
// contract).
func emitNodeBatch(f *cmdutil.Factory, dto nodeBatchDTO) error {
	if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		return renderNodeBatch(w, dto)
	}); err != nil {
		return err
	}
	if len(dto.Unavailable) > 0 {
		return exitcode.Silent(exitcode.NotFound)
	}
	return nil
}
