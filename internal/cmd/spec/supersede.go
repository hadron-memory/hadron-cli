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
	// superseded — the thing a partial run did NOT do, whatever it created —
	// false when it verifiably was not, and null when that cannot be known
	// (the retirement update got no answer and the re-read failed too).
	Retired *bool `json:"retired"`
}

func newCmdSupersede(f *cmdutil.Factory) *cobra.Command {
	var memory, title, feature, ruleAfter, reason, to string
	var copyBody, yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "supersede <old-citation>",
		Short: "Retire a spec and mint its replacement",
		// Retiring a spec is the supersede flow, never a delete; point
		// the deletion verbs here rather than at the generic node
		// commands that do hard-delete a spec node.
		SuggestFor: []string{"delete", "rm", "remove", "retire", "deprecate"},
		Long: `Retire a spec and create its replacement.

The old spec is never renumbered or deleted — it is tagged "superseded"
and linked to the replacement with a "superseded-by" edge.

Any spec can be superseded. Name the replacement's loc with --to <loc>
(any valid loc; it must be free). Without --to, a legacy rule or flow
citation gets the next free number in the legacy numbering (in the same
feature by default; --feature relocates it to another existing feature),
with that numbering's table-of-contents and inheritance edges; a spec
outside the legacy numbering needs --to. Update the register ledger
afterward (the tool prints a reminder; it never edits the register).`,
		Example: `  hadron spec supersede msg:010:02 -m hrn:mem:micromentor.org:platform-specs --title "W2 v2" --yes
  hadron spec supersede onboarding:mentor:screens -m hrn:mem:micromentor.org:specs --to onboarding:mentor:screens-v2 --title "Screens v2" --yes
  hadron spec supersede msg:010:02 -m hrn:mem:micromentor.org:platform-specs --title "W2 v2" --copy-body --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" {
				return exitcode.Newf(exitcode.Usage, "--title is required")
			}
			// The old spec's loc, too, is validated before any request (@copilot
			// on #710).
			if _, verr := validateSpecLoc(args[0]); verr != nil {
				return verr
			}
			// --to is validated up front, before any request and whichever path
			// runs below: a resumed run must not accept a --to it would then
			// ignore (@codex on #710).
			var toLoc string
			if to != "" {
				if feature != "" || ruleAfter != "" {
					return exitcode.Newf(exitcode.Usage, "--to names the replacement's loc — don't combine it with --feature/--rule-after, which allocate a legacy number instead")
				}
				var terr error
				if toLoc, terr = validateSpecLoc(to); terr != nil {
					return terr
				}
				if old, oerr := validateSpecLoc(args[0]); oerr == nil && old == toLoc {
					return exitcode.Newf(exitcode.Usage, "a spec cannot supersede itself (%s)", toLoc)
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}

			oldNode, err := fetchSpecTaggedNode(cmd, client, memURN, args[0])
			if err != nil {
				return err
			}
			// Any spec can be superseded (#708). Only ALLOCATING its successor
			// needs the legacy numbering (below); a successor named with --to
			// needs nothing from the old spec's shape.
			oldLoc := oldNode.Loc
			if successors := supersededByTargets(oldNode); len(successors) > 1 {
				// Two replacements claim this spec (a concurrent supersede). Finishing
				// would retire it in favour of whichever edge happens to be listed
				// first, so refuse, and leave the choice to a human.
				return exitcode.Newf(exitcode.Conflict,
					"%s is superseded by more than one replacement (%s), so it was not retired; keep one, remove the other %q edge(s), then rerun this command",
					oldLoc, strings.Join(successors, ", "), supersededByLabel)
			}
			if hasTag(oldNode.Tags, supersededTag) {
				return exitcode.Newf(exitcode.Usage, "%q is already superseded", oldNode.Loc)
			}
			if sc, ok := existingSuccessor(oldNode); ok {
				if sc.id == "" {
					return exitcode.Newf(exitcode.NotFound,
						"%s has a %q edge to a replacement you cannot read, so it was not retired; ask someone who can read it to finish",
						oldLoc, supersededByLabel)
				}
				if sc.otherMemory {
					// Supersede links within one corpus. A successor elsewhere was not
					// made by it, and its loc is not a citation in THIS corpus, so
					// finishing would retire the spec against the wrong node.
					return exitcode.Newf(exitcode.Conflict,
						"%s has a %q edge to %s, which is in another memory, so it was not retired; supersede only links within one corpus — review that edge",
						oldLoc, supersededByLabel, sc.label)
				}
				successorLoc := sc.loc
				// A resumed run finishes against the successor the earlier run
				// created. A --to naming a DIFFERENT one is refused, never
				// silently ignored (@codex on #710).
				if toLoc != "" && toLoc != successorLoc {
					return exitcode.Newf(exitcode.Conflict,
						"%s already has a replacement from an earlier run, %s, but --to names %s, so nothing was retired; rerun with --to %s (or without --to) to finish retiring it against %s, or review that %q edge if it is not the replacement you want",
						oldLoc, successorLoc, toLoc, successorLoc, successorLoc, supersededByLabel)
				}
				result := supersedeResultDTO{
					Old: oldLoc, New: successorLoc, MemoryID: memURN,
					Name: specNameAt(successorLoc, title), Tags: specTags(semanticTags(oldNode.Tags)), DryRun: dryRun, Retired: boolRef(false),
					Edges: []supersedeEdgeDTO{{Label: supersededByLabel, Target: oldLoc + " → " + successorLoc, Status: edgeStatusCreated}},
				}
				render := func(w io.Writer) error { return renderSupersede(w, result) }
				if dryRun {
					return output.Write(f.IOStreams, f.JSON, result, render)
				}
				if err := cmdutil.Confirm(f.IOStreams, yes,
					fmt.Sprintf("Finish retiring %s as superseded by %s?", oldLoc, successorLoc)); err != nil {
					return err
				}
				// Re-validate IMMEDIATELY before retiring: the first read can be
				// arbitrarily old by now (the prompt above waits on a human), and a
				// concurrent supersede may have added a second successor since
				// (#691 review). This narrows the window; closing it needs a server
				// conditional (team chat #1333).
				fresh, ferr := gen.GetNode(cmd.Context(), client, oldNode.Id)
				if ferr != nil || fresh.Node == nil {
					if ferr == nil {
						ferr = exitcode.Newf(exitcode.NotFound, "%s vanished", oldLoc)
					}
					_ = output.Write(f.IOStreams, f.JSON, result, render)
					// Nothing was written in this invocation, so keep the mapped
					// code (7 for no answer, 4 for not found): scripts retry or
					// stop on it (#691 review).
					mapped := api.MapError(ferr)
					return exitcode.Newf(exitcode.FromError(mapped),
						"could not re-read %s just before retiring it (%v), so it was not retired; rerun this command to finish",
						oldLoc, mapped)
				}
				if now := supersededByTargets(fresh.Node); len(now) != 1 || now[0] != successorLoc {
					_ = output.Write(f.IOStreams, f.JSON, result, render)
					return exitcode.Newf(exitcode.Conflict,
						"%s's successors changed while this ran (now: %s), so it was not retired; review them, then rerun this command",
						oldLoc, strings.Join(now, ", "))
				}
				oldNode = fresh.Node
				if retired, rerr := retire(cmd, client, oldNode, successorLoc, reason); rerr != nil {
					result.Retired = retired
					_ = output.Write(f.IOStreams, f.JSON, result, render)
					return retireError(retired, rerr, oldLoc, successorLoc, memURN)
				}
				result.Retired = boolRef(true)
				registerReminder(f.IOStreams.ErrOut, oldLoc, successorLoc)
				return output.Write(f.IOStreams, f.JSON, result, render)
			}

			// The successor's loc: named with --to (any valid loc, no edges
			// derived from it), or allocated in the legacy numbering, which
			// only a legacy rule/flow citation has.
			var newLoc, parentLoc, inheritLoc string
			var newSeq *int
			scaffoldOptional := true
			if toLoc != "" {
				newLoc = toLoc
				// The loc must be free: a live node there is refused, never
				// written over or retired against.
				if _, rerr := resolveSpecNode(cmd, client, memURN, newLoc); rerr == nil {
					return exitcode.Newf(exitcode.Usage, "%s already exists — name a free loc with --to", newLoc)
				} else if exitcode.FromError(rerr) != exitcode.NotFound {
					return rerr
				}
				newSeq = seqFromLoc(newLoc)
			} else {
				oldCit, perr := ParseCitation(oldLoc)
				if perr != nil || oldCit.Level() < 3 {
					return exitcode.Newf(exitcode.Usage,
						"%s is not a legacy rule/flow citation, so no replacement number can be allocated for it — name the replacement's loc with --to <loc>", oldLoc)
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

				newTarget, pl, il, err := planReplacement(oldCit, feature, ruleAfter, locs, allLocs)
				if err != nil {
					return err
				}
				newLoc, parentLoc, inheritLoc = newTarget.Format(), pl, il
				newSeq, scaffoldOptional = specSeq(newTarget), newTarget.Level() == 3
			}

			newTags := specTags(semanticTags(oldNode.Tags))
			name := specNameAt(newLoc, title)

			result := supersedeResultDTO{
				Old: oldLoc, New: newLoc, MemoryID: memURN,
				Name: name, Tags: newTags, DryRun: dryRun, Retired: boolRef(false),
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
			result.Edges = append(result.Edges, supersedeEdgeDTO{Label: supersededByLabel, Target: oldLoc + " → " + newLoc, Status: edgeStatusPlanned})

			render := func(w io.Writer) error { return renderSupersede(w, result) }
			if dryRun {
				return output.Write(f.IOStreams, f.JSON, result, render)
			}

			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Supersede %s with %s? The old spec is retired (tagged %q).", oldLoc, newLoc, supersededTag)); err != nil {
				return err
			}

			// 1. The replacement's body and abstract.
			var newID string
			body := rubricBodyAt(newLoc, title, scaffoldOptional)
			abs := placeholderAbstractAt(newLoc, title)
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
			edges, err := resolveSpecEdges(cmd, client, memURN, newLoc, structural, nil)
			if err != nil {
				return err
			}
			nodeType := "info"
			in := gen.CreateNodeInput{
				MemoryId: memURN, Loc: newLoc, Name: name,
				Tags: newTags, NodeType: &nodeType,
				Abstract: &abs, Content: &body, Data: specDataRaw(),
				Seq: newSeq, Role: specRole(),
				Edges: edges,
			}
			up, err := api.CreateSpecNode(cmd.Context(), client, &in)
			if err != nil {
				mapped := api.MapError(err)
				// No answer (exit 7) after a write is not "nothing happened": the
				// replacement, edges included, may have committed. A blind rerun
				// would allocate ANOTHER number and strand this one (#691 review),
				// so name the exact check, and what to do if it landed.
				if exitcode.FromError(mapped) == exitcode.Unavailable {
					return exitcode.Newf(exitcode.Unavailable,
						"creating replacement %s got no answer (%v), so it may have been created; before rerunning, check `hadron spec get %s -m %s` (a fresh node can take a minute to resolve). If it exists, do NOT rerun as-is — that would allocate another replacement — link it with `hadron spec link %s %s -m %s --label %s`, and rerun this command only once `hadron spec get %s -m %s` shows that %s edge. Only if it still does not exist after a minute (a fresh node can take that long to resolve), rerun",
						newLoc, mapped, newLoc, memURN,
						oldLoc, newLoc, memURN, supersededByLabel,
						oldLoc, memURN, supersededByLabel)
				}
				return mapped
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
			landed, other, fresh, lerr := supersededByState(cmd, client, oldNode.Id, newID)
			switch {
			case lerr == nil && other != "":
				// A create that SUCCEEDED is this run's edge even when a stale
				// re-read doesn't show it yet, so never report it as unwritten.
				wrote := landed || cerr == nil
				// An ERRORED create the re-read doesn't show is not confirmed
				// absent: it may have committed behind a stale read. So it is
				// `unknown`, never `failed` (#691 review).
				result.Edges[supersededByIdx].Status = edgeStatusUnknown
				if wrote {
					result.Edges[supersededByIdx].Status = edgeStatusCreated
				}
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				if wrote {
					return exitcode.Newf(exitcode.Conflict,
						"%s is now superseded by both %s and %s (another supersede ran at the same time), so it was not retired; keep one replacement, remove the other's %q edge, then rerun this command to finish",
						oldLoc, newLoc, other, supersededByLabel)
				}
				return exitcode.Newf(exitcode.Conflict,
					"created replacement %s, but %s is already superseded by %s (another supersede got there first), so %s was not retired; this run's %q edge to %s failed to confirm and may or may not exist (%v) — check with `hadron spec get %s -m %s` and review both replacements before changing anything",
					newLoc, oldLoc, other, oldLoc, supersededByLabel, newLoc, api.MapError(cerr),
					oldLoc, memURN)
			case cerr == nil && lerr != nil:
				// The link was written, but whether it is the ONLY successor can't
				// be checked, so don't retire on an unverified premise (#691
				// review). A rerun re-reads first, and finishes only when there is
				// exactly one successor.
				result.Edges[supersededByIdx].Status = edgeStatusCreated
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return exitcode.Newf(exitcode.Error,
					"linked %s to replacement %s but could not re-read %s to confirm it is the only successor (%v), so it was not retired; once `hadron spec get %s -m %s` shows its %s edge to %s, rerun this command to finish (rerunning before then can mint a second replacement)",
					oldLoc, newLoc, oldLoc, api.MapError(lerr),
					oldLoc, memURN, supersededByLabel, newLoc)
			case cerr == nil && !landed:
				// Written, but the re-read doesn't show it (a stale read?). Retire
				// only on a VERIFIED link, so stop; a rerun re-reads first.
				result.Edges[supersededByIdx].Status = edgeStatusCreated
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return exitcode.Newf(exitcode.Error,
					"linked %s to replacement %s, but re-reading %s did not show that link yet, so it was not retired; once `hadron spec get %s -m %s` shows its %s edge to %s, rerun this command to finish (rerunning before then can mint a second replacement)",
					oldLoc, newLoc, oldLoc,
					oldLoc, memURN, supersededByLabel, newLoc)
			case cerr == nil:
				// Written, seen, and the sole successor: retire below.
			case lerr == nil && landed:
				// The create errored but committed; carry on and finish.
			case lerr == nil:
				// The create errored and a re-read doesn't show the edge. That is
				// still not proof of absence — a read can lag a committed write, as
				// the success path already allows — so it is `unknown`, and the
				// remedy checks first (#691 review).
				result.Edges[supersededByIdx].Status = edgeStatusUnknown
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return exitcode.Newf(exitcode.Error,
					"created replacement %s, but creating the %q edge from %s errored (%v) and a re-read does not show it yet, so it may or may not exist; check `hadron spec get %s -m %s` — if after a minute it has no %s edge to %s, link them with `hadron spec link %s %s -m %s --label %s`, and once `hadron spec get %s -m %s` shows that edge, rerun this command to finish retiring %s (rerunning before then can mint a second replacement)",
					newLoc, supersededByLabel, oldLoc, api.MapError(cerr),
					oldLoc, memURN, supersededByLabel, newLoc,
					oldLoc, newLoc, memURN, supersededByLabel,
					oldLoc, memURN, oldLoc)
			default:
				result.Edges[supersededByIdx].Status = edgeStatusUnknown
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return exitcode.Newf(exitcode.Error,
					"created replacement %s but the %q edge from %s may or may not exist (%v, and re-reading %s failed: %v); check `hadron spec get %s -m %s` — if after a minute it has no %s edge to %s, link them with `hadron spec link %s %s -m %s --label %s`, and once `hadron spec get %s -m %s` shows that edge, rerun this command to finish retiring %s (rerunning before then can mint a second replacement)",
					newLoc, supersededByLabel, oldLoc, api.MapError(cerr), oldLoc, api.MapError(lerr),
					oldLoc, memURN, supersededByLabel, newLoc,
					oldLoc, newLoc, memURN, supersededByLabel,
					oldLoc, memURN, oldLoc)
			}
			result.Edges[supersededByIdx].Status = edgeStatusCreated
			// Retire against the FRESH read, never the first one: the retirement
			// writes whole tags and content, so a concurrent edit since the first
			// read would otherwise be overwritten (#691 review).
			if fresh != nil {
				oldNode = fresh
			}

			// 4. Retire the old spec: tag superseded, same loc, append a note.
			if retired, rerr := retire(cmd, client, oldNode, newLoc, reason); rerr != nil {
				result.Retired = retired
				_ = output.Write(f.IOStreams, f.JSON, result, render)
				return retireError(retired, rerr, oldLoc, newLoc, memURN)
			}

			result.Retired = boolRef(true)
			registerReminder(f.IOStreams.ErrOut, oldLoc, newLoc)
			return output.Write(f.IOStreams, f.JSON, result, render)
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to the memory set by hadron spec use, then the active memory)")
	cmd.Flags().StringVar(&title, "title", "", "human title for the replacement spec (required)")
	cmd.Flags().StringVar(&feature, "feature", "", "relocate the replacement under this existing feature (3 digits)")
	cmd.Flags().StringVar(&ruleAfter, "rule-after", "", "allocate the replacement rule strictly after this number")
	cmd.Flags().StringVar(&to, "to", "", "create the replacement at exactly this loc (any valid loc) instead of allocating a legacy number")
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
func supersededByState(cmd *cobra.Command, client graphql.Client, oldID, successorID string) (landed bool, other string, fresh *gen.GetNodeNode, err error) {
	resp, err := gen.GetNode(cmd.Context(), client, oldID)
	if err != nil {
		return false, "", nil, err
	}
	if resp.Node == nil {
		return false, "", nil, exitcode.Newf(exitcode.NotFound, "node %s not found", oldID)
	}
	fresh = resp.Node
	for _, sc := range supersededBySuccessors(resp.Node) {
		if sc.id != "" && sc.id == successorID {
			landed = true
		} else {
			other = sc.label
		}
	}
	return landed, other, fresh, nil
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

// retire tags the old spec superseded. When the update gets NO ANSWER it may
// still have committed (#691 review), so the old spec is re-read: a SEEN tag
// means retired. A re-read without it proves nothing (it may be stale, as the
// superseded-by check already allows), so that stays unknown. It returns true
// on success; false (with the error) when the update was refused outright; nil
// when retirement could not be established either way.
func retire(cmd *cobra.Command, client graphql.Client, oldNode *gen.GetNodeNode, successorLoc, reason string) (*bool, error) {
	err := retireSupersededSpec(cmd, client, oldNode, successorLoc, reason)
	if err == nil {
		return boolRef(true), nil
	}
	if exitcode.FromError(api.MapError(err)) != exitcode.Unavailable {
		return boolRef(false), err
	}
	resp, rerr := gen.GetNode(cmd.Context(), client, oldNode.Id)
	if rerr != nil || resp.Node == nil {
		return nil, err
	}
	if hasTag(resp.Node.Tags, supersededTag) {
		return boolRef(true), nil
	}
	return nil, err
}

// retireError is the error for a retirement that did not verifiably happen.
func retireError(retired *bool, err error, oldLoc, successorLoc, memURN string) error {
	if retired == nil {
		return exitcode.Newf(exitcode.Error,
			"linked %s to replacement %s, but the retirement update got no answer (%v) and re-reading %s did not confirm it, so whether it was retired is unknown; check `hadron spec get %s -m %s` for the %q tag — if it is still missing after a minute, rerun this command to finish",
			oldLoc, successorLoc, api.MapError(err), oldLoc, oldLoc, memURN, supersededTag)
	}
	return exitcode.Newf(exitcode.Error,
		"linked %s to replacement %s but failed to tag the old spec as retired: %v; rerun this command to finish the retirement update",
		oldLoc, successorLoc, api.MapError(err))
}

func boolRef(b bool) *bool { return &b }

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
	case r.Retired == nil:
		verb = "? retirement unknown:"
	case *r.Retired:
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

// registerReminder prints the register-ledger reminder for the locs that are
// actually IN the ledger: the register is the legacy numbering's, and a spec
// at any other loc is reported there as outside the numbering, not entered
// (#708). So a legacy old spec is marked retired, a legacy replacement is
// added, and a supersede entirely outside the numbering prints nothing
// (@codex on #710).
func registerReminder(w io.Writer, oldLoc, newLoc string) {
	_, oldErr := ParseCitation(oldLoc)
	_, newErr := ParseCitation(newLoc)
	switch {
	case oldErr == nil && newErr == nil:
		fmt.Fprintf(w, "reminder: update the register — mark %s retired and add %s to the ledger.\n", oldLoc, newLoc)
	case oldErr == nil:
		fmt.Fprintf(w, "reminder: update the register — mark %s retired (its replacement %s is outside the legacy numbering, so it has no ledger entry).\n", oldLoc, newLoc)
	case newErr == nil:
		fmt.Fprintf(w, "reminder: update the register — add %s to the ledger.\n", newLoc)
	}
}
