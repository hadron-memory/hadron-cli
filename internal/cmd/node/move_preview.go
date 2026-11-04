package node

import (
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	urnlib "github.com/hadron-memory/urn-lib-go"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const moveAdvisory = "Advisory: what you can read now. It does not predict whether the move succeeds, which refusal applies, the rebased locs, or which links break. hadron-server#3424 tracks the server's plan."

type movePreviewNode struct {
	URN  string `json:"urn"`
	Name string `json:"name"`
}
type movePreviewDTO struct {
	Advisory bool   `json:"advisory"`
	Complete bool   `json:"complete"`
	Message  string `json:"message"`
	Status   string `json:"status"`
	Source   struct {
		Nodes []movePreviewNode `json:"nodes"`
	} `json:"source"`
	Destination struct {
		Requested string            `json:"requested"`
		Present   []movePreviewNode `json:"present"`
	} `json:"destination"`
	NotPredicted []string `json:"notPredicted"`
}

func previewMove(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, memory, source, requested string, targetURN, targetMemory *string) error {
	dto := movePreviewDTO{Advisory: true, Message: moveAdvisory, Status: "INCOMPLETE", NotPredicted: []string{"move success", "refusal", "rebased locs", "broken links", "write permission", "schema validity", "changes between reads or before a later move"}}
	dto.Source.Nodes = []movePreviewNode{}
	dto.Destination.Requested = requested
	dto.Destination.Present = []movePreviewNode{}
	// Build privately before rendering. Any failed read keeps complete=false;
	// no partial source/destination list is presented as a finished inventory.
	sourceNodes, present, err := readMovePreview(cmd, client, memory, source, targetURN, targetMemory)
	if err == nil {
		dto.Source.Nodes = sourceNodes
		dto.Destination.Present = present
		dto.Complete = true
		dto.Status = "COMPLETE"
	}
	if writeErr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, e := fmt.Fprintln(w, moveAdvisory); e != nil {
			return e
		}
		if _, e := fmt.Fprintln(w, dto.Status); e != nil {
			return e
		}
		if !dto.Complete {
			_, e := fmt.Fprintln(w, "The read did not finish; no complete inventory is available. Nothing was written.")
			return e
		}
		t := output.NewTable(w)
		t.Row("Source subtree as read:")
		for _, n := range dto.Source.Nodes {
			t.Row(output.SafeCell(n.URN), output.SafeCell(n.Name))
		}
		t.Row("Requested destination:", output.SafeLine(dto.Destination.Requested, len(dto.Destination.Requested)+1))
		t.Row("Nodes already present at the destination:")
		for _, n := range dto.Destination.Present {
			t.Row(output.SafeCell(n.URN), output.SafeCell(n.Name))
		}
		return t.Flush()
	}); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return exitcode.Newf(exitcode.Error, "INCOMPLETE: advisory move read failed: %v", api.MapError(err))
	}
	return nil
}

func readMovePreview(cmd *cobra.Command, client graphql.Client, memory, source string, targetURN, targetMemory *string) ([]movePreviewNode, []movePreviewNode, error) {
	ref, err := cmdutil.ResolveNodeRef(cmd, client, memory, source)
	if err != nil {
		return nil, nil, err
	}
	root, err := gen.MovePreviewSource(cmd.Context(), client, ref)
	if err != nil {
		return nil, nil, err
	}
	if root.Node == nil || root.Node.Id == "" || root.Node.Loc == "" || root.Node.MemoryId == "" {
		return nil, nil, fmt.Errorf("source not available")
	}
	destLoc := root.Node.Loc
	destMemory := ""
	if targetURN != nil {
		parts, e := urnlib.SplitNodeUrn(*targetURN)
		if e != nil {
			return nil, nil, e
		}
		destMemory = cmdutil.CanonicalMemoryRef(parts.MemoryURN)
		destLoc = parts.Loc
	} else {
		destMemory = *targetMemory
	}
	mem, err := gen.MovePreviewMemory(cmd.Context(), client, destMemory)
	if err != nil {
		return nil, nil, err
	}
	if mem.Memory == nil || mem.Memory.Id == "" {
		return nil, nil, fmt.Errorf("destination memory not available")
	}
	nodes, err := readMoveSubtree(cmd, client, root.Node.MemoryId, root.Node.Loc)
	if err != nil {
		return nil, nil, err
	}
	foundRoot := false
	for _, n := range nodes {
		if n.URN == root.Node.Urn {
			foundRoot = true
		}
	}
	if !foundRoot {
		return nil, nil, fmt.Errorf("source no longer appears in the readable subtree")
	}
	present, err := readMoveSubtree(cmd, client, mem.Memory.Id, destLoc)
	if err != nil {
		return nil, nil, err
	}
	return nodes, present, nil
}

// PageNodes detects a stalled/repeated page and reads until an empty page,
// independent of a possibly stale total. The prefix filter is only a candidate
// selector; colon-boundary membership is checked before hydrating candidates.
func readMoveSubtree(cmd *cobra.Command, client graphql.Client, memory, prefix string) ([]movePreviewNode, error) {
	nodes := []movePreviewNode{}
	err := api.PageNodes(api.NodeBatchCap, func(limit, offset int) ([]*api.ListNode, error) {
		page, err := gen.MovePreviewList(cmd.Context(), client, memory, prefix, limit, offset)
		if err != nil {
			return nil, err
		}
		if page.FindNodes == nil {
			return nil, fmt.Errorf("node listing unavailable")
		}
		candidates := []*api.ListNode{}
		refs := []string{}
		byID := map[string]*api.ListNode{}
		for _, h := range page.FindNodes.Hits {
			if h == nil || h.Node == nil || h.Node.Id == "" || h.Node.MemoryId != memory {
				return nil, fmt.Errorf("node listing contains an unavailable row")
			}
			n := &api.ListNode{Id: h.Node.Id, MemoryId: h.Node.MemoryId, Loc: h.Node.Loc}
			candidates = append(candidates, n)
			if n.Loc == prefix || strings.HasPrefix(n.Loc, prefix+":") {
				refs = append(refs, n.Id)
				byID[n.Id] = n
			}
		}
		if len(refs) > 0 {
			batch, e := gen.MovePreviewBatch(cmd.Context(), client, refs)
			if e != nil {
				return nil, e
			}
			b := batch.NodeBatch
			if b == nil || b.Truncated || len(b.Omitted) > 0 || len(b.Unavailable) > 0 {
				return nil, fmt.Errorf("node batch is incomplete (truncated, omitted or unavailable)")
			}
			seen := map[string]bool{}
			for _, n := range b.Nodes {
				if n == nil {
					return nil, fmt.Errorf("node batch contains an unavailable row")
				}
				prior, ok := byID[n.Id]
				if !ok || seen[n.Id] || n.MemoryId != memory || n.Loc != prior.Loc || n.Urn == "" {
					return nil, fmt.Errorf("node changed during the read or batch identity is incomplete")
				}
				seen[n.Id] = true
				nodes = append(nodes, movePreviewNode{URN: n.Urn, Name: n.Name})
			}
			if len(seen) != len(byID) {
				return nil, fmt.Errorf("node batch did not return every requested node")
			}
		}
		return candidates, nil
	}, func(*api.ListNode) bool { return true })
	return nodes, err
}
