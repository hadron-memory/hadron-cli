package spec

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type labelReportDTO struct {
	MemoryRef string            `json:"memoryRef"`
	Prefix    string            `json:"prefix"`
	Items     []labelReportItem `json:"items"`
}
type labelReportItem struct {
	ParentID  string  `json:"parentId"`
	ParentLoc string  `json:"parentLoc"`
	EdgeID    *string `json:"edgeId"`
	Label     *string `json:"label"`
	ChildID   *string `json:"childId"`
	ChildLoc  *string `json:"childLoc"`
	ChildName *string `json:"childName"`
	Status    string  `json:"status"`
}

func newCmdLabelReport(f *cmdutil.Factory) *cobra.Command {
	var memory, prefix string
	c := &cobra.Command{Use: "label-report", Short: "Inventory visible citation-parent edge labels without lint judgments",
		Example: `  hadron spec label-report -m hrn:mem:acme.com:specs
  hadron spec label-report -m hrn:mem:acme.com:specs --prefix msg:cha --json`,
		Long: `Read-only inventory of the selected spec corpus, paging to exhaustion.

Each spec points at its citation parent with an edge (child → parent: the
edge from msg:cha:010:01 to msg:cha:010), and each edge carries a label,
normally the child's title. For every parent you can see, this command lists
the incoming edges from its citation children, with their labels, so you can
eyeball labels that disagree with the child's name. It is an inventory, not a
lint: it never says a label is wrong, and it is not a complete check of the
corpus (it shows only what you may read). The memory is -m, else the one chosen
by ` + "`hadron spec use`" + `. Example line:

  verified direct relationship: msg:cha:010 edge="<edge id>" label="W4 7d check-in"
    child="msg:cha:010:01" ("<node id>") name="msg:cha:010:01 — W4 7d check-in" status=available

"verified direct relationship" is a child in the same memory that you can read
as a citation source; it stays verified even when the child's current name
cannot be read (then status=unavailable and the name is empty).
"unclassified candidate" is an edge whose source you cannot read, so its child
fields are null. An empty label and a missing label are reported as different
things.

Details. Verified direct relationships require a readable same-memory source whose citation
parent is this node. Unequal editorial labels are not defects. No history,
heuristic, lint finding, edge rewrite or mutation is performed.

A visible same-memory direct citation source establishes the relationship even when its current name cannot be read; in that case unavailable describes the current name.
Every incoming edge satisfying that source condition is inventoried, regardless of its editorial label; no special edge kind is selected.

An edge with a concealed source is an unclassified candidate (child fields null);
a listed parent that cannot be batch-read has a marker with edgeId/label/child fields null. Empty and
null labels/names remain distinct. No hidden endpoint lookup is attempted.
Transport/protocol failures fail the command, without printing a partial report.
The report describes caller-visible data only, not complete hidden corpus coverage.
--prefix selects the parent citation branch; a direct child may be beneath it.`, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			prefix, err := validateSpecPrefix(prefix, cmd.Flags().Changed("prefix"))
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			mem, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}
			var pref *string
			if prefix != "" {
				pref = &prefix
			}
			listed, err := scanLabelSpecs(cmd.Context(), client, &mem, pref)
			if err != nil {
				return err
			}
			refs := make([]string, 0, len(listed))
			scope := map[string]*api.ListNode{}
			for _, n := range listed {
				if n != nil && underPrefix(n.Loc, prefix) {
					if _, e := ParseCitation(n.Loc); e == nil {
						refs = append(refs, n.Id)
						scope[n.Id] = n
					}
				}
			}
			nodes, _, err := api.CollectNodeBatch(refs, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
				r, e := api.ReadNodeBatch(cmd.Context(), client, chunk, nil, nil)
				if e != nil {
					return nil, api.MapError(e)
				}
				if e := validateLabelBatch(chunk, r.NodeBatch); e != nil {
					return nil, e
				}
				return r.NodeBatch, nil
			})
			if err != nil {
				return err
			}
			full := map[string]*gen.NodeBatchNodeBatchNodeBatchResultNodesNode{}
			for _, n := range nodes {
				if n != nil {
					if expected, ok := scope[n.Id]; !ok || expected.Loc != n.Loc || expected.MemoryId != n.MemoryId {
						return exitcode.Newf(exitcode.Unavailable, "batch returned a node outside the listed scope")
					}
					full[n.Id] = n
				}
			}
			items := collectLabelItems(scope, full)
			result := labelReportDTO{MemoryRef: cmdutil.CanonicalMemoryRef(mem), Prefix: prefix, Items: items}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				source := "the selected spec memory"
				if memory != "" {
					source = "--memory"
				}
				if _, e := fmt.Fprintf(w, "memory: %s (from %s)\n", result.MemoryRef, source); e != nil {
					return e
				}
				if _, e := fmt.Fprintln(w, "Visible citation-parent labels (inventory only; unequal labels are not defects):"); e != nil {
					return e
				}
				if len(items) == 0 {
					_, e := fmt.Fprintln(w, "No visible direct citation-parent edges.")
					return e
				}
				for _, i := range items {
					qualifier := "verified direct relationship"
					if i.Status == "unavailable" {
						qualifier = "unavailable child: current name unavailable"
					}
					if i.EdgeID == nil {
						qualifier = "unavailable-parent: edges unavailable"
					} else if i.ChildID == nil {
						qualifier = "unclassified candidate: source unavailable"
					}
					if _, e := fmt.Fprintf(w, "%s: ", qualifier); e != nil {
						return e
					}
					if _, e := fmt.Fprintf(w, "%s edge=%s label=%s child=%s (%s) name=%s status=%s\n", i.ParentLoc, labelValue(i.EdgeID), labelValue(i.Label), labelValue(i.ChildLoc), labelValue(i.ChildID), labelValue(i.ChildName), i.Status); e != nil {
						return e
					}
				}
				return nil
			})
		}}
	c.Flags().StringVarP(&memory, "memory", "m", "", "spec memory ID or fully-qualified URN (defaults to the selected spec memory)")
	c.Flags().StringVar(&prefix, "prefix", "", "restrict visible parents to this citation branch")
	return c
}

func labelValue(s *string) string {
	if s == nil {
		return "null"
	}
	return strconv.Quote(*s)
}

func collectLabelItems(scope map[string]*api.ListNode, full map[string]*gen.NodeBatchNodeBatchNodeBatchResultNodesNode) []labelReportItem {
	items := []labelReportItem{}
	for id, listed := range scope {
		parent := full[id]
		if parent == nil {
			items = append(items, labelReportItem{ParentID: id, ParentLoc: listed.Loc, Status: "unavailable"})
			continue
		}
		for _, edge := range parent.IncomingEdges {
			if edge == nil {
				continue
			}
			if edge.Source == nil {
				items = append(items, labelReportItem{ParentID: parent.Id, ParentLoc: parent.Loc, EdgeID: &edge.Id, Label: edge.Name, Status: "unavailable"})
				continue
			}
			src := edge.Source
			if src.MemoryId != parent.MemoryId {
				continue
			}
			citation, e := ParseCitation(src.Loc)
			if e != nil {
				continue
			}
			p, ok := citation.Parent()
			if !ok || p.Format() != parent.Loc {
				continue
			}
			item := labelReportItem{ParentID: parent.Id, ParentLoc: parent.Loc, EdgeID: &edge.Id, Label: edge.Name, ChildID: &src.Id, ChildLoc: &src.Loc, Status: "unavailable"}
			// Never fetch a hidden/unlisted endpoint. Only an independently visible
			// corpus member that survived batch access checks supplies its current name.
			if child := full[src.Id]; child != nil && child.MemoryId == parent.MemoryId && child.Loc == src.Loc {
				name := child.Name
				item.ChildName = &name
				item.Status = "available"
			}
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.ParentLoc != b.ParentLoc {
			return a.ParentLoc < b.ParentLoc
		}
		return labelValue(a.EdgeID) < labelValue(b.EdgeID)
	})
	return items
}

// Use the existing marker union and pagination, but require a real listing
// envelope: data:null is unavailable, not an empty corpus.
func scanLabelSpecs(ctx context.Context, client graphql.Client, memory, prefix *string) ([]*api.ListNode, error) {
	nodes, err := collectSpecNodes(memory, prefix, func(filter *gen.NodeFilter) ([]*api.ListNode, error) {
		return paginateNodes(func(limit, offset int) ([]*api.ListNode, error) {
			r, e := gen.FindNodes(ctx, client, nil, nil, filter, sortLoc(), nil, &limit, &offset, false, false)
			if e != nil {
				return nil, e
			}
			if r == nil || r.FindNodes == nil {
				return nil, exitcode.Newf(exitcode.Unavailable, "server returned no listing; label report incomplete")
			}
			out := []*api.ListNode{}
			for _, h := range r.FindNodes.Hits {
				if h == nil || h.Node == nil {
					return nil, exitcode.Newf(exitcode.Unavailable, "server returned an unavailable listing hit; label report incomplete")
				}
				out = append(out, h.Node)
			}
			return out, nil
		})
	})
	return nodes, api.MapError(err)
}

// Spillover must strictly partition a requested chunk. Reject foreign/repeated
// refs rather than let a malformed provider response repeat the queue forever.
func validateLabelBatch(chunk []string, r *gen.NodeBatchNodeBatchNodeBatchResult) error {
	if r == nil {
		return exitcode.Newf(exitcode.Unavailable, "server returned no batch; label report incomplete")
	}
	pending := map[string]bool{}
	for _, id := range chunk {
		pending[id] = true
	}
	take := func(id string) error {
		if !pending[id] {
			return exitcode.Newf(exitcode.Unavailable, "batch returned an unexpected or repeated reference; label report incomplete")
		}
		delete(pending, id)
		return nil
	}
	for _, n := range r.Nodes {
		if n == nil {
			return exitcode.Newf(exitcode.Unavailable, "batch returned a null node; label report incomplete")
		}
		if e := take(n.Id); e != nil {
			return e
		}
	}
	for _, id := range r.Unavailable {
		if e := take(id); e != nil {
			return e
		}
	}
	for _, id := range r.Omitted {
		if e := take(id); e != nil {
			return e
		}
	}
	if len(r.Omitted) > 0 && !r.Truncated {
		return exitcode.Newf(exitcode.Unavailable, "batch omitted references without spillover; label report incomplete")
	}
	if len(pending) > 0 {
		return exitcode.Newf(exitcode.Unavailable, "batch did not account for every requested reference; label report incomplete")
	}
	return nil
}
