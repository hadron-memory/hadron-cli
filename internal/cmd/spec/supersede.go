package spec

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const supersededByLabel = "superseded-by"
const supersededTag = "superseded"

// Edge outcome, tracked per edge so the output reports what actually happened
// rather than echoing the plan as if every edge were created (#128).
const (
	edgeStatusPlanned = "planned" // dry-run / not yet executed
	edgeStatusCreated = "created"
	edgeStatusFailed  = "failed" // CreateEdge rejected it
	// The create errored AND the re-read failed: it may or may not exist. Same
	// word as a lost install answer elsewhere in the contract (#394).
	edgeStatusUnknown = "unknown"
)

// supersedeEdgeDTO is one structural edge with its execution outcome. It extends
// the plan-time plannedEdgeDTO shape with a status the real run fills in.
type supersedeEdgeDTO struct {
	Label  string `json:"label"`
	Target string `json:"target"`
	Status string `json:"status"`
}

type supersedeResultDTO struct {
	Old      string             `json:"old"`
	New      string             `json:"new"`
	MemoryID string             `json:"memoryId"`
	Name     string             `json:"name"`
	Tags     []string           `json:"tags"`
	Edges    []supersedeEdgeDTO `json:"edges"`
	DryRun   bool               `json:"dryRun"`
	// Retired is true only once the old spec has actually been tagged
	// superseded — the thing a partial run did NOT do, whatever it created.
	Retired bool `json:"retired"`
}

func newCmdSupersede(f *cmdutil.Factory) *cobra.Command {
	var memory, title, feature, ruleAfter, reason string
	var copyBody, yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "supersede <old-citation>",
		Short: "Retire a spec and mint its replacement",
		// Retiring a spec is the supersede flow, never a delete; point
		// the deletion verbs here rather than at the generic node
		// commands that do hard-delete a spec node.
		SuggestFor: []string{"delete", "rm", "remove", "retire", "deprecate"},
		Long: `Retire a numbered spec and create its replacement.

The old spec is never renumbered or deleted — it is tagged "superseded"
and linked to the replacement with a "superseded-by" edge. The new spec
gets the next free number (in the same feature by default; --feature
relocates it to another existing feature). Update the register ledger
afterward (the tool prints a reminder; it never edits the register).`,
		Example: `  hadron spec supersede msg:010:02 -m hrn:mem:micromentor.org:platform-specs --title "W2 v2" --yes
  hadron spec supersede msg:010:02 -m hrn:mem:micromentor.org:platform-specs --title "W2 v2" --copy-body --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" {
				return exitcode.Newf(exitcode.Usage, "--title is required")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}

			oldNode, oldCit, err := fetchSpecTaggedNode(cmd, client, memURN, args[0])
			if err != nil {
				return err
			}
			if oldCit.Level() < 3 {
				return exitcode.Newf(exitcode.Usage, "only a numbered rule/flow spec can be superseded, not %q", oldNode.Loc)
			}
			if successors := supersededByTargets(oldNode); len(successors) > 1 {
				// Two replacements claim this spec (a concurrent supersede). Finishing
				// would retire it in favour of whichever edge happens to be listed
				// first, so refuse, and leave the choice to a human.
				return exitcode.Newf(exitcode.Conflict,
					"%s is superseded by more than one replacement (%s), so it was not retired; keep one, remove the other %q edge(s), then rerun this command",
					oldCit.Format(), strings.Join(successors, ", "), supersededByLabel)
			}
			if hasTag(oldNode.Tags, supersededTag) {
				return exitcode.Newf(exitcode.Usage, "%q is already superseded", oldNode.Loc)
			}
			if sc, ok := existingSuccessor(oldNode); ok {
				if sc.id == "" {
					return exitcode.Newf(exitcode.NotFound,
						"%s has a %q edge to a replacement you cannot read, so it was not retired; ask someone who can read it to finish",
						oldCit.Format(), supersededByLabel)
				}
				if sc.otherMemory {
					// Supersede links within one corpus. A successor elsewhere was not
					// made by it, and its loc is not a citation in THIS corpus, so
					// finishing would retire the spec against the wrong node.
					return exitcode.Newf(exitcode.Conflict,
						"%s has a %q edge to %s, which is in another memory, so it was not retired; supersede only links within one corpus — review that edge",
						oldCit.Format(), supersededByLabel, sc.label)
				}
				successorLoc := sc.loc
				successorCit, err := ParseCitation(successorLoc)
				if err != nil {
					return err
				}
				result := supersedeResultDTO{
					Old: oldCit.Format(), New: successorLoc, MemoryID: memURN,
					Name: specName(successorCit, title), Tags: specTags(semanticTags(oldNode.Tags)), DryRun: dryRun,
					Edges: []supersedeEdgeDTO{{Label: supersededByLabel, Target: oldCit.Format() + " → " + successorLoc, Status: edgeStatusCreated}},
				}
				render := func(w io.Writer) error { return renderSupersede(w, result) }
				if dryRun {
					return output.Write(f.IOStreams, f.JSON, result, render)
				}
				if err := cmdutil.Confirm(f.IOStreams, yes,
					fmt.Sprintf("Finish retiring %s as superseded by %s?", oldCit.Format(), successorLoc)); err != nil {
					return err
				}
				if rerr := retireSupersededSpec(cmd, client, oldNode, successorLoc, reason); rerr != nil {
					_ = output.Write(f.IOStreams, f.JSON, result, render)
					return exitcode.Newf(exitcode.Error,
						"%s is already linked to %s but the old spec could not be tagged retired: %v; rerun this command to retry the retirement update",
						oldCit.Format(), successorLoc, api.MapError(rerr))
				}
				result.Retired = true
				fmt.Fprintf(f.IOStreams.ErrOut, "reminder: update the register — mark %s retired and add %s to the ledger.\n", oldCit.Format(), successorLoc)
				return output.Write(f.IOStreams, f.JSON, result, render)
			}

			// Scan the module subtree for allocation + parent checks. Paged to
			// exhaustion — a truncated scan here would make the replacement
			// allocator reuse a live number on a subtree past one page (#23).
			prefix := Citation{Product: oldCit.Product, Module: oldCit.Module}.Format()
			all, err := scanAllNodes(cmd.Context(), client, &memURN, &prefix, nil)
			if err != nil {
				return err
			}
			locs := map[string]bool{}
			var allLocs []string
			for _, n := range all {
				if n == nil {
					continue
				}
				if n.Loc != prefix && !strings.HasPrefix(n.Loc, prefix+":") {
					continue
				}
				if _, perr := ParseCitation(n.Loc); perr != nil {
					continue
				}
				locs[n.Loc] = true
				allLocs = append(allLocs, n.Loc)
			}

			newTarget, parentLoc, inheritLoc, err := planReplacement(oldCit, feature, ruleAfter, locs, allLocs)
			if err != nil {
				return err
			}

			newTags := specTags(semanticTags(oldNode.Tags))
			name := specName(newTarget, title)

			result := supersedeResultDTO{
				Old: oldCit.Format(), New: newTarget.Format(), MemoryID: memURN,
				Name: name, Tags: newTags, DryRun: dryRun,
			}
			if parentLoc != "" {
				result.Edges = append(result.Edges, supersedeEdgeDTO{Label: title, Target: parentLoc, Status: edgeStatusPlanned})
			}
			if inheritLoc != "" {
				result.Edges = append(result.Edges, supersedeEdgeDTO{Label: inheritEdgeLabel, Target: inheritLoc, Status: edgeStatusPlanned})
			}
			// The retirement link is identified by its POSITION, not its label: a
			// ToC edge carries the user's --title, which could itself be
			// "superseded-by" and collide with a label match (Codex #155).
			supersededByIdx := len(result.Edges)
			result.Edges = append(result.Edges, supersedeEdgeDTO{Label: supersededByLabel, Target: oldCit.Format() + " → " + newTarget.Format(), Status: edgeStatusPlanned})

			render := func(w io.Writer) error { return renderSupersede(w, result) }
			if dryRun {
				return output.Write(f.IOStreams, f.JSON, result, render)
			}

			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Supersede %s with %s? The old spec is retired (tagged %q).", oldCit.Format(), newTarget.Format(), supersededTag)); err != nil {
				return err
			}

			// 1. The replacement's body and abstract.
			var newID string
			body := rubricBody(newTarget, title)
			abs := placeholderAbstract(newTarget, title)
			if copyBody {
				if oldNode.Content != nil {
					body = *oldNode.Content
				}
				if abstractPresent(oldNode.Abstract) {
					abs = *oldNode.Abstract
				}
			}
			// 2. The replacement's ToC + inheritance edges travel INLINE on its
			// createSpecNode (#687), resolved before anything is written: the
			// replacement is created with them or not at all, so it can no longer
			// land orphaned from the spec tree (#127/#128's partial state).
			structural := make([]plannedEdgeDTO, 0, len(result.Edges))
			for i, e := range result.Edges {
				if i != supersededByIdx { // the retirement link is step 3's
					structural = append(structural, plannedEdgeDTO{Label: e.Label, Target: e.Target})
				}
			}
			edges, err := resolveSpecEdges(cmd, client, memURN, newTarget.Format(), structural, nil)
			if err != nil {
				return err
			}
			nodeType := "info"
			in := gen.CreateNodeInput{
				MemoryId: memURN, Loc: newTarget.Format(), Name: name,
				Tags: newTags, NodeType: &nodeType,
				Abstract: &abs, Content: &body, Data: specDataRaw(),
				Seq: specSeq(newTarget), Role: specRole(),
				Edges: edges,
			}
			up, err := api.CreateSpecNode(cmd.Context(), client, &in)
			if err != nil {
				return api.MapError(err)
			}
			newID = up.Id
			for i := range result.Edges {
				if i != supersededByIdx {
					result.Edges[i].Status = edgeStatusCreated
				}
			}

			// 3. superseded-by edge old → new (its failure is fatal — the retirement
			// link is the whole point of the command). It leaves an EXISTING node,
			// so it cannot travel inline. Once it exists, rerunning supersede takes
			// the finish-the-retirement path above.
			edgeResp, cerr := gen.CreateEdge(cmd.Context(), client, oldNode.Id, newID, supersededByLabel, nil, nil, nil, nil, nil, nil)
			if cerr == nil && (edgeResp == nil || edgeResp.CreateEdge == nil) {
				// No error but no edge either: not proof of anything, so let the
				// re-read below decide, exactly as for an error (#691 review).
				cerr = errors.New("createEdge returned no edge")
			}

			// Then LOOK, after every write and not only a failed one (#691 review):
			//   - a failed create may have committed — a lost response looks like a
			//     refusal — so prescribing `spec link` over it would fail, and a
			//     blind rerun over one that didn't land mints a second replacement;
			//   - a SUCCESSFUL create may still race a concurrent supersede that
			//     picked a different replacement: edge identity includes the target,
			//     so both writes succeed. Retiring only when this run's replacement
			//     is the SOLE successor keeps two runs from both retiring.
			// This narrows the race; closing it needs a server-side conditional
			// retirement, which is the server's to add.
			landed, other, lerr := supersededByState(cmd, client, oldNode.Id, newID)
			switch {
			case lerr == nil && other != "":
				// A create that SUCCEEDED is this run's edge even when a stale
				// re-read doesn't show it yet, so never report it as unwritten.
				wrote := landed || cerr == nil
				result.Edges[supersededByIdx].Status = edgeStatusFailed
				if wrote {
					result.Edges[supersededByIdx].Status = edgeStatusCreated
				}
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				if wrote {
					return exitcode.Newf(exitcode.Conflict,
						"%s is now superseded by both %s and %s (another supersede ran at the same time), so it was not retired; keep one replacement, remove the other's %q edge, then rerun this command to finish",
						oldCit.Format(), newTarget.Format(), other, supersededByLabel)
				}
				return exitcode.Newf(exitcode.Conflict,
					"created replacement %s, but %s is already superseded by %s (another supersede got there first), so no second %q edge was written and %s was not retired; %s is unlinked — review both replacements before changing anything",
					newTarget.Format(), oldCit.Format(), other, supersededByLabel, oldCit.Format(), newTarget.Format())
			case cerr == nil && lerr != nil:
				// The link was written, but whether it is the ONLY successor can't
				// be checked, so don't retire on an unverified premise (#691
				// review). A rerun re-reads first, and finishes only when there is
				// exactly one successor.
				result.Edges[supersededByIdx].Status = edgeStatusCreated
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return exitcode.Newf(exitcode.Error,
					"linked %s to replacement %s but could not re-read %s to confirm it is the only successor (%v), so it was not retired; once `hadron spec get %s -m %s` shows its %s edge to %s, rerun this command to finish (rerunning before then can mint a second replacement)",
					oldCit.Format(), newTarget.Format(), oldCit.Format(), api.MapError(lerr),
					oldCit.Format(), memURN, supersededByLabel, newTarget.Format())
			case cerr == nil && !landed:
				// Written, but the re-read doesn't show it (a stale read?). Retire
				// only on a VERIFIED link, so stop; a rerun re-reads first.
				result.Edges[supersededByIdx].Status = edgeStatusCreated
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return exitcode.Newf(exitcode.Error,
					"linked %s to replacement %s, but re-reading %s did not show that link yet, so it was not retired; once `hadron spec get %s -m %s` shows its %s edge to %s, rerun this command to finish (rerunning before then can mint a second replacement)",
					oldCit.Format(), newTarget.Format(), oldCit.Format(),
					oldCit.Format(), memURN, supersededByLabel, newTarget.Format())
			case cerr == nil:
				// Written, seen, and the sole successor: retire below.
			case lerr == nil && landed:
				// The create errored but committed; carry on and finish.
			case lerr == nil:
				result.Edges[supersededByIdx].Status = edgeStatusFailed
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return exitcode.Newf(exitcode.Error,
					"created replacement %s but failed to create the %q edge from %s (confirmed absent): %v; link them with `hadron spec link %s %s -m %s --label %s`, then rerun this command to finish retiring %s",
					newTarget.Format(), supersededByLabel, oldCit.Format(), api.MapError(cerr),
					oldCit.Format(), newTarget.Format(), memURN, supersededByLabel, oldCit.Format())
			default:
				result.Edges[supersededByIdx].Status = edgeStatusUnknown
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return exitcode.Newf(exitcode.Error,
					"created replacement %s but the %q edge from %s may or may not exist (%v, and re-reading %s failed: %v); check with `hadron spec get %s -m %s` — if it has no %s edge to %s, link them with `hadron spec link %s %s -m %s --label %s` — then rerun this command to finish retiring %s",
					newTarget.Format(), supersededByLabel, oldCit.Format(), api.MapError(cerr), oldCit.Format(), api.MapError(lerr),
					oldCit.Format(), memURN, supersededByLabel, newTarget.Format(),
					oldCit.Format(), newTarget.Format(), memURN, supersededByLabel, oldCit.Format())
			}
			result.Edges[supersededByIdx].Status = edgeStatusCreated

			// 4. Retire the old spec: tag superseded, same loc, append a note.
			if rerr := retireSupersededSpec(cmd, client, oldNode, newTarget.Format(), reason); rerr != nil {
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return exitcode.Newf(exitcode.Error,
					"linked %s to replacement %s but failed to tag/update the old spec as retired: %v; rerun this command to finish the retirement update",
					oldCit.Format(), newTarget.Format(), api.MapError(rerr))
			}

			result.Retired = true
			fmt.Fprintf(f.IOStreams.ErrOut, "reminder: update the register — mark %s retired and add %s to the ledger.\n", oldCit.Format(), newTarget.Format())
			return output.Write(f.IOStreams, f.JSON, result, render)
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to the memory set by hadron spec use, then the active memory)")
	cmd.Flags().StringVar(&title, "title", "", "human title for the replacement spec (required)")
	cmd.Flags().StringVar(&feature, "feature", "", "relocate the replacement under this existing feature (3 digits)")
	cmd.Flags().StringVar(&ruleAfter, "rule-after", "", "allocate the replacement rule strictly after this number")
	cmd.Flags().StringVar(&reason, "reason", "", "note appended to the retired spec")
	cmd.Flags().BoolVar(&copyBody, "copy-body", false, "seed the replacement's body/abstract from the old spec")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan without writing anything")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

// planReplacement computes the replacement citation (same level as old)
// plus its ToC parent and inheritance target.
func planReplacement(old Citation, feature, ruleAfter string, locs map[string]bool, allLocs []string) (newTarget Citation, parentLoc, inheritLoc string, err error) {
	switch old.Level() {
	case 4: // flow → next flow under the same rule
		parent := Citation{Product: old.Product, Module: old.Module, Feature: old.Feature, Rule: old.Rule}
		t, aerr := allocateChild(parent, childNumbersAt(parent, allLocs), nil, 0)
		if aerr != nil {
			return Citation{}, "", "", aerr
		}
		return t, parent.Format(), "", nil
	default: // rule → next rule under the (possibly relocated) feature
		feat := old.Feature
		if feature != "" {
			feat = feature
		}
		parent := Citation{Product: old.Product, Module: old.Module, Feature: feat}
		if _, perr := ParseCitation(parent.Format()); perr != nil {
			return Citation{}, "", "", perr
		}
		if !locs[parent.Format()] {
			return Citation{}, "", "", exitcode.Newf(exitcode.NotFound, "feature %q does not exist", parent.Format())
		}
		after := 0
		if ruleAfter != "" {
			n, cerr := strconv.Atoi(ruleAfter)
			if cerr != nil {
				return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--rule-after must be a number")
			}
			after = n
		}
		t, aerr := allocateChild(parent, childNumbersAt(parent, allLocs), nil, after)
		if aerr != nil {
			return Citation{}, "", "", aerr
		}
		inherit := ""
		if cl, ok := t.InheritedContractLoc(); ok && locs[cl.Format()] {
			inherit = cl.Format()
		}
		return t, parent.Format(), inherit, nil
	}
}

// supersededByState re-reads the old spec after the superseded-by write and
// reports whether its edge to THIS run's replacement (by node id) exists
// (landed), and the label of any superseded-by successor that is a different
// node (other, "" when none).
func supersededByState(cmd *cobra.Command, client graphql.Client, oldID, successorID string) (landed bool, other string, err error) {
	resp, err := gen.GetNode(cmd.Context(), client, oldID)
	if err != nil {
		return false, "", err
	}
	if resp.Node == nil {
		return false, "", exitcode.Newf(exitcode.NotFound, "node %s not found", oldID)
	}
	for _, sc := range supersededBySuccessors(resp.Node) {
		if sc.id != "" && sc.id == successorID {
			landed = true
		} else {
			other = sc.label
		}
	}
	return landed, other, nil
}

// existingSuccessor is the spec's (first) superseded-by successor, if any.
func existingSuccessor(n *gen.GetNodeNode) (successor, bool) {
	if t := supersededBySuccessors(n); len(t) > 0 {
		return t[0], true
	}
	return successor{}, false
}

// supersededByTargets lists the labels of a spec's distinct successors.
func supersededByTargets(n *gen.GetNodeNode) []string {
	var out []string
	for _, sc := range supersededBySuccessors(n) {
		out = append(out, sc.label)
	}
	return out
}

// successor is one node a superseded-by edge points at. Identity is the node
// ID, never the loc: a loc is unique only within a memory, and edges may cross
// memories, so two successors can share a citation (#691 review).
type successor struct {
	id          string // "" when the caller cannot read the target
	loc         string // the target's loc, "" when unreadable
	otherMemory bool   // the target lives in a different memory from the spec
	label       string // for messages: the loc, qualified when in another memory
}

// supersededBySuccessors lists the distinct successors of n's superseded-by
// edges, in edge order. A target the caller cannot read comes back null; it is
// still a successor, listed as unreadableSuccessor, never skipped — and each
// such edge counts on its own, since collapsing them would hide exactly the
// ambiguity this list exists for.
func supersededBySuccessors(n *gen.GetNodeNode) []successor {
	var out []successor
	seen := map[string]bool{}
	for _, e := range n.OutgoingEdges {
		if e == nil || edgeNameStr(e.Name) != supersededByLabel {
			continue
		}
		if e.Target == nil {
			out = append(out, successor{label: unreadableSuccessor})
			continue
		}
		if seen[e.Target.Id] {
			continue
		}
		seen[e.Target.Id] = true
		sc := successor{id: e.Target.Id, loc: e.Target.Loc, label: e.Target.Loc}
		if e.Target.MemoryId != n.MemoryId {
			sc.otherMemory = true
			sc.label += " (in memory " + e.Target.MemoryId + ")"
		}
		out = append(out, sc)
	}
	return out
}

// unreadableSuccessor stands in for a superseded-by target the caller cannot
// read (the edge's target resolves null).
const unreadableSuccessor = "(a successor you cannot read)"

func retireSupersededSpec(cmd *cobra.Command, client graphql.Client, oldNode *gen.GetNodeNode, successorLoc, reason string) error {
	note := fmt.Sprintf("\n\n> Superseded by %s.", successorLoc)
	if reason != "" {
		note = fmt.Sprintf("\n\n> Superseded by %s: %s", successorLoc, reason)
	}
	oldContent := ""
	if oldNode.Content != nil {
		oldContent = *oldNode.Content
	}
	retired := oldContent
	if !strings.Contains(retired, "> Superseded by "+successorLoc) {
		retired += note
	}
	retireTags := append([]string{}, oldNode.Tags...)
	if !hasTag(retireTags, supersededTag) {
		retireTags = append(retireTags, supersededTag)
	}
	retireIn := gen.UpdateNodeInput{
		MemoryId: &oldNode.MemoryId, Loc: &oldNode.Loc,
		Tags: retireTags, Content: &retired,
	}
	_, err := api.UpdateSpecNode(cmd.Context(), client, &retireIn)
	return err
}

// semanticTags strips the structural tags (spec / p-level / superseded),
// leaving the topical tags to carry over to the replacement.
func semanticTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if t == "spec" || t == supersededTag || rePLevel.MatchString(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func renderSupersede(w io.Writer, r supersedeResultDTO) error {
	// "✓ superseded" only when it happened: a partial run prints its result
	// right before an error saying the old spec was NOT retired (#691 review).
	verb := "✗ not retired:"
	switch {
	case r.DryRun:
		verb = "would supersede"
	case r.Retired:
		verb = "✓ superseded"
	}
	fmt.Fprintf(w, "%s %s → %s — %s\n", verb, r.Old, r.New, r.Name)
	fmt.Fprintf(w, "  tags: %v\n", r.Tags)
	for _, e := range r.Edges {
		// Dry-run / not-yet-executed edges carry no meaningful status, so keep the
		// plain form; a real run annotates each with created/failed/skipped.
		if e.Status == "" || e.Status == edgeStatusPlanned {
			fmt.Fprintf(w, "  edge: %s → %s\n", e.Label, e.Target)
		} else {
			fmt.Fprintf(w, "  edge [%s]: %s → %s\n", e.Status, e.Label, e.Target)
		}
	}
	return nil
}
