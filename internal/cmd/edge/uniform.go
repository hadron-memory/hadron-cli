package edge

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// The uniform edge reads (#473, cli#1341): edges as first-class,
// loc-addressed peers of nodes (spec 037).

// uniformEdgeDTO is the stable --json shape of one edge read through the
// uniform surface: edge get, and each row of edge list without a node. An
// endpoint in a memory the caller cannot read (or a deleted one) comes back
// null from the server, and its id and loc are then "".
type uniformEdgeDTO struct {
	ID          string  `json:"id"`
	URN         string  `json:"urn"`
	Loc         string  `json:"loc"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	IsRunnable  bool    `json:"isRunnable"`
	Priority    int     `json:"priority"`
	SourceID    string  `json:"sourceId"`
	SourceLoc   string  `json:"sourceLoc"`
	TargetID    string  `json:"targetId"`
	TargetLoc   string  `json:"targetLoc"`
}

// edgeListPageDTO is the stable --json shape of edge list without a node.
type edgeListPageDTO struct {
	Total int              `json:"total"`
	Edges []uniformEdgeDTO `json:"edges"`
}

// uniformEdgePageSize bounds one page of the whole-list scan (the server caps
// a page at 200).
const uniformEdgePageSize = 200

func uniformEdgeFrom(e gen.UniformEdge) uniformEdgeDTO {
	d := uniformEdgeDTO{ID: e.Id, URN: e.Urn, Loc: e.Loc, Description: e.Description, Priority: e.Priority}
	if e.Name != nil {
		d.Name = *e.Name
	}
	if e.IsRunnable != nil {
		d.IsRunnable = *e.IsRunnable
	}
	if e.Source != nil {
		d.SourceID, d.SourceLoc = e.Source.Id, e.Source.Loc
	}
	if e.Target != nil {
		d.TargetID, d.TargetLoc = e.Target.Id, e.Target.Loc
	}
	return d
}

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <edge-id> | <edge-urn>",
		Short: "Show one edge",
		Long: `Show one edge by its id (the EDGE-ID column of edge list) or its URN
(hrn:edge:<root>:<memory>:<loc>, where the memory is the source node's).

An edge you cannot read and an edge that does not exist give the same "not
found" (exit 4). A node at either end that you cannot read, or that was
deleted, is shown without its id and loc.`,
		Example: `  hadron edge get hrn:edge:acme.com:kb:findings:flaky-ci:routes-to:start-here
  hadron edge get <edge-id> --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "an edge id or URN is required")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GetEdge(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Edge == nil {
				return exitcode.Newf(exitcode.NotFound, "edge %q not found — or you cannot read its memory", args[0])
			}
			dto := uniformEdgeFrom(resp.Edge.UniformEdge)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("URN", output.SafeCell(dto.URN))
				t.Row("ID", output.SafeCell(dto.ID))
				t.Row("NAME", output.SafeCell(dto.Name))
				t.Row("FROM", output.SafeCell(dto.SourceLoc))
				t.Row("TO", output.SafeCell(dto.TargetLoc))
				if dto.Description != nil {
					t.Row("DESCRIPTION", output.SafeCell(*dto.Description))
				}
				t.Row("RUNNABLE", fmt.Sprint(dto.IsRunnable))
				t.Row("PRIORITY", fmt.Sprint(dto.Priority))
				return t.Flush()
			})
		},
	}
}

// uniformListFlags are edge list's flags for the no-node form.
type uniformListFlags struct {
	source, target, org string
	runnable            bool
	includeDeleted      bool
	limit, offset       int
}

// uniformOnly are the flags that apply only without a node, and nodeOnly the
// ones that apply only with one.
var (
	uniformOnly = []string{"source", "target", "runnable", "include-deleted", "org", "limit", "offset"}
	nodeOnly    = []string{"direction", "name", "to", "from"}
)

// listUniformEdges is edge list with no node: the uniform edges() list,
// paged to exhaustion (#23) unless --limit or --offset asks for one page.
func listUniformEdges(cmd *cobra.Command, f *cmdutil.Factory, memory string, u uniformListFlags) error {
	changed := cmd.Flags().Changed
	filter := &gen.EdgeFilter{}
	for name, v := range map[string]string{"memory": memory, "source": u.source, "target": u.target, "org": u.org} {
		if changed(name) && strings.TrimSpace(v) == "" {
			return exitcode.Newf(exitcode.Usage, "--%s was given an empty value — omit the flag to not filter on it. Nothing was sent", name)
		}
	}
	if changed("memory") {
		// Every memory spelling the CLI accepts (v2, <org>:<slug>, legacy
		// <org>::<slug>, an id) normalizes as it does for -m elsewhere.
		m := cmdutil.CanonicalMemoryRef(memory)
		filter.MemoryId = &m
	}
	// --source and --target go to the server as typed, and a ref it cannot
	// resolve matches no edge — an empty list, not "not found". So take only
	// the two forms that name a node on their own (an id or a qualified node
	// URN) and refuse a bare loc, which would silently list nothing.
	for _, name := range []string{"source", "target"} {
		v := strings.TrimSpace(map[string]string{"source": u.source, "target": u.target}[name])
		if changed(name) && !cmdutil.IsEntityID(v) && !cmdutil.IsQualifiedNodeRef(v) {
			return exitcode.Newf(exitcode.Usage, "--%s takes a node id or a node URN (hrn:node:<root>:<memory>:<loc>), not %q. Nothing was sent", name, v)
		}
	}
	if changed("source") {
		s := strings.TrimSpace(u.source)
		filter.SourceNodeId = &s
	}
	if changed("target") {
		t := strings.TrimSpace(u.target)
		filter.TargetNodeId = &t
	}
	if changed("runnable") {
		r := u.runnable
		filter.IsRunnable = &r
	}
	if changed("include-deleted") {
		d := u.includeDeleted
		filter.IncludeDeleted = &d
	}
	var orgArg *string
	if changed("org") {
		o := strings.TrimSpace(u.org)
		orgArg = &o
	}
	if u.limit < 0 || u.offset < 0 {
		return exitcode.Newf(exitcode.Usage, "--limit and --offset must not be negative")
	}
	if changed("limit") && u.limit == 0 {
		return exitcode.Newf(exitcode.Usage, "--limit must be at least 1 (omit it to list every edge)")
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	dto := edgeListPageDTO{Edges: []uniformEdgeDTO{}}
	single := changed("limit") || changed("offset")
	skip, size := u.offset, uniformEdgePageSize
	if changed("limit") {
		size = u.limit
	}
	for {
		resp, err := gen.ListEdges(cmd.Context(), client, filter, &size, &skip, orgArg)
		if err != nil {
			return api.MapError(err)
		}
		dto.Total = resp.Edges.Total
		for _, e := range resp.Edges.Items {
			if e != nil {
				dto.Edges = append(dto.Edges, uniformEdgeFrom(e.UniformEdge))
			}
		}
		skip += len(resp.Edges.Items)
		if single || len(resp.Edges.Items) == 0 || skip >= resp.Edges.Total {
			break
		}
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if len(dto.Edges) == 0 {
			_, err := fmt.Fprintln(w, "no edges")
			return err
		}
		t := output.NewTable(w, "FROM", "REL", "TO", "EDGE-ID")
		for _, e := range dto.Edges {
			rel := e.Name
			if rel == "" {
				rel = e.Loc
			}
			t.Row(output.SafeCell(e.SourceLoc), output.SafeCell(rel), output.SafeCell(e.TargetLoc), output.SafeCell(e.ID))
		}
		return t.Flush()
	})
}
