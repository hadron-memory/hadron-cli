package coding

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// plannedFix is one edge relabel: promote a check's description into an edge
// label that carries no condition.
type plannedFix struct {
	Loc      string
	EdgeID   string
	OldLabel string
	NewLabel string
}

// planReviewFix selects the mechanical subset of findings that can be repaired
// without a human: an empty or non-condition label on a check whose description
// already states the trigger. Anything else — a broken label on a node whose
// description has no condition either — needs a person and is left alone.
func planReviewFix(in reviewInput, findings []findingDTO) []plannedFix {
	broken := map[string]bool{}
	for _, f := range findings {
		if f.Rule == "label-present" || f.Rule == "label-is-condition" {
			broken[f.Node] = true
		}
	}
	var out []plannedFix
	for loc := range broken {
		e, ok := in.Edges[loc]
		if !ok {
			continue
		}
		trigger := triggerFromDescription(in.Members[loc].Description)
		if trigger == "" {
			continue
		}
		out = append(out, plannedFix{Loc: loc, EdgeID: e.ID, OldLabel: e.Label, NewLabel: trigger})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Loc < out[j].Loc })
	return out
}

// triggerFromDescription extracts an "Applies when …" sentence from a
// description, or "" when it carries no condition. Deliberately conservative:
// it only promotes text that is already spelled as a trigger, so --fix never
// invents a condition.
func triggerFromDescription(desc string) string {
	d := strings.TrimSpace(desc)
	if d == "" {
		return ""
	}
	lower := strings.ToLower(d)
	i := strings.Index(lower, triggerStem)
	if i < 0 {
		return ""
	}
	rest := d[i:]
	// Stop at the first sentence end so a long description contributes only its
	// trigger clause.
	if j := strings.IndexAny(rest, ".\n"); j > 0 {
		rest = rest[:j]
	}
	rest = strings.TrimSpace(rest)
	if len(strings.TrimSpace(rest[len(triggerStem):])) == 0 {
		return "" // the stem with nothing after it is not a condition either
	}
	return rest
}

// applyReviewFix writes the planned relabels and returns how many landed.
//
// Each write is a single-edge updateEdge. It must never go through
// updateNode(edges:), which REPLACES a node's whole outgoing edge set and would
// destroy the check's sibling documented-by / relates-to edges — the hazard
// that makes the MCP surface unsafe for this repair (issue #325).
func applyReviewFix(ctx context.Context, client graphql.Client, f *cmdutil.Factory, in reviewInput, findings []findingDTO, yes bool) (int, error) {
	plan := planReviewFix(in, findings)
	if len(plan) == 0 {
		fmt.Fprintln(f.IOStreams.ErrOut, "--fix: nothing mechanically fixable (a broken label needs a description that already states its trigger)")
		return 0, nil
	}
	for _, p := range plan {
		fmt.Fprintf(f.IOStreams.ErrOut, "  %s: %q → %q\n", p.Loc, p.OldLabel, p.NewLabel)
	}
	if err := cmdutil.Confirm(f.IOStreams, yes,
		fmt.Sprintf("Relabel %d edge(s)?", len(plan))); err != nil {
		return 0, err
	}
	applied := 0
	for _, p := range plan {
		label := p.NewLabel
		if _, err := gen.UpdateEdge(ctx, client, p.EdgeID, &label, nil, nil, nil, nil, nil, nil); err != nil {
			return applied, fmt.Errorf("relabelling %s: %w", p.Loc, api.MapError(err))
		}
		applied++
	}
	return applied, nil
}
