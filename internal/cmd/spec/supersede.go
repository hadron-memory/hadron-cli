package spec

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const supersededByLabel = "superseded-by"
const supersededTag = "superseded"

type supersedeResultDTO struct {
	Old      string           `json:"old"`
	New      string           `json:"new"`
	MemoryID string           `json:"memoryId"`
	Name     string           `json:"name"`
	Tags     []string         `json:"tags"`
	Edges    []plannedEdgeDTO `json:"edges"`
	DryRun   bool             `json:"dryRun"`
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
			memURN, err := memoryURNFromFlag(memory)
			if err != nil {
				return err
			}
			if title == "" {
				return exitcode.Newf(exitcode.Usage, "--title is required")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			oldNode, err := fetchSpecNode(cmd, client, memURN, args[0])
			if err != nil {
				return err
			}
			oldCit, err := ParseCitation(oldNode.Loc)
			if err != nil {
				return err
			}
			if oldCit.Level() < 3 {
				return exitcode.Newf(exitcode.Usage, "only a numbered rule/flow spec can be superseded, not %q", oldNode.Loc)
			}
			if hasTag(oldNode.Tags, supersededTag) {
				return exitcode.Newf(exitcode.Usage, "%q is already superseded", oldNode.Loc)
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

			plevel := defaultPLevel(newTarget)
			newTags := specTags(plevel, semanticTags(oldNode.Tags))
			name := specName(newTarget, title)

			result := supersedeResultDTO{
				Old: oldCit.Format(), New: newTarget.Format(), MemoryID: memURN,
				Name: name, Tags: newTags, DryRun: dryRun,
			}
			if parentLoc != "" {
				result.Edges = append(result.Edges, plannedEdgeDTO{Label: tocEdgeLabel(plevel, title), Target: parentLoc})
			}
			if inheritLoc != "" {
				result.Edges = append(result.Edges, plannedEdgeDTO{Label: inheritEdgeLabel, Target: inheritLoc})
			}
			result.Edges = append(result.Edges, plannedEdgeDTO{Label: supersededByLabel, Target: oldCit.Format() + " → " + newTarget.Format()})

			render := func(w io.Writer) error { return renderSupersede(w, result) }
			if dryRun {
				return output.Write(f.IOStreams, f.JSON, result, render)
			}

			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Supersede %s with %s? The old spec is retired (tagged %q).", oldCit.Format(), newTarget.Format(), supersededTag)); err != nil {
				return err
			}

			// 1. Create the replacement.
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
			createOnly := true
			nodeType := "info"
			in := gen.NodeInput{
				MemoryId: memURN, Loc: newTarget.Format(), Name: name,
				CreateOnly: &createOnly, Tags: newTags, NodeType: &nodeType,
				Abstract: &abs, Content: &body, Data: specDataRaw(),
			}
			up, err := gen.UpsertNode(cmd.Context(), client, &in)
			if err != nil {
				return api.MapError(err)
			}
			newID := up.UpsertNode.Id

			// 2. New node's ToC + inheritance edges.
			for _, e := range result.Edges {
				if e.Label == supersededByLabel {
					continue
				}
				if tid, rerr := resolveSpecNode(cmd, client, memURN, e.Target); rerr == nil {
					if _, cerr := gen.CreateEdge(cmd.Context(), client, newID, tid, e.Label, nil, nil, nil); cerr != nil {
						fmt.Fprintf(f.IOStreams.ErrOut, "warning: edge %q failed: %v\n", e.Label, api.MapError(cerr))
					}
				}
			}

			// 3. superseded-by edge old → new.
			if _, cerr := gen.CreateEdge(cmd.Context(), client, oldNode.Id, newID, supersededByLabel, nil, nil, nil); cerr != nil {
				return api.MapError(cerr)
			}

			// 4. Retire the old spec: tag superseded, same loc, append a note.
			note := fmt.Sprintf("\n\n> Superseded by %s.", newTarget.Format())
			if reason != "" {
				note = fmt.Sprintf("\n\n> Superseded by %s: %s", newTarget.Format(), reason)
			}
			oldContent := ""
			if oldNode.Content != nil {
				oldContent = *oldNode.Content
			}
			retired := oldContent + note
			retireTags := append(append([]string{}, oldNode.Tags...), supersededTag)
			retireIn := gen.NodeInput{
				MemoryId: oldNode.MemoryId, Loc: oldNode.Loc, Name: oldNode.Name,
				Tags: retireTags, Content: &retired,
			}
			if _, rerr := gen.UpsertNode(cmd.Context(), client, &retireIn); rerr != nil {
				return api.MapError(rerr)
			}

			fmt.Fprintf(f.IOStreams.ErrOut, "reminder: update the register — mark %s retired and add %s to the ledger.\n", oldCit.Format(), newTarget.Format())
			return output.Write(f.IOStreams, f.JSON, result, render)
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVar(&title, "title", "", "human title for the replacement spec (required)")
	cmd.Flags().StringVar(&feature, "feature", "", "relocate the replacement under this existing feature (3 digits)")
	cmd.Flags().StringVar(&ruleAfter, "rule-after", "", "allocate the replacement rule strictly after this number")
	cmd.Flags().StringVar(&reason, "reason", "", "note appended to the retired spec")
	cmd.Flags().BoolVar(&copyBody, "copy-body", false, "seed the replacement's body/abstract from the old spec")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan without writing anything")
	_ = cmd.MarkFlagRequired("memory")
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
		fmt.Fprintf(w, "  edge: %s → %s\n", e.Label, e.Target)
	}
	return nil
}
