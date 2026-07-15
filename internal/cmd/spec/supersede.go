package spec

import (
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
	edgeStatusFailed  = "failed"  // CreateEdge rejected it
	edgeStatusSkipped = "skipped" // target didn't resolve, so it was never attempted
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
}

func newCmdSupersede(f *cmdutil.Factory) *cobra.Command {
	var memory, title, feature, ruleAfter, reason string
	var copyBody, yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "supersede <old-citation>",
		Short: "Retire a spec and mint its replacement",
		Long: `Retire a numbered spec and create its replacement.

The old spec is never renumbered or deleted — it is tagged "superseded"
and linked to the replacement with a "superseded-by" edge. The new spec
gets the next free number (in the same feature by default; --feature
relocates it to another existing feature). Update the register ledger
afterward (the tool prints a reminder; it never edits the register).`,
		Example: `  hadron spec supersede msg:010:02 -m micromentor.org::platform-specs --title "W2 v2" --yes
  hadron spec supersede msg:010:02 -m micromentor.org::platform-specs --title "W2 v2" --copy-body --dry-run`,
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
			if hasTag(oldNode.Tags, supersededTag) {
				return exitcode.Newf(exitcode.Usage, "%q is already superseded", oldNode.Loc)
			}
			if successorLoc, ok := existingSupersededByTarget(oldNode); ok {
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

			// 1. Create the replacement.
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
			nodeType := "info"
			in := gen.CreateNodeInput{
				MemoryId: memURN, Loc: newTarget.Format(), Name: name,
				Tags: newTags, NodeType: &nodeType,
				Abstract: &abs, Content: &body, Data: specDataRaw(),
				Seq: specSeq(newTarget),
			}
			up, err := gen.CreateNode(cmd.Context(), client, &in)
			if err != nil {
				return api.MapError(err)
			}
			newID = up.CreateNode.Id

			// 2. New node's ToC + inheritance edges. Best-effort, but each outcome
			// is tracked and surfaced: a target that doesn't resolve was previously
			// skipped SILENTLY (the else branch was a no-op), and every planned edge
			// was then printed as if created (#128). Now the status is recorded and a
			// skip/failure is folded into the exit code (#127).
			var edgeFailures []string
			for i := range result.Edges {
				if i == supersededByIdx {
					continue // the retirement link is created in step 3
				}
				e := &result.Edges[i]
				tid, rerr := resolveSpecNode(cmd, client, memURN, e.Target)
				if rerr != nil {
					fmt.Fprintf(f.IOStreams.ErrOut, "warning: skipped edge %q → %s: %v\n", e.Label, e.Target, rerr)
					e.Status = edgeStatusSkipped
					edgeFailures = append(edgeFailures, e.Target)
					continue
				}
				if _, cerr := gen.CreateEdge(cmd.Context(), client, newID, tid, e.Label, nil, nil, nil, nil, nil, nil); cerr != nil {
					fmt.Fprintf(f.IOStreams.ErrOut, "warning: edge %q → %s failed: %v\n", e.Label, e.Target, api.MapError(cerr))
					e.Status = edgeStatusFailed
					edgeFailures = append(edgeFailures, e.Target)
					continue
				}
				e.Status = edgeStatusCreated
			}

			// 3. superseded-by edge old → new (its failure is fatal — the retirement
			// link is the whole point of the command).
			if _, cerr := gen.CreateEdge(cmd.Context(), client, oldNode.Id, newID, supersededByLabel, nil, nil, nil, nil, nil, nil); cerr != nil {
				result.Edges[supersededByIdx].Status = edgeStatusFailed
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return exitcode.Newf(exitcode.Error,
					"created replacement %s but failed to create the %q edge from %s: %v; add that edge manually or remove/review %s before rerunning",
					newTarget.Format(), supersededByLabel, oldCit.Format(), api.MapError(cerr), newTarget.Format())
			}
			result.Edges[supersededByIdx].Status = edgeStatusCreated

			// 4. Retire the old spec: tag superseded, same loc, append a note.
			if rerr := retireSupersededSpec(cmd, client, oldNode, newTarget.Format(), reason); rerr != nil {
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return exitcode.Newf(exitcode.Error,
					"linked %s to replacement %s but failed to tag/update the old spec as retired: %v; rerun this command to finish the retirement update",
					oldCit.Format(), newTarget.Format(), api.MapError(rerr))
			}

			fmt.Fprintf(f.IOStreams.ErrOut, "reminder: update the register — mark %s retired and add %s to the ledger.\n", oldCit.Format(), newTarget.Format())
			if err := output.Write(f.IOStreams, f.JSON, result, render); err != nil {
				return err
			}
			// The replacement exists but a structural edge is missing — it's
			// orphaned from the spec ToC. Exit non-zero so the gap isn't read as a
			// clean supersede (#127/#128), matching `spec new`'s edge-failure regime.
			if len(edgeFailures) > 0 {
				return exitcode.Newf(exitcode.Error,
					"superseded %s with %s but failed to wire %d structural edge(s) to %s — the replacement is orphaned from the spec tree; fix the target(s) and wire with `hadron edge add`",
					oldCit.Format(), newTarget.Format(), len(edgeFailures), strings.Join(edgeFailures, ", "))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to `hadron spec use`, then the active memory)")
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

func existingSupersededByTarget(n *gen.GetNodeNode) (string, bool) {
	for _, e := range n.OutgoingEdges {
		if e == nil || e.Target == nil || edgeNameStr(e.Name) != supersededByLabel {
			continue
		}
		return e.Target.Loc, true
	}
	return "", false
}

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
	_, err := gen.UpdateNode(cmd.Context(), client, &retireIn)
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
	verb := "✓ superseded"
	if r.DryRun {
		verb = "would supersede"
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
