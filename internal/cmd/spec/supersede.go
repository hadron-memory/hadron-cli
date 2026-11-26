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

// Edge outcome, tracked per edge so the output reports what actually happened
// rather than echoing the plan as if every edge were created (#128).
const (
	edgeStatusPlanned = "planned" // dry-run / not yet executed
	edgeStatusCreated = "created"
	// The retirement got no usable answer: it may or may not exist. Same word as
	// a lost install answer elsewhere in the contract (#394).
	edgeStatusUnknown = "unknown"
)

// supersedeEdgeDTO is one structural edge with its execution outcome. It extends
// the plan-time plannedEdgeDTO shape with a status the real run fills in.
type supersedeEdgeDTO struct {
	Label  string `json:"label"`
	Target string `json:"target"`
	Status string `json:"status"`
}

// createdSpecDTO is the successor create mode made. It stays whatever the
// retirement answers (ruling 0212).
type createdSpecDTO struct {
	ID  string `json:"id"`
	Loc string `json:"loc"`
}

type supersedeResultDTO struct {
	Old      string `json:"old"`
	New      string `json:"new"`
	MemoryID string `json:"memoryId"`
	// Name and Tags describe the successor. With --by they are the server's, from
	// a successful answer; "" and [] before or without one.
	Name              string             `json:"name"`
	Tags              []string           `json:"tags"`
	Edges             []supersedeEdgeDTO `json:"edges"`
	DryRun            bool               `json:"dryRun"`
	ExistingSuccessor bool               `json:"existingSuccessor,omitempty"`
	// Retired is true after a success and false for a dry run. After a refused or
	// unanswered request it is what the re-read showed (see Current), null when
	// that is unknown.
	Retired     *bool   `json:"retired"`
	NodeID      string  `json:"nodeId"`
	Revision    int     `json:"revision"`
	Form        string  `json:"form"`
	SuccessorID *string `json:"successorId"`
	Reason      *string `json:"reason"`
	Summary     *string `json:"summary"`
	// Abstract is the retired spec's, as the server composed it; null unless the
	// server answered success.
	Abstract *string           `json:"abstract"`
	Created  *createdSpecDTO   `json:"created"`
	Outcome  string            `json:"outcome"`
	Current  *retireCurrentDTO `json:"current"`
	// Note says, on a dry run, that the server composes the text; null otherwise.
	Note *string `json:"note"`
}

func newCmdSupersede(f *cmdutil.Factory) *cobra.Command {
	var memory, title, feature, ruleAfter, reason, to, by, summaryText string
	var copyBody, yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "supersede <old-citation>",
		Short: "Retire a spec to a new draft or an existing successor",
		// Retiring a spec is the supersede flow, never a delete; point
		// the deletion verbs here rather than at the generic node
		// commands that do hard-delete a spec node.
		SuggestFor: []string{"delete", "rm", "remove", "retire", "deprecate"},
		Long: `Retire a spec in favour of a successor: a new DRAFT this command creates, or an
existing spec named with --by <citation> (that mode creates no node).

Both modes read the old spec for its revision, then send one retireSpec request.
In one transaction the server tags the spec "superseded", writes its banner and
abstract and adds the "superseded-by" edge. The server composes that text. --reason
and --summary are sent when given; the summary defaults to the stored abstract.

The server judges every retirement; the CLI refuses nothing on what it read. It
refuses, with exit 5 and nothing written by that request, a stale revision, a spec
that is already retired or already has a successor, and a successor that cannot be
used (the spec itself, another memory, retired). A spec linked to a successor but
not retired also exits 5: it needs manual review, the CLI makes no automatic
repair, and the earlier resume of a half-finished supersede no longer exists.
After such a refusal, or a request that got no usable answer (its outcome is
unknown, and it is not resent), the command reads the spec once and reports what it
currently shows, which is not proof of what this request did.

Create mode creates the successor first, then asks. The two are not one
transaction: if the retirement is refused or unanswered, the new successor stays,
the command names it, and it does not delete it, retry or create another. Retire to
it with --by once the cause is fixed.

--by takes a citation in the same memory and cannot combine with --title, --to,
--feature, --rule-after or --copy-body.

--dry-run writes nothing and prints what would be sent (and, in create mode, the
successor's planned citation and edges), after checking your write access to the
memory: a denied caller gets exit 8. A server without retireSpec is refused; there
is no fallback to separate writes, and create mode on such a server leaves the
successor it created.

The replacement starts unminted and unapproved: its body is the scaffold
(or a copy of the old body with --copy-body). Edit it (` + "`hadron spec edit`" + `),
approve it (` + "`hadron node approve`" + `) and mint it (` + "`hadron spec mint`" + `) afterwards;
until then it is not a permanent citation. Use supersede when the old spec is
minted and cannot be renumbered.

Name the replacement's loc with --to <loc> (any valid loc; it must be free).
Without --to, a legacy rule or flow citation gets the next free number in the
legacy numbering (in the same feature by default; --feature relocates it to another
existing feature), with that numbering's table-of-contents and inheritance edges; a
spec outside the legacy numbering needs --to. The replacement keeps a dotted spec
role such as spec.rule; a legacy tag-only spec gets the base spec role. If the
corpus has a register node, update its ledger afterward (the tool prints a
reminder only then; it never edits the register).`,
		Example: `  hadron spec supersede msg:010:02 -m hrn:mem:micromentor.org:platform-specs --title "W2 v2" --yes
  hadron spec supersede onboarding:mentor:screens -m hrn:mem:micromentor.org:specs --by onboarding:mentor:screens-v2 --reason "ruling 0099" --yes
  hadron spec supersede msg:010:02 -m hrn:mem:micromentor.org:platform-specs --title "W2 v2" --copy-body --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("by") {
				if strings.TrimSpace(by) == "" {
					return exitcode.Newf(exitcode.Usage, "--by is blank; name an existing successor citation")
				}
				for _, flag := range []string{"title", "to", "feature", "rule-after", "copy-body"} {
					if cmd.Flags().Changed(flag) {
						return exitcode.Newf(exitcode.Usage, "--by uses an existing successor; cannot combine it with --%s", flag)
					}
				}
				var err error
				by, err = validateSpecLoc(by)
				if err != nil {
					return err
				}
			}
			if by == "" && title == "" {
				return exitcode.Newf(exitcode.Usage, "--title is required")
			}
			why, err := givenText(cmd, "reason", reason)
			if err != nil {
				return err
			}
			summary, err := givenText(cmd, "summary", summaryText)
			if err != nil {
				return err
			}
			// The old spec's loc, too, is validated before any request (@copilot
			// on #710).
			if _, verr := validateSpecLoc(args[0]); verr != nil {
				return verr
			}
			// --to is validated up front, before any request and whichever path
			// runs below: a resumed run must not accept a --to it would then
			// ignore (@codex on #710).
			// A GIVEN --to that is blank is refused, never read as "no --to": a
			// script's empty "$TO" would otherwise allocate a numbered
			// replacement and retire the old spec (@codex P1 on #710).
			if cmd.Flags().Changed("to") && strings.TrimSpace(to) == "" {
				return exitcode.Newf(exitcode.Usage, "--to is blank; name the replacement's loc, or omit --to to allocate a legacy number")
			}
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
					return exitcode.Newf(exitcode.Usage, "a spec cannot supersede itself (the citation given is not shown)")
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

			oldNode, err := fetchForRetire(cmd, client, memURN, args[0])
			if err != nil {
				return err
			}
			oldLoc := oldNode.Loc
			result := supersedeResultDTO{
				Old: oldLoc, MemoryID: memURN, Tags: []string{}, DryRun: dryRun, Retired: boolRef(false),
				NodeID: oldNode.Id, Revision: oldNode.Revision, Form: string(gen.RetireSpecFormSupersede),
				Reason: why, Summary: summary, Outcome: retireOutcomePlanned,
			}
			retirement := &gen.RetireSpecInput{
				AuthoredWith: cmdutil.AuthoredWith(cmd.Context()),
				Ref:          oldNode.Id, Form: gen.RetireSpecFormSupersede,
				ExpectedRevision: oldNode.Revision, Reason: why, Summary: summary,
			}
			if dryRun {
				result.Note = noteRef(dryRunNote)
			}
			render := func(w io.Writer) error { return renderSupersede(w, result) }
			// retire asks the door once and writes the result, whichever way it ends.
			retire := func(created string, remind func()) error {
				// The retirement link is the LAST edge: a ToC edge carries the
				// user's --title, which could itself be "superseded-by" (Codex #155).
				link := &result.Edges[len(result.Edges)-1]
				retirement.SuccessorRef = result.SuccessorID
				retired, failure := sendRetire(cmd, client, retirement)
				if failure != nil {
					report := reportRetireFailure(cmd, client, retirement, failure, created)
					result.Outcome, result.Current, result.Retired = report.outcome, report.current, nil
					link.Status = edgeStatusRefused
					if report.outcome == retireOutcomeUnknown {
						link.Status = edgeStatusUnknown
					}
					if report.current != nil {
						result.Retired = report.current.Retired
					}
					// With nothing created and nothing re-read there is only the refusal.
					if report.current != nil || created != "" {
						if werr := output.Write(f.IOStreams, f.JSON, result, render); werr != nil {
							return werr
						}
					}
					return report.err
				}
				target := retired.SuccessorEdge.Target
				result.Retired, result.Outcome = boolRef(true), retireOutcomeRetired
				result.Abstract = retired.Node.Abstract
				result.New, link.Target, link.Status = target.Loc, oldLoc+" → "+target.Loc, edgeStatusCreated
				if result.ExistingSuccessor {
					result.Name, result.Tags = target.Name, tagsOrEmpty(target.Tags)
				}
				remind()
				if werr := output.Write(f.IOStreams, f.JSON, result, render); werr != nil {
					return exitcode.Newf(exitcode.Error, "the spec (the citation given is not shown) was superseded (the server answered at revision %d), but its result could not be written to the output: %v; check `hadron spec get <citation> -m <memory>`",
						retired.Node.Revision, werr)
				}
				return nil
			}

			if by != "" {
				successorID, err := resolveSpecNode(cmd, client, memURN, by)
				if err != nil {
					return err
				}
				result.New, result.ExistingSuccessor, result.SuccessorID = by, true, &successorID
				result.Edges = []supersedeEdgeDTO{{Label: supersededByLabel, Target: oldLoc + " → " + by, Status: edgeStatusPlanned}}
				if dryRun {
					if err := cmdutil.RequireWriteAccessForDryRun(cmd, client, memURN); err != nil {
						return err
					}
					return output.Write(f.IOStreams, f.JSON, result, render)
				}
				if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Retire %s as superseded by existing %s? No node is created.", oldLoc, by)); err != nil {
					return err
				}
				return retire("", registerCheck(cmd, client, memURN, f.IOStreams.ErrOut, oldLoc, by))
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
					return exitcode.Newf(exitcode.Usage, "the loc given with --to (not shown) already exists — name a free loc with --to")
				} else if exitcode.FromError(rerr) != exitcode.NotFound {
					return rerr
				}
				newSeq = seqFromLoc(newLoc)
			} else {
				oldCit, perr := ParseCitation(oldLoc)
				if perr != nil || oldCit.Level() < 3 {
					return exitcode.Newf(exitcode.Usage,
						"the spec (the citation given is not shown) is not a legacy rule/flow citation, so no replacement number can be allocated for it — name the replacement's loc with --to <loc>")
				}
				// Scan the module subtree for allocation + parent checks. Paged to
				// exhaustion — a truncated scan here would make the replacement
				// allocator reuse a live number on a subtree past one page (#23).
				prefix := Citation{Product: oldCit.Product, Module: oldCit.Module}.Format()
				all, err := scanAllNodes(cmd.Context(), client, &memURN, &prefix)
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
			result.New, result.Name, result.Tags = newLoc, name, newTags
			if parentLoc != "" {
				result.Edges = append(result.Edges, supersedeEdgeDTO{Label: title, Target: parentLoc, Status: edgeStatusPlanned})
			}
			if inheritLoc != "" {
				result.Edges = append(result.Edges, supersedeEdgeDTO{Label: inheritEdgeLabel, Target: inheritLoc, Status: edgeStatusPlanned})
			}
			result.Edges = append(result.Edges, supersedeEdgeDTO{Label: supersededByLabel, Target: oldLoc + " → " + newLoc, Status: edgeStatusPlanned})

			if dryRun {
				if err := cmdutil.RequireWriteAccessForDryRun(cmd, client, memURN); err != nil {
					return err
				}
				return output.Write(f.IOStreams, f.JSON, result, render)
			}

			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Supersede %s with %s? The old spec is retired (tagged %q).", oldLoc, newLoc, supersededTag)); err != nil {
				return err
			}

			// 1. The replacement's body and abstract. oldNode is the raw read, so a
			// copied body keeps its {{…}} placeholders (cli#742).
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
			for _, e := range result.Edges[:len(result.Edges)-1] { // the retirement link is the door's
				structural = append(structural, plannedEdgeDTO{Label: e.Label, Target: e.Target})
			}
			edges, err := resolveSpecEdges(cmd, client, memURN, structural, nil)
			if err != nil {
				return err
			}
			nodeType := "info"
			// A successor keeps the old spec's dotted kind. A tag-only legacy
			// spec still starts with the base role; another open role must not
			// be forwarded through the spec door.
			successorRole := specRole()
			if api.RoleInFamily(oldNode.Role, api.SpecNodeRole) {
				successorRole = oldNode.Role
			}
			remind := registerCheck(cmd, client, memURN, f.IOStreams.ErrOut, oldLoc, newLoc)
			up, err := api.CreateSpecNode(cmd.Context(), client, &gen.CreateNodeInput{
				AuthoredWith: cmdutil.AuthoredWith(cmd.Context()),
				MemoryRef:    memURN, Loc: newLoc, Name: name,
				Tags: newTags, NodeType: &nodeType,
				Abstract: &abs, Content: &body, Data: specDataRaw(),
				Seq: newSeq, Role: successorRole,
				Edges: edges,
			})
			if err != nil {
				mapped := api.MapError(err)
				// No answer (exit 7) after a write is not "nothing happened": the
				// replacement, edges included, may have committed. A blind rerun
				// would allocate ANOTHER number and strand this one (#691 review),
				// so name the exact check, and what to do if it exists.
				if exitcode.FromError(mapped) == exitcode.Unavailable {
					return exitcode.Newf(exitcode.Unavailable,
						"creating %s got no answer (%v), so it may have been created; no retirement was requested. Before rerunning, check `hadron spec get <replacement> -m <memory>` (a fresh node can take a minute to resolve). If it exists, do NOT rerun as-is (that would allocate another replacement): retire to it with `hadron spec supersede <citation> --by <replacement> -m <memory>`. Only if it still does not exist after a minute, rerun",
						replacementName(toLoc != "", newSeq), mapped)
				}
				return mapped
			}
			for i := range result.Edges[:len(result.Edges)-1] {
				result.Edges[i].Status = edgeStatusCreated
			}
			result.Created, result.SuccessorID = &createdSpecDTO{ID: up.Id, Loc: up.Loc}, &up.Id
			return retire(up.Id, remind)
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to the memory set by hadron spec use, then the active memory)")
	cmd.Flags().StringVar(&title, "title", "", "human title for a new replacement spec (required unless --by is used)")
	cmd.Flags().StringVar(&feature, "feature", "", "relocate the replacement under this existing feature (3 digits)")
	cmd.Flags().StringVar(&ruleAfter, "rule-after", "", "allocate the replacement rule strictly after this number")
	cmd.Flags().StringVar(&to, "to", "", "create the replacement at exactly this loc (any valid loc) instead of allocating a legacy number")
	cmd.Flags().StringVar(&reason, "reason", "", "why it is retired, kept in the server's banner")
	cmd.Flags().BoolVar(&copyBody, "copy-body", false, "seed the replacement's body/abstract from the old spec")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "write nothing: print what would be sent")
	cmd.Flags().StringVar(&by, "by", "", "retire to this existing citation in the same memory; creates no node")
	cmd.Flags().StringVar(&summaryText, "summary", "", "historical summary for the retired abstract (default: its stored abstract)")
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
			return Citation{}, "", "", exitcode.Newf(exitcode.NotFound, "the feature (not shown) does not exist")
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

// replacementName names a replacement whose create got no answer, so nothing reported it: by the
// number allocated for it, or as the --to loc. Never by a citation the operator typed.
func replacementName(named bool, seq *int) string {
	if named {
		return "the replacement named with --to (not shown)"
	}
	if seq == nil {
		return "the replacement (the citation given is not shown)"
	}
	return fmt.Sprintf("the replacement numbered %02d under its parent (the citation given is not shown)", *seq)
}

func boolRef(b bool) *bool { return &b }

func noteRef(s string) *string { return &s }

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
	say, failed := sayer(w)
	// "✓ superseded" only when the server answered success: a refused or
	// unanswered run prints its result right before the error (#691 review).
	verb := "✓ superseded"
	switch r.Outcome {
	case retireOutcomePlanned:
		verb = "would supersede"
	case retireOutcomeRefused:
		verb = "✗ retirement refused:"
	case retireOutcomeUnknown:
		verb = "? retirement outcome unknown:"
	}
	say("%s %s → %s", verb, r.Old, r.New)
	if r.Name != "" {
		say(" — %s", output.SafeCell(r.Name))
	}
	say("\n")
	if r.Created != nil {
		say("  created successor: %s (id %s)\n", output.SafeCell(r.Created.Loc), output.SafeCell(r.Created.ID))
	}
	if len(r.Tags) > 0 {
		say("  tags: %v\n", r.Tags)
	}
	for _, e := range r.Edges {
		// Dry-run / not-yet-executed edges carry no meaningful status, so keep the
		// plain form; a real run annotates each with its status.
		if e.Status == "" || e.Status == edgeStatusPlanned {
			say("  edge: %s → %s\n", e.Label, e.Target)
		} else {
			say("  edge [%s]: %s → %s\n", e.Status, e.Label, e.Target)
		}
	}
	if r.Current != nil {
		say("  %s\n", r.Current.Statement)
	}
	if r.Outcome == retireOutcomePlanned {
		successor := r.New
		if !r.ExistingSuccessor {
			successor += " (to be created)"
		}
		say("  successor: %s\n", successor)
		renderInputs(say, gen.RetireSpecFormSupersede, r.Old, r.NodeID, r.Revision, r.Reason, r.Summary)
	}
	return failed()
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
