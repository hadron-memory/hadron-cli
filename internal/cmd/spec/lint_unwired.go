package spec

import (
	"fmt"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// unwiredFindings is `spec lint --unwired` (#687): for each spec at a legacy-
// grammar citation, the edges legacy `spec new` (--product/--module/--feature)
// would have planned for it and that it does not have, each reported with the
// exact `spec link` that adds it.
//
// "Planned" is exactly what planTarget wires (Vera's ruling on #687 Q2): the
// table-of-contents edge to the citation's parent, and the inheritance edge to
// the contract it inherits, each only when that target exists. Nothing wider:
// a spec at any other loc owes no edge (#708), which is why this is opt-in and
// info-level rather than a default finding.
//
// A parent edge counts under any label, since spec new labels it with the
// child's title and a renamed spec keeps its edge; the inheritance edge counts
// only under inheritEdgeLabel, since any other edge to a contract is an
// ordinary cross-reference. exists says whether a target loc is a live node
// and whether it is a spec.
//
// spec new wires a target whatever node holds its loc, but spec link takes
// only specs, so a non-spec target's remedy is the edge create that runs. A
// non-spec source is skipped: lint reports it as missing its spec tag, and
// once tagged it is a spec this check covers.
func unwiredFindings(nodes []specNode, memURN string, exists func(loc string) (found, spec bool, err error)) ([]lintFindingDTO, error) {
	fs := []lintFindingDTO{}
	for _, n := range nodes {
		if n.Unavailable || !isSpec(n.Tags, n.Role) {
			continue
		}
		c, err := ParseCitation(n.Loc)
		if err != nil {
			continue
		}
		type planned struct{ kind, target, label string }
		var want []planned
		if p, ok := c.Parent(); ok {
			want = append(want, planned{"table-of-contents", p.Format(), ""})
		}
		if ic, ok := c.InheritedContractLoc(); ok {
			want = append(want, planned{"inheritance", ic.Format(), inheritEdgeLabel})
		}
		for _, w := range want {
			if hasOutEdge(n, w.target, w.label) {
				continue
			}
			ok, spec, err := exists(w.target)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			label := w.label
			if label == "" {
				if label = titleFromName(n.Name); label == "" {
					label = n.Loc
				}
			}
			fix := fmt.Sprintf("hadron spec link %s %s -m %s --label %s",
				cmdutil.ShellQuote(n.Loc), cmdutil.ShellQuote(w.target), cmdutil.ShellQuote(memURN), cmdutil.ShellQuote(label))
			if !spec {
				fix = fmt.Sprintf("hadron edge create -m %s --from %s --to %s --name %s",
					cmdutil.ShellQuote(memURN), cmdutil.ShellQuote(n.Loc), cmdutil.ShellQuote(w.target), cmdutil.ShellQuote(label))
			}
			fs = append(fs, lintFindingDTO{
				Citation: n.Loc, Rule: "unwired", Severity: sevInfo,
				Message: fmt.Sprintf("has no %s edge to %s, which `spec new` plans for it; add it with `%s`",
					w.kind, w.target, fix),
			})
		}
	}
	return fs, nil
}

// hasOutEdge reports an outgoing edge from n to target in n's own memory,
// under label when one is given. An edge to the same loc in another memory is
// a cross-reference to that corpus, not the planned local edge.
func hasOutEdge(n specNode, target, label string) bool {
	for _, e := range n.OutEdges {
		if e.Loc == target && e.TargetMemoryID == n.MemoryID && (label == "" || e.Name == label) {
			return true
		}
	}
	return false
}

// specLocExists answers unwiredFindings' exists: a loc in scope is known
// without a request (a spec unless its read says otherwise); any other is
// read once and remembered.
func specLocExists(cmd *cobra.Command, client graphql.Client, memURN string, inScope map[string]bool, nodes []specNode) func(string) (bool, bool, error) {
	type state struct{ found, spec bool }
	seen := map[string]state{}
	for _, n := range nodes {
		if !n.Unavailable {
			seen[n.Loc] = state{true, isSpec(n.Tags, n.Role)}
		}
	}
	return func(loc string) (bool, bool, error) {
		if v, ok := seen[loc]; ok {
			return v.found, v.spec, nil
		}
		if inScope[loc] {
			return true, true, nil
		}
		n, err := fetchSpecNode(cmd, client, memURN, loc)
		switch {
		case err == nil:
			seen[loc] = state{true, isSpec(n.Tags, n.Role)}
		case exitcode.FromError(err) == exitcode.NotFound:
			seen[loc] = state{}
		default:
			return false, false, err
		}
		return seen[loc].found, seen[loc].spec, nil
	}
}
