package api

import (
	"fmt"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// NodeBatchCap mirrors hadron-server's BATCH_READ_MAX_NODES (cor:api:040): a
// single nodeBatch call accepts at most 200 refs and fails loud above that, so
// bulk reads fan out in fixed-size chunks. The server also enforces a ~1 MB
// response cap that can return a partial page (truncated=true) with the
// spillover in `omitted`; CollectNodeBatch re-requests those.
const NodeBatchCap = 200

// CollectNodeBatch fetches full nodes for refs in cap-sized chunks, re-queuing
// the spillover the server drops under its response-size cap. A ref is a node
// PK or a fully-qualified node URN (hadron-server#813) — `omitted` and
// `unavailable` come back as the refs were sent, so re-queuing and reporting
// both stay in the caller's own vocabulary. fetch is injected rather than
// calling gen.NodeBatch directly so the chunking/truncation loop is
// unit-testable without a server and so each caller controls the field
// projection. Returns the nodes (input order is not preserved across chunks),
// the union of refs the server reported unavailable, and the first error.
//
// Shared by the whole-corpus fan-outs (`memory export`, `spec get --prefix`).
func CollectNodeBatch(
	refs []string,
	fetch func([]string) (*gen.NodeBatchNodeBatchNodeBatchResult, error),
) ([]*gen.NodeBatchNodeBatchNodeBatchResultNodesNode, []string, error) {
	var nodes []*gen.NodeBatchNodeBatchNodeBatchResultNodesNode
	var unavailable []string
	queue := append([]string(nil), refs...)
	for len(queue) > 0 {
		n := NodeBatchCap
		if n > len(queue) {
			n = len(queue)
		}
		chunk := queue[:n]
		queue = queue[n:]

		res, err := fetch(chunk)
		if err != nil {
			return nil, nil, err
		}
		if res == nil {
			return nil, nil, fmt.Errorf("nodeBatch returned no result for %d ref(s)", len(chunk))
		}
		nodes = append(nodes, res.Nodes...)
		unavailable = append(unavailable, res.Unavailable...)
		if res.Truncated {
			// Byte-cap spillover. The server always returns at least one node
			// per call, so re-queuing strictly shrinks the backlog; guard the
			// contract anyway so a server bug surfaces as an error, not a hang.
			if len(res.Nodes) == 0 {
				return nil, nil, fmt.Errorf("nodeBatch truncated without returning any node (%d omitted)", len(res.Omitted))
			}
			queue = append(queue, res.Omitted...)
		}
	}
	return nodes, unavailable, nil
}
