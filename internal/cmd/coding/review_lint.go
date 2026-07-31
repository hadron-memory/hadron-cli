package coding

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// triggerStem is the conventional opening of a check's edge label; the label
// must carry a real condition after it.
const triggerStem = "applies when"

// reRelLoc matches the server's derived `<source>:rel:<target>` edge loc, which
// appears when an edge has no name. It corroborates an empty label rather than
// being a defect of its own — the label is what broke.
var reRelLoc = regexp.MustCompile(`(?i):rel:`)

// reviewInput is everything the rule engine needs, so the engine itself is pure
// and unit-testable without a server.
type reviewInput struct {
	Members     map[string]checkNode // checklist items, by loc
	Edges       map[string]graphEdge // loc → its edge to the review parent
	Unavailable []string             // edge sources that could not be read
	Toolchain   string               // "" = infer; "-" = disabled
	Suggest     bool                 // quote the body's scope paragraph in full
}

func newCmdReviewLint(f *cmdutil.Factory) *cobra.Command {
	var memory, root, toolchain string
	var strict, suggest bool
	var fix, yes bool
	cmd := &cobra.Command{
		Use:     "lint",
		Aliases: []string{"check", "validate"},
		Short:   "Validate the review checklist tree's trigger edges",
		Long: `Validate every check under the review parent.

A check is triaged by ` + "`tasks:review-changes`" + ` reading its edge label
back to the review parent, so a label that is not a condition — "child-of",
"related", an empty label — makes the check silently stop firing.

A node counts as a checklist item when it sits under the parent's loc
prefix (` + "`review:`" + ` by default) and is neither tagged ` + "`meta`" + `
nor runnable. That excludes the router, the tasks that consume the tree, the
meta backlog, pattern nodes, and findings — all of which legitimately hang
off the same parent, some of them carrying a ` + "`review`" + ` tag.

Errors exit 5; --strict promotes warnings to errors too.`,
		Example: `  hadron coding review lint -m micromentor.org::mmdata
  hadron coding review lint -m hadronmemory.com::hadron-portal --json
  hadron coding review lint -m micromentor.org::mmdata --fix --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if yes && !fix {
				return exitcode.Newf(exitcode.Usage, "--yes only applies with --fix")
			}
			mem, err := codingMemoryURN(memory)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			edges, _, err := fetchRootEdges(ctx, client, mem, root, true)
			if err != nil {
				return err
			}

			// Two sources of candidate checks: nodes under the root's child
			// prefix (which catches a check with no edge at all — the
			// highest-severity finding), and the far endpoints of the parent's
			// inbound edges (which catches one that is unreadable).
			listed, err := scanPrefix(ctx, client, mem, childPrefix(root))
			if err != nil {
				return err
			}
			candidates := map[string]string{} // node id → loc
			for _, n := range listed {
				if n == nil {
					continue
				}
				if isChecklistItemListing(root, n.Loc, n.Tags, n.IsRunnable) {
					candidates[n.Id] = n.Loc
				}
			}
			edgeByLoc := map[string]graphEdge{}
			var redacted []graphEdge
			for _, e := range edges {
				if e.OtherID == "" {
					// The server redacted the endpoint projection, so there is
					// no node to read or classify — report it rather than
					// letting it vanish from the sweep.
					redacted = append(redacted, e)
					continue
				}
				if e.Other != "" {
					edgeByLoc[e.Other] = e
				}
				candidates[e.OtherID] = e.Other
			}

			nodes, unavailable, err := fetchNodes(ctx, client, candidates)
			if err != nil {
				return err
			}
			for _, e := range redacted {
				unavailable = append(unavailable, e.endpointName())
			}

			members := map[string]checkNode{}
			for loc, n := range nodes {
				if isChecklistItem(root, n) {
					members[loc] = n
				}
			}
			// An unreadable node can't be tested against the predicate, so its
			// membership is indeterminate — reported, never dropped.
			in := reviewInput{Members: members, Edges: edgeByLoc, Unavailable: unavailable, Toolchain: toolchain, Suggest: suggest}
			findings := lintReview(in)

			if fix {
				applied, ferr := applyReviewFix(ctx, client, f, in, findings, yes)
				if ferr != nil {
					return ferr
				}
				if applied > 0 {
					// Re-derive from the repaired state so the report reflects
					// what is true after the write, not before.
					for _, fx := range planReviewFix(in, findings) {
						e := in.Edges[fx.Loc]
						e.Label = fx.NewLabel
						in.Edges[fx.Loc] = e
					}
					findings = lintReview(in)
					fmt.Fprintf(f.IOStreams.ErrOut, "fixed %d edge label(s)\n", applied)
				}
			}

			if strict {
				for i := range findings {
					if findings[i].Severity == sevWarning {
						findings[i].Severity = sevError
					}
				}
			}
			hasError := false
			for _, fd := range findings {
				if fd.Severity == sevError {
					hasError = true
					break
				}
			}

			if err := output.Write(f.IOStreams, f.JSON, findings, func(w io.Writer) error {
				if len(findings) == 0 {
					fmt.Fprintf(w, "✓ %d check(s) OK\n", len(members))
					return nil
				}
				t := output.NewTable(w, "NODE", "SEVERITY", "RULE", "MESSAGE")
				for _, fd := range findings {
					t.Row(fd.Node, fd.Severity, fd.Rule, fd.Message)
				}
				return t.Flush()
			}); err != nil {
				return err
			}
			if hasError {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to lint (org::memory)")
	cmd.Flags().StringVar(&root, "root", reviewRootLoc, "loc of the review parent node")
	cmd.Flags().StringVar(&toolchain, "toolchain", "", `the memory's toolchain for the foreign-toolchain check (e.g. "ts"; "-" disables; default: inferred)`)
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors")
	cmd.Flags().BoolVar(&suggest, "suggest", false, "quote the body's scope paragraph in full rather than truncated")
	cmd.Flags().BoolVar(&fix, "fix", false, "promote a check's description into an empty/non-condition edge label where possible")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt for --fix")
	return cmd
}

// lintReview is the pure rule engine. Findings come back sorted (node, rule) so
// the output and the --json contract are deterministic.
func lintReview(in reviewInput) []findingDTO {
	out := []findingDTO{}

	locs := make([]string, 0, len(in.Members))
	for l := range in.Members {
		locs = append(locs, l)
	}
	sort.Strings(locs)

	// Per-check rules.
	valid := map[string]string{} // loc → normalised trigger, for duplicate detection
	for _, loc := range locs {
		n := in.Members[loc]
		e, hasEdge := in.Edges[loc]
		if !hasEdge {
			out = append(out, findingDTO{loc, "parent-edge-exists", sevError,
				"no edge to the review parent — invisible to tasks:review-changes"})
			continue
		}
		label := strings.TrimSpace(e.Label)
		// The text the label should carry is usually already in the body;
		// quote it so the fixer doesn't have to open the node (#331).
		hint := triggerHint(n.Content, in.Suggest)
		switch {
		case label == "":
			msg := "edge label is empty — the check can never match a diff"
			if reRelLoc.MatchString(e.Loc) {
				msg += fmt.Sprintf(" (its derived loc %q confirms the name was never set)", e.Loc)
			}
			out = append(out, findingDTO{loc, "label-present", sevError, msg + hint})
		case !strings.HasPrefix(strings.ToLower(label), triggerStem):
			out = append(out, findingDTO{loc, "label-is-condition", sevError,
				fmt.Sprintf("edge label %q is not a condition — expected %q followed by the trigger", label, triggerStem) + hint})
		case len(strings.TrimSpace(label[len(triggerStem):])) == 0:
			out = append(out, findingDTO{loc, "label-is-condition", sevError,
				fmt.Sprintf("edge label is the bare stem %q with no condition after it", label) + hint})
		default:
			valid[loc] = strings.ToLower(strings.Join(strings.Fields(label), " "))
		}
		if strings.TrimSpace(n.Description) == "" {
			out = append(out, findingDTO{loc, "description-present", sevWarning,
				"no description — list and search output show the description, so the trigger is invisible there"})
		}
	}

	out = append(out, lintDuplicateTriggers(valid)...)
	out = append(out, lintSeq(in, locs)...)
	out = append(out, lintToolchain(in, locs, valid)...)

	for _, u := range sortedCopy(in.Unavailable) {
		out = append(out, findingDTO{u, "check-node-resolves", sevWarning,
			"edge source could not be read — membership is indeterminate, so it was neither linted nor dismissed"})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

// lintDuplicateTriggers flags checks sharing a trigger — usually one was cloned
// and never re-pointed. Only well-formed triggers take part, so a pair of
// `child-of` labels is reported once by label-is-condition, not twice.
func lintDuplicateTriggers(valid map[string]string) []findingDTO {
	byTrigger := map[string][]string{}
	for loc, t := range valid {
		byTrigger[t] = append(byTrigger[t], loc)
	}
	var out []findingDTO
	for t, locs := range byTrigger {
		if len(locs) < 2 {
			continue
		}
		sort.Strings(locs)
		for _, loc := range locs {
			others := make([]string, 0, len(locs)-1)
			for _, o := range locs {
				if o != loc {
					others = append(others, o)
				}
			}
			out = append(out, findingDTO{loc, "duplicate-trigger", sevWarning,
				fmt.Sprintf("trigger %q is shared with %s — likely a clone that was never re-pointed", t, strings.Join(others, ", "))})
		}
	}
	return out
}

// lintSeq flags siblings sharing a seq, which makes their order
// non-deterministic. An unset seq is not a finding — most checks leave it nil.
func lintSeq(in reviewInput, locs []string) []findingDTO {
	bySeq := map[int][]string{}
	for _, loc := range locs {
		if s := in.Members[loc].Seq; s != nil {
			bySeq[*s] = append(bySeq[*s], loc)
		}
	}
	var out []findingDTO
	for s, ls := range bySeq {
		if len(ls) < 2 {
			continue
		}
		sort.Strings(ls)
		for _, loc := range ls {
			out = append(out, findingDTO{loc, "seq-unique", sevWarning,
				fmt.Sprintf("seq %d is shared with %s — sibling ordering is non-deterministic", s, strings.Join(without(ls, loc), ", "))})
		}
	}
	return out
}

// toolchainFamilies maps a family to the words that name it in a trigger.
var toolchainFamilies = map[string]*regexp.Regexp{
	"dart":   regexp.MustCompile(`(?i)\b(dart|flutter|pubspec)\b`),
	"ts":     regexp.MustCompile(`(?i)(\btypescript\b|\bjavascript\b|\.tsx?\b|\bnode\.js\b|\bnpm\b|\btsconfig\b)`),
	"go":     regexp.MustCompile(`(?i)(\bgolang\b|\bgo\.mod\b|\.go\b)`),
	"swift":  regexp.MustCompile(`(?i)\b(swift|xcode|cocoapods)\b`),
	"kotlin": regexp.MustCompile(`(?i)\b(kotlin|gradle)\b`),
	"python": regexp.MustCompile(`(?i)(\bpython\b|\bpip\b|\.py\b)`),
}

// lintToolchain flags a trigger naming a toolchain the memory is not about —
// the misfiled check that can never match a diff in its own repo.
//
// Heuristic and warn-only. The memory's own family is inferred from the whole
// member corpus (names and descriptions, not just triggers — triggers alone are
// too sparse to break a tie), and the rule stays SILENT when it cannot pick a
// clear winner rather than guessing. --toolchain overrides; "-" disables.
func lintToolchain(in reviewInput, locs []string, valid map[string]string) []findingDTO {
	if in.Toolchain == "-" {
		return nil
	}
	home := in.Toolchain
	if home == "" {
		home = inferToolchain(in, locs)
	}
	if home == "" {
		return nil
	}
	var out []findingDTO
	for _, loc := range locs {
		trigger, ok := valid[loc]
		if !ok {
			continue // a broken label is already reported; don't pile on
		}
		fams := familiesIn(trigger)
		if len(fams) != 1 || fams[0] == home {
			continue
		}
		out = append(out, findingDTO{loc, "foreign-toolchain", sevWarning,
			fmt.Sprintf("trigger names %s but the memory looks like %s — it can never match a diff here", fams[0], home)})
	}
	return out
}

// inferToolchain picks the family the member corpus mentions most, requiring a
// strict winner so an even split yields no opinion.
func inferToolchain(in reviewInput, locs []string) string {
	counts := map[string]int{}
	for _, loc := range locs {
		n := in.Members[loc]
		text := n.Loc + " " + n.Name + " " + n.Description
		if e, ok := in.Edges[loc]; ok {
			text += " " + e.Label
		}
		for _, fam := range familiesIn(text) {
			counts[fam]++
		}
	}
	best, bestN, tied := "", 0, false
	for fam, c := range counts {
		switch {
		case c > bestN:
			best, bestN, tied = fam, c, false
		case c == bestN:
			tied = true
		}
	}
	if tied || bestN == 0 {
		return ""
	}
	return best
}

func familiesIn(text string) []string {
	var out []string
	for fam, re := range toolchainFamilies {
		if re.MatchString(text) {
			out = append(out, fam)
		}
	}
	sort.Strings(out)
	return out
}

func without(all []string, drop string) []string {
	out := make([]string, 0, len(all))
	for _, s := range all {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
