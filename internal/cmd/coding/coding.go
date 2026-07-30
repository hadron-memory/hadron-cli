// Package coding lints the coding-workflow graph: the review:* checklist tree
// and the preflight router. Both are executable infrastructure — a malformed
// edge label makes a check or a route silently stop firing — so the defects are
// mechanically detectable and worth a linter. See
// docs/plans/coding-command-group.md.
package coding

import (
	"context"
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
	Tags        []string
	Seq         *int
	IsRunnable  bool
}

// graphEdge is one edge incident to a lint root. Other is the far endpoint's
// loc — the edge's source for the review tree's incoming edges, its target for
// preflight's outgoing routes (see docs/plans/coding-command-group.md,
// Decision 3: the two subcommands read opposite ends).
type graphEdge struct {
	ID       string
	Label    string
	Loc      string // the edge's own (usually name-derived) loc
	Other    string
	MemoryID string // the far endpoint's memory, for the moved-memory route check
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
	cmd.AddCommand(newCmdReviewLint(f))
	return cmd
}

func newCmdPreflight(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preflight <command>",
		Short: "Work with the preflight router",
	}
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
func fetchRootEdges(ctx context.Context, client graphql.Client, mem codingMemory, rootLoc string, incoming bool) ([]graphEdge, error) {
	ref, err := mem.nodeRef(rootLoc)
	if err != nil {
		return nil, err
	}
	resp, err := gen.GetNode(ctx, client, ref)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.Node == nil {
		return nil, exitcode.Newf(exitcode.NotFound,
			"no %q node in %s — nothing to lint", rootLoc, mem.raw)
	}
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
				ge.Other, ge.MemoryID = e.Source.Loc, e.Source.MemoryId
			}
			out = append(out, ge)
		}
		return out, nil
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
			ge.Other, ge.MemoryID = e.Target.Loc, e.Target.MemoryId
		}
		out = append(out, ge)
	}
	return out, nil
}

// fetchNodes bulk-reads locs into the lint model. The second return is the refs
// the server would not hand back: a node can list but be unreadable, and
// CLAUDE.md requires those be surfaced rather than silently dropped.
func fetchNodes(ctx context.Context, client graphql.Client, mem codingMemory, locs []string) (map[string]checkNode, []string, error) {
	if len(locs) == 0 {
		return map[string]checkNode{}, nil, nil
	}
	// Keep ref → loc, so an unavailable ref maps back to the bare loc every
	// other row is keyed by. Deriving it by trimming a prefix would depend on
	// how the URN happens to be spelled.
	locByRef := make(map[string]string, len(locs))
	refs := make([]string, 0, len(locs))
	for _, l := range locs {
		ref, err := mem.nodeRef(l)
		if err != nil {
			return nil, nil, err
		}
		locByRef[ref] = l
		refs = append(refs, ref)
	}
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

// scanTagged pages a memory's nodes carrying every one of tags to exhaustion.
// An unbounded query returns one page and silently drops the rest (#23), so a
// "whole memory" sweep must paginate.
func scanTagged(ctx context.Context, client graphql.Client, mem codingMemory, tags []string) ([]*api.ListNode, error) {
	const pageSize = 200
	var all []*api.ListNode
	for offset := 0; ; offset += pageSize {
		limit, off := pageSize, offset
		page, err := api.FindNodes(ctx, client, nil, nil,
			&gen.NodeFilter{MemoryIds: []string{mem.Ref}, Tags: tags}, nil, nil, &limit, &off)
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
