package node

import (
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	var memory, locPrefix string
	cmd := &cobra.Command{
		Use:   "get <node-urn>... | <loc>... -m <memory> | --prefix <loc> -m <memory>",
		Short: "Show one or more nodes, including content and edges",
		Long: `Show a node by its fully-qualified URN: <org>::<memory>::<loc>
(e.g. hadronmemory.com::dev::start-here). The hrn:node: prefix is
optional (legacy urn:node: also accepted). Pass -m/--memory to name a
node by a bare <loc> within that memory instead; without -m a bare loc
is rejected, since the same loc can exist in several memories.

Pass SEVERAL refs to read them together. The bodies then come back in one
batched call instead of one round trip each, and a ref that is missing or
unreadable is reported under "unavailable" rather than failing the whole read.

--prefix <loc> -m <memory> reads a whole subtree — every node whose loc starts
with the prefix — in a SINGLE call with no per-node resolution. This is the
cheapest way to pull a branch, and unlike ` + "`node list`" + ` it returns full
content and edges. An empty --prefix means the whole memory. The server caps
the node count and fails loudly rather than truncating silently.

With one ref the output is the node object, unchanged. With several refs, or
with --prefix, it is {nodes, unavailable}. Any unavailable ref exits 4, so a
partial read is never mistaken for a complete one.`,
		Example: `  hadron node get hadronmemory.com::dev::start-here
  hadron node get start-here -m hadronmemory.com::dev --json
  hadron node get start-here preflight instructions -m hadronmemory.com::dev --json
  hadron node get --prefix findings: -m hadronmemory.com::dev --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			prefixMode := cmd.Flags().Changed("prefix")
			// The two forms are mutually exclusive server-side, so reject the
			// combination here with a message that names both rather than
			// letting the server answer with its own phrasing.
			if prefixMode && len(args) > 0 {
				return exitcode.Newf(exitcode.Usage,
					"--prefix reads a subtree and cannot be combined with explicit node refs — drop one")
			}
			if prefixMode && strings.TrimSpace(memory) == "" {
				return exitcode.Newf(exitcode.Usage, "--prefix needs -m/--memory <org::memory> to scope the subtree")
			}
			if !prefixMode && len(args) == 0 {
				return exitcode.Newf(exitcode.Usage,
					"specify at least one node ref, or --prefix <loc> -m <memory> to read a subtree")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			// One ref keeps the single-node shape: that output is a stable
			// contract and callers of `node get <ref>` must not have to care
			// that a batch form now exists.
			if !prefixMode && len(args) == 1 {
				node, err := fetchNode(cmd, client, memory, args[0])
				if err != nil {
					return err
				}
				dto := detailDTO(node)
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					return renderNodeDetail(w, dto)
				})
			}

			nodes, unavailable, err := fetchNodeBatch(cmd, client, memory, locPrefix, args, prefixMode)
			if err != nil {
				return err
			}
			dto := nodeBatchDTO{Nodes: []nodeDetailDTO{}, Unavailable: []string{}}
			for _, n := range nodes {
				if n == nil {
					continue
				}
				dto.Nodes = append(dto.Nodes, batchDetailDTO(n))
			}
			dto.Unavailable = append(dto.Unavailable, unavailable...)
			return emitNodeBatch(f, dto)
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> against")
	cmd.Flags().StringVar(&locPrefix, "prefix", "", "read every node under this loc prefix in one call (needs -m; empty means the whole memory)")
	return cmd
}

// renderNodeDetail prints one node in the human layout. Shared so a node read
// on its own and the same node read in a batch look identical.
func renderNodeDetail(w io.Writer, dto nodeDetailDTO) error {
	fmt.Fprintf(w, "%s\n  loc: %s\n  type: %s\n", dto.Name, dto.Loc, dto.NodeType)
	if dto.ObjectType != nil && *dto.ObjectType != "" {
		fmt.Fprintf(w, "  object-type: %s\n", *dto.ObjectType)
	}
	fmt.Fprintf(w, "  runnable: %t\n", dto.IsRunnable)
	if dto.Description != nil && *dto.Description != "" {
		fmt.Fprintf(w, "  about: %s\n", *dto.Description)
	}
	if len(dto.Tags) > 0 {
		fmt.Fprintf(w, "  tags: %v\n", dto.Tags)
	}
	fmt.Fprintf(w, "  updated: %s\n", dto.UpdatedAt)
	if dto.Data != nil && len(*dto.Data) > 0 {
		if dataStr := string(*dto.Data); dataStr != "null" {
			fmt.Fprintf(w, "  data: %s\n", dataStr)
		}
	}
	if dto.Properties != nil && len(*dto.Properties) > 0 {
		if propStr := string(*dto.Properties); propStr != "null" {
			fmt.Fprintf(w, "  properties: %s\n", propStr)
		}
	}
	if len(dto.OutgoingEdges) > 0 || len(dto.IncomingEdges) > 0 {
		fmt.Fprintln(w, "  edges:")
		for _, e := range dto.OutgoingEdges {
			fmt.Fprintf(w, "    → %s (%s)\n", e.Loc, edgeRel(e))
		}
		for _, e := range dto.IncomingEdges {
			fmt.Fprintf(w, "    ← %s (%s)\n", e.Loc, edgeRel(e))
		}
	}
	if dto.Content != nil && *dto.Content != "" {
		fmt.Fprintf(w, "\n%s\n", *dto.Content)
	} else if dto.Abstract != nil && *dto.Abstract != "" {
		fmt.Fprintf(w, "\n(abstract)\n%s\n", *dto.Abstract)
	}
	return nil
}

// edgeRefDTO is one edge endpoint in node output.
type edgeRefDTO struct {
	EdgeID     string `json:"edgeId"`
	Name       string `json:"name"`
	EdgeLoc    string `json:"edgeLoc"`
	IsRunnable bool   `json:"isRunnable"`
	Priority   int    `json:"priority"`
	NodeID     string `json:"nodeId"`
	Loc        string `json:"loc"`
	MemoryID   string `json:"memoryId"`
}

// edgeRel is the relationship shown for an edge: its name, or its loc when the
// name is empty (spec 037).
func edgeRel(e edgeRefDTO) string {
	if e.Name != "" {
		return e.Name
	}
	return e.EdgeLoc
}

func edgeRefOf(edgeID string, name *string, edgeLoc string, isRunnable *bool, priority int, nodeID, loc, memoryID string) edgeRefDTO {
	n := ""
	if name != nil {
		n = *name
	}
	run := false
	if isRunnable != nil {
		run = *isRunnable
	}
	return edgeRefDTO{
		EdgeID: edgeID, Name: n, EdgeLoc: edgeLoc, IsRunnable: run, Priority: priority,
		NodeID: nodeID, Loc: loc, MemoryID: memoryID,
	}
}

func detailDTO(n *gen.GetNodeNode) nodeDetailDTO {
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
		// #781: target/source is null when its memory is unreadable to the
		// caller (a cross-memory edge's far endpoint) — surface a blank ref
		// rather than dereferencing nil.
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

// fetchNode resolves a node reference (a full URN, or a bare loc within
// memory) and returns the full node.
func fetchNode(cmd *cobra.Command, client graphql.Client, memory, ref string) (*gen.GetNodeNode, error) {
	id, err := cmdutil.ResolveNodeRef(cmd, client, memory, ref)
	if err != nil {
		return nil, err
	}
	resp, err := gen.GetNode(cmd.Context(), client, id)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.Node == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "node %q not found", ref)
	}
	return resp.Node, nil
}
