// Package coding lints the coding-workflow graph: the review:* checklist tree
// and the preflight router. Both are executable infrastructure — a malformed
// edge label makes a check or a route silently stop firing — so the defects are
// mechanically detectable and worth a linter. See
// docs/plans/coding-command-group.md.
package coding

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Severity levels, spelled as `spec lint` spells them so the two linters'
// --json output is interchangeable.
const (
	sevError   = "error"
	sevWarning = "warning"
)

// Loc of the two roots this group lints. Both are conventional names in a
// coding-workflow memory, overridable per command with --root.
const (
	reviewRootLoc    = "review"
	preflightRootLoc = "preflight"
)

// findingDTO is the --json contract, mirroring spec's lintFindingDTO (node loc,
// rule, severity, message) so agents can treat both linters the same.
type findingDTO struct {
	Node     string `json:"node"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // error | warning
	Message  string `json:"message"`
}

// checkNode is the lint-friendly projection of a node — decoupled from the
// genqlient types so the rule engines unit-test without a server.
type checkNode struct {
	Loc         string
	Name        string
	Description string
	// Content is the node body. The shared NodeBatch operation already selects
	// it, so carrying it costs no extra round trip — it is what lets a label
	// finding quote the trigger paragraph the body already states (#331).
	Content    string
	Tags       []string
	Seq        *int
	IsRunnable bool
}

// graphEdge is one edge incident to a lint root. Other* describe the far
// endpoint — the edge's source for the review tree's incoming edges, its target
// for preflight's outgoing routes (see docs/plans/coding-command-group.md,
// Decision 3: the two subcommands read opposite ends).
//
// OtherID is the endpoint's node id, which is what the far node is read by: a
// route may legitimately cross into another memory, so rebuilding a URN from
// the root's memory would look the wrong node up (or miss it entirely).
// OtherLoc is empty when the server redacted the endpoint projection — an
// unreadable endpoint the linter must report, not skip.
type graphEdge struct {
	ID       string
	Label    string
	Loc      string // the edge's own (usually name-derived) loc
	OtherID  string
	Other    string // the far endpoint's loc; "" when the projection was redacted
	MemoryID string // the far endpoint's memory id, for the moved-memory route check
}

// endpointName identifies an edge in a finding when its far endpoint has no
// loc to name it by.
func (e graphEdge) endpointName() string {
	if e.Other != "" {
		return e.Other
	}
	if e.Label != "" {
		return "(unreadable target of " + strconv.Quote(e.Label) + ")"
	}
	return "(unreadable target of edge " + e.ID + ")"
}

// NewCmdCoding builds the `hadron coding` group.
func NewCmdCoding(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "coding <command>",
		Short: "Lint the coding-workflow graph (review checklist, preflight router)",
		Long: `Lint the coding-workflow graph in a Hadron memory.

The review:* checklist tree and the preflight router are executable
infrastructure, not prose: ` + "`tasks:review-changes`" + ` triages checks by
reading each one's "Applies when …" edge label back to the review parent,
and preflight routes symptom → finding along its outgoing edges. A
malformed edge label makes the check or route silently stop firing — the
node still exists and never matches again.

These commands detect that mechanically. Errors exit 5; --strict promotes
warnings to errors. Every subcommand takes -m/--memory.`,
	}
	cmd.AddCommand(newCmdReview(f))
	cmd.AddCommand(newCmdPreflight(f))
	return cmd
}

func newCmdReview(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review <command>",
		Short: "Work with the review:* checklist tree",
	}
	cmd.AddCommand(newCmdReviewList(f))
	cmd.AddCommand(newCmdReviewAdd(f))
	cmd.AddCommand(newCmdReviewLint(f))
	return cmd
}

func newCmdPreflight(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preflight <command>",
		Short: "Work with the preflight router",
	}
	cmd.AddCommand(newCmdPreflightList(f))
	cmd.AddCommand(newCmdPreflightLint(f))
	return cmd
}

// codingMemory holds the two spellings of the target memory this package needs:
// the canonical ref the server's memory(ref:) dispatch wants, and the raw
// <org>::<slug> pair that node URNs are composed from. They are NOT
// interchangeable — CanonicalMemoryRef emits the flat hrn:mem:<root>:<slug>
// form, so pasting a loc onto it yields a ref that resolves to nothing.
type codingMemory struct {
	Ref string // for filters and memory(ref:)
	raw string // as the user spelled it, for cmdutil.NodeURN
}

func codingMemoryURN(memory string) (codingMemory, error) {
	if strings.TrimSpace(memory) == "" {
		return codingMemory{}, exitcode.Newf(exitcode.Usage, "-m/--memory is required (org::memory)")
	}
	return codingMemory{Ref: cmdutil.CanonicalMemoryRef(memory), raw: strings.TrimSpace(memory)}, nil
}

// nodeRef composes the fully-qualified node URN for a bare loc in this memory.
func (m codingMemory) nodeRef(loc string) (string, error) {
	if u := cmdutil.NodeURN(m.raw, loc); u != "" {
		return u, nil
	}
	return "", exitcode.Newf(exitcode.Usage,
		"-m/--memory %q must be an <org>::<memory> pair to address nodes by bare loc", m.raw)
}

// fetchRootEdges reads a lint root and projects the edges on the requested
// side. incoming=true reads incomingEdges (whose far endpoint is `source`),
// incoming=false reads outgoingEdges (far endpoint `target`).
// It also returns the root's own memory id, which is the only value comparable
// against an endpoint's MemoryId — both come from the same projection, whereas
// the -m flag's canonical ref is a URN and would never match.
func fetchRootEdges(ctx context.Context, client graphql.Client, mem codingMemory, rootLoc string, incoming bool) ([]graphEdge, string, error) {
	ref, err := mem.nodeRef(rootLoc)
	if err != nil {
		return nil, "", err
	}
	resp, err := gen.GetNode(ctx, client, ref)
	if err != nil {
		return nil, "", api.MapError(err)
	}
	if resp.Node == nil {
		return nil, "", exitcode.Newf(exitcode.NotFound,
			"no %q node in %s — nothing to lint", rootLoc, mem.raw)
	}
	rootMemoryID := resp.Node.MemoryId
	var out []graphEdge
	if incoming {
		for _, e := range resp.Node.IncomingEdges {
			if e == nil {
				continue
			}
			ge := graphEdge{ID: e.Id, Loc: e.Loc}
			if e.Name != nil {
				ge.Label = *e.Name
			}
			if e.Source != nil {
				ge.OtherID, ge.Other, ge.MemoryID = e.Source.Id, e.Source.Loc, e.Source.MemoryId
			}
			out = append(out, ge)
		}
		return out, rootMemoryID, nil
	}
	for _, e := range resp.Node.OutgoingEdges {
		if e == nil {
			continue
		}
		ge := graphEdge{ID: e.Id, Loc: e.Loc}
		if e.Name != nil {
			ge.Label = *e.Name
		}
		if e.Target != nil {
			ge.OtherID, ge.Other, ge.MemoryID = e.Target.Id, e.Target.Loc, e.Target.MemoryId
		}
		out = append(out, ge)
	}
	return out, rootMemoryID, nil
}

// fetchNodes bulk-reads locs into the lint model. The second return is the refs
// the server would not hand back: a node can list but be unreadable, and
// CLAUDE.md requires those be surfaced rather than silently dropped.
// Nodes are addressed by **id**, never by a URN rebuilt from the root's memory:
// an edge may legitimately cross into another memory, and rebuilding the ref
// would then look up the wrong memory — reporting a live node as unresolvable,
// or silently linting a same-loc node from the home memory instead.
// wantContent retains node bodies. Only `review lint` reads them (to quote a
// check's scope paragraph, #331); `preflight lint` never does, so it opts out
// rather than holding every route target's body for the run.
func fetchNodes(ctx context.Context, client graphql.Client, byID map[string]string, wantContent bool) (map[string]checkNode, []string, error) {
	if len(byID) == 0 {
		return map[string]checkNode{}, nil, nil
	}
	// Keep ref → loc, so an unavailable ref maps back to the loc every other
	// row is keyed by.
	locByRef := make(map[string]string, len(byID))
	refs := make([]string, 0, len(byID))
	for id, loc := range byID {
		locByRef[id] = loc
		refs = append(refs, id)
	}
	sort.Strings(refs) // deterministic batching
	nodes, unavailable, err := api.CollectNodeBatch(refs, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
		resp, ferr := gen.NodeBatch(ctx, client, chunk, nil, nil)
		if ferr != nil {
			return nil, api.MapError(ferr)
		}
		return resp.NodeBatch, nil
	})
	if err != nil {
		return nil, nil, err
	}
	out := make(map[string]checkNode, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		cn := checkNode{Loc: n.Loc, Name: n.Name, Tags: n.Tags, Seq: n.Seq}
		if n.Description != nil {
			cn.Description = *n.Description
		}
		if wantContent && n.Content != nil {
			cn.Content = *n.Content
		}
		if n.IsRunnable != nil {
			cn.IsRunnable = *n.IsRunnable
		}
		out[n.Loc] = cn
	}
	// Report unavailable refs by loc, not by the fully-qualified ref, so the
	// finding lines up with every other row in the table.
	bare := make([]string, 0, len(unavailable))
	for _, u := range unavailable {
		if loc, ok := locByRef[u]; ok {
			bare = append(bare, loc)
			continue
		}
		bare = append(bare, u) // unrecognised spelling — report it verbatim
	}
	return out, bare, nil
}

// scanPrefix pages every node under a loc prefix to exhaustion. An unbounded
// query returns one page and silently drops the rest (#23), so the sweep must
// paginate.
//
// Scoped by prefix rather than listing the whole memory: membership is decided
// by the lint root's child prefix, so anything outside it can never be a
// checklist item and need not be fetched.
func scanPrefix(ctx context.Context, client graphql.Client, mem codingMemory, locPrefix string) ([]*api.ListNode, error) {
	const pageSize = 200
	var all []*api.ListNode
	for offset := 0; ; offset += pageSize {
		limit, off, pfx := pageSize, offset, locPrefix
		page, err := api.FindNodes(ctx, client, nil, nil,
			&gen.NodeFilter{MemoryIds: []string{mem.Ref}, LocPrefix: &pfx}, nil, nil, &limit, &off)
		if err != nil {
			return nil, api.MapError(err)
		}
		all = append(all, page.Nodes...)
		if len(page.Nodes) < pageSize {
			return all, nil
		}
	}
}

// hasTag reports whether tags contains want, case-insensitively.
func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(strings.TrimSpace(t), want) {
			return true
		}
	}
	return false
}
