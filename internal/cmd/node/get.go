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
		Long: `Show a node by its fully-qualified URN: hrn:node:<root>:<slug>:<loc>
(e.g. hrn:node:hadronmemory.com:dev:start-here). The prefix is REQUIRED for
this flat form — scheme-less <org>:<memory>:<loc> is ambiguous, since a loc
carries its own single colons. The legacy scheme-less <org>::<memory>::<loc>
(and urn:node:) remain accepted. Pass -m/--memory to name a
node by a bare <loc> within that memory instead; without -m a bare loc
is rejected, since the same loc can exist in several memories.

Pass SEVERAL refs to read them together. They are sent as one batched read —
the server takes a URN as happily as an id, so nothing is resolved first — and a
ref that is missing or unreadable is reported under "unavailable" rather than
failing the whole read. Up to 200 nodes that is a single request; beyond the
server's cap the set is split into as few requests as the cap allows.

--prefix <loc> -m <memory> reads a whole subtree — every node whose loc starts
with the prefix — in a SINGLE call, without naming each node. That is the way
to pull a branch you have not enumerated, and unlike ` + "`node list`" + ` it
returns full content and edges. An empty --prefix means the whole memory. The
server caps the node count and fails loudly rather than truncating silently.

With one ref the output is the node object, unchanged. With several refs, or
with --prefix, it is {nodes, unavailable}. "unavailable" names refs that are
missing OR not readable by you — the server reports those identically — and any
unavailable ref exits 4, so a partial read is never mistaken for a complete one.

A malformed ref is a different thing and fails the whole call with exit 2, so a
typo never hides among the denials.

A server or transport failure is neither: it exits 1 (or 7 when the request
never got an answer). Named because it is the code a caller is least likely to
guess, and the one that means "this says nothing about whether the node
exists" — #334 was filed after an intermittent 502 was read as missing data.
Under --json the envelope explaining it is on stdout with the rest.

One difference to know about: a batched read returns content RAW, with Mustache
templates left uncompiled, while a single-ref read compiles them. That is the
server's design — the batch is a bulk SOURCE read for lint/audit/migration, and
compiling per node would reintroduce the N+1 it removes. Identical for a node
without templates; for a template node the batch gives you the source.`,
		Example: `  hadron node get hrn:node:hadronmemory.com:dev:start-here
  hadron node get start-here -m hrn:mem:hadronmemory.com:dev --json
  hadron node get start-here preflight instructions -m hrn:mem:hadronmemory.com:dev --json
  hadron node get --prefix findings: -m hrn:mem:hadronmemory.com:dev --json`,
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
				return exitcode.Newf(exitcode.Usage, "--prefix needs -m/--memory hrn:mem:<root>:<slug> to scope the subtree")
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
				id, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
				if err != nil {
					return err
				}
				var dto nodeDetailDTO
				err = readConsistently(cmd, client, revisionSelector{refs: []string{id}}, func() ([]*nodeDetailDTO, error) {
					node, err := fetchNodeByID(cmd, client, id, args[0])
					if err != nil {
						return nil, err
					}
					dto = detailDTO(node)
					return []*nodeDetailDTO{&dto}, nil
				})
				if err != nil {
					return err
				}
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					return renderNodeDetail(w, dto)
				})
			}

			sel, err := batchRevisionSelector(memory, locPrefix, args, prefixMode)
			if err != nil {
				return err
			}
			var dto nodeBatchDTO
			err = readConsistently(cmd, client, sel, func() ([]*nodeDetailDTO, error) {
				nodes, unavailable, err := fetchNodeBatch(cmd, client, memory, locPrefix, args, prefixMode)
				if err != nil {
					return nil, err
				}
				dto = nodeBatchDTO{Nodes: []nodeDetailDTO{}, Unavailable: []string{}}
				for _, n := range nodes {
					if n == nil {
						continue
					}
					dto.Nodes = append(dto.Nodes, batchDetailDTO(n))
				}
				dto.Unavailable = append(dto.Unavailable, unavailable...)
				ptrs := make([]*nodeDetailDTO, len(dto.Nodes))
				for i := range dto.Nodes {
					ptrs[i] = &dto.Nodes[i]
				}
				return ptrs, nil
			})
			if err != nil {
				return err
			}
			return emitNodeBatch(f, dto)
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve a bare <loc> against")
	cmd.Flags().StringVar(&locPrefix, "prefix", "", "read every node under this loc prefix in one call (needs -m; empty means the whole memory)")
	return cmd
}

// renderNodeDetail prints one node in the human layout. Shared so a node read
// on its own and the same node read in a batch look identical.
func renderNodeDetail(w io.Writer, dto nodeDetailDTO) error {
	fmt.Fprintf(w, "%s\n  loc: %s\n", dto.Name, dto.Loc)
	// The fully-qualified URN, then the link that opens it, in that order and
	// adjacent (#515). This is the framing the MCP node read already uses, and
	// what every role-agent briefing means by "never hand-build a portal link —
	// copy the URL line a node read prints". Until now the CLI's node read
	// printed neither, so that instruction was unsatisfiable here.
	//
	// Each is printed only when the server sent it. No placeholder, no dash,
	// and above all no locally-composed fallback: a link to a guessed origin
	// fails silently for whoever clicks it (cor:api:230:01). The URN survives
	// the link's absence — they are independent answers, and losing the link
	// must not cost the caller the reference.
	if dto.URN != "" {
		fmt.Fprintf(w, "  urn: %s\n", dto.URN)
	}
	if dto.PortalURL != "" {
		fmt.Fprintf(w, "  URL: %s\n", dto.PortalURL)
	}
	fmt.Fprintf(w, "  type: %s\n", dto.NodeType)
	if dto.ObjectType != nil && *dto.ObjectType != "" {
		fmt.Fprintf(w, "  object-type: %s\n", *dto.ObjectType)
	}
	fmt.Fprintf(w, "  runnable: %t\n", dto.IsRunnable)
	// Printed whenever the field is NON-NULL. Ungoverned (null) is the
	// overwhelming majority and a `role: -` line on every node would be noise;
	// anything else changes which door may write the node, so it earns a line.
	//
	// An EMPTY role is shown too, and shown as odd (@copilot on #615). It is a
	// state `--role` refuses to create, but one the generic surface and MCP can
	// still produce, and it matches no kind — so hiding it alongside null would
	// make the one node a reader most needs to notice look ordinary.
	if dto.Role != nil {
		if *dto.Role == "" {
			fmt.Fprintf(w, "  role: \"\" (empty — matches no kind; clear it to null or set a value)\n")
		} else {
			fmt.Fprintf(w, "  role: %s\n", *dto.Role)
		}
	}
	if dto.Description != nil && *dto.Description != "" {
		fmt.Fprintf(w, "  about: %s\n", *dto.Description)
	}
	if len(dto.Tags) > 0 {
		fmt.Fprintf(w, "  tags: %v\n", dto.Tags)
	}
	fmt.Fprintf(w, "  updated: %s\n", dto.UpdatedAt)
	if dto.Revision != nil {
		fmt.Fprintf(w, "  revision: %d\n", *dto.Revision)
	} else {
		fmt.Fprintln(w, "  revision: unknown (the server predates node revisions)")
	}
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
			ID: n.Id,
			// urn is non-nullable server-side; portalUrl is not, and strVal
			// keeps an absent one out of the --json shape (omitempty) rather
			// than emitting "".
			URN:        n.Urn,
			PortalURL:  strVal(n.PortalUrl),
			MemoryID:   n.MemoryId,
			Loc:        n.Loc,
			Name:       n.Name,
			NodeType:   n.NodeType,
			Tags:       n.Tags,
			IsRunnable: boolVal(n.IsRunnable),
			Role:       n.Role,
			UpdatedAt:  n.UpdatedAt,
		},
		ObjectType:         n.ObjectType,
		Description:        n.Description,
		Abstract:           n.Abstract,
		AbstractOriginHash: n.AbstractOriginHash,
		Content:            n.Content,
		Data:               n.Data,
		Properties:         n.Properties,
		Seq:                n.Seq,
		CreatedAt:          n.CreatedAt,
		OutgoingEdges:      []edgeRefDTO{},
		IncomingEdges:      []edgeRefDTO{},
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

// consistentReadAttempts bounds readConsistently's retries.
const consistentReadAttempts = 3

// revisionSelector names the same nodes the content read names: explicit
// refs (ids or canonical refs), or a memory + loc prefix.
type revisionSelector struct {
	refs   []string
	memory *string
	prefix *string
}

// readConsistently reads nodes BETWEEN two revision reads and keeps them only
// when every node's revision is the same before and after (@codex on #724).
// A revision advances on every authoring change, so equal brackets mean the
// content read IS that revision; a timestamp comparison could not promise
// that, since two writes can share one. Otherwise the whole read repeats, and
// after the last attempt it fails rather than print a pairing it could not
// verify.
//
// Against a server that predates revisions every Revision stays nil — null
// means exactly that, never a guess and never a race — and the content is
// read once, as before.
func readConsistently(cmd *cobra.Command, client graphql.Client, sel revisionSelector, read func() ([]*nodeDetailDTO, error)) error {
	for attempt := 1; ; attempt++ {
		before, supported, err := liveRevisions(cmd, client, sel)
		if err != nil {
			return err
		}
		dtos, err := read()
		if err != nil || !supported {
			return err
		}
		ids := make([]string, 0, len(dtos))
		for _, d := range dtos {
			ids = append(ids, d.ID)
		}
		after, supported, err := liveRevisions(cmd, client, revisionSelector{refs: ids})
		if err != nil {
			return err
		}
		// The before-read HAD revisions, so an after-read without them is a
		// server that changed under the read (a rolling or mixed
		// deployment), not an older server: null would falsely say it
		// predates revisions. Treated as a change, and read again (@copilot
		// on #724).
		if supported && pairRevisions(dtos, before, after) {
			return nil
		}
		if attempt == consistentReadAttempts {
			return exitcode.Newf(exitcode.Conflict,
				"the node changed while it was being read, %d times running, so its revision could not be paired with its content; try again", consistentReadAttempts)
		}
	}
}

// pairRevisions sets each DTO's Revision when its node carried the same
// revision before and after the content read, and reports whether every node
// did. A node absent from either read (created, deleted or made unavailable in
// between) is a change like an edit.
func pairRevisions(dtos []*nodeDetailDTO, before, after map[string]int) bool {
	for _, d := range dtos {
		b, okB := before[d.ID]
		a, okA := after[d.ID]
		if !okB || !okA || a != b {
			return false
		}
	}
	for _, d := range dtos {
		rev := after[d.ID]
		d.Revision = &rev
	}
	return true
}

// liveRevisions reads id → revision for the nodes sel names. supported is
// false when the server predates the field (#1323). Explicit refs go in
// api.NodeBatchCap-sized calls; a prefix read's byte-cap spillover is
// re-read by id. Any other failure, including a null envelope, is the
// command's error.
func liveRevisions(cmd *cobra.Command, client graphql.Client, sel revisionSelector) (revs map[string]int, supported bool, _ error) {
	revs = map[string]int{}
	ask := func(refs []string, memory, prefix *string) (*gen.NodeLiveRevisionsNodeBatchNodeBatchResult, bool, error) {
		resp, err := gen.NodeLiveRevisions(cmd.Context(), client, refs, memory, prefix)
		if err != nil {
			if isUnknownFieldErr(err, "revision") {
				return nil, false, nil
			}
			return nil, false, api.MapError(err)
		}
		if resp.NodeBatch == nil {
			return nil, false, exitcode.Newf(exitcode.Error, "the server returned no result for the node revision read")
		}
		return resp.NodeBatch, true, nil
	}
	collect := func(r *gen.NodeLiveRevisionsNodeBatchNodeBatchResult) {
		for _, n := range r.Nodes {
			if n != nil {
				revs[n.Id] = n.Revision
			}
		}
	}
	byRefs := func(refs []string) (bool, error) {
		for start := 0; start < len(refs); start += api.NodeBatchCap {
			end := min(start+api.NodeBatchCap, len(refs))
			r, ok, err := ask(refs[start:end], nil, nil)
			if err != nil || !ok {
				return ok, err
			}
			collect(r)
		}
		return true, nil
	}
	if sel.prefix != nil {
		r, ok, err := ask(nil, sel.memory, sel.prefix)
		if err != nil || !ok {
			return nil, ok, err
		}
		collect(r)
		if r.Truncated {
			if ok, err := byRefs(r.Omitted); err != nil || !ok {
				return nil, ok, err
			}
		}
		return revs, true, nil
	}
	ok, err := byRefs(sel.refs)
	if err != nil || !ok {
		return nil, ok, err
	}
	return revs, true, nil
}

// fetchNodeByID reads one node by its resolved id; ref is what the caller
// typed, for the not-found message.
func fetchNodeByID(cmd *cobra.Command, client graphql.Client, id, ref string) (*gen.GetNodeNode, error) {
	resp, err := gen.GetNode(cmd.Context(), client, id)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.Node == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "node %q not found", ref)
	}
	return resp.Node, nil
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
