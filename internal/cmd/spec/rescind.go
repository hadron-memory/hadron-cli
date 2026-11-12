package spec

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#809 (split A, Vera 2026-10-31): retire a spec WITHOUT a successor.
//
// `supersede` always means a successor exists; `rescind` always means none. The
// citation stays and is never renumbered; the node is tagged `superseded` so
// retrieval filters treat it as not live; the banner (body) and the abstract
// prefix say why. One guarded write: the immutable node id and the revision the
// read saw, so a concurrent change is refused (NODE_WRITE_CONFLICT) with nothing
// written. No edge, no allocation, no mint, no inheritance-edge change.

const (
	rescindedWord = "RESCINDED"
	withdrawnWord = "WITHDRAWN"
)

// rescindResultDTO is the stable --json shape.
type rescindResultDTO struct {
	Loc      string `json:"loc"`
	MemoryID string `json:"memoryId"`
	NodeID   string `json:"nodeId"`
	// Revision is the revision the write was (or, with --dry-run, would be) guarded on.
	Revision int `json:"revision"`
	// Retired is true once the spec verifiably carries the superseded tag and the
	// RESCINDED/WITHDRAWN prefix; false when it verifiably does not (a dry run, a refused
	// write); null when a write got no answer and a re-read could not settle it.
	Retired *bool  `json:"retired"`
	Wording string `json:"wording"`
	// Abstract is the abstract this command writes (or, with --dry-run, would write); for an
	// already-rescinded spec it is the STORED abstract.
	Abstract         string   `json:"abstract"`
	Withdrawn        bool     `json:"withdrawn"`
	DryRun           bool     `json:"dryRun"`
	AlreadyRescinded bool     `json:"alreadyRescinded"`
	Tags             []string `json:"tags"`
}

func newCmdRescind(f *cmdutil.Factory) *cobra.Command {
	var memory, reason, summary, summaryFile string
	var withdrawn, yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "rescind <citation>",
		Short: "Retire a spec with no successor (rescinded, or withdrawn never in force)",
		Long: `Retire a spec that has no successor: a rule that was in force and is withdrawn
(Rescinded), or a draft that never took effect (--withdrawn).

The spec stays at its citation, which is never renumbered or deleted. One guarded
update, bound to the node's id and the revision this command read, sets together:
  - the "superseded" tag, so retrieval filters treat it as not live;
  - a banner at the top of the body ("Rescinded (<UTC date>, <reason>), no successor",
    or "Withdrawn, never in force");
  - the abstract: "RESCINDED (<date>); do not apply. <reason> Historical: <summary>"
    (WITHDRAWN for --withdrawn).
No edge is written: a spec retired in favour of another one uses ` + "`hadron spec supersede`" + `.
Nothing is allocated, minted or re-linked, and the specs that cite this one are not
rewritten (that is a corpus task).

--reason and --summary are required; there is no inferred "in force" status, so say
--withdrawn yourself for a draft. Rescinding an in-force Decided spec needs a recorded
ruling: name it in --reason. The abstract is hashed content, so rescinding ends the
spec's approval; re-approval or re-mint is a separate step.

A stale revision is refused with exit 5 and nothing is written; rerun to start from a
fresh read. Rerunning on a spec that is already rescinded writes nothing. A write that
gets no answer is checked by re-reading, and the output says "unknown" when that cannot
settle it. --dry-run reads only and prints the planned tag, banner, abstract, node id
and revision.`,
		Example: `  hadron spec rescind cor:spc:020:07 -m hrn:mem:hadronmemory.com:specs --reason "ruling 0099 A" --summary "Placeholders were once confined to drafts." --yes
  hadron spec rescind msg:010:09 -m hrn:mem:acme.com:specs --withdrawn --reason "draft dropped" --summary-file why.md --dry-run`,
		SuggestFor: []string{"retire", "withdraw", "deprecate"},
		Args:       cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := validateSpecLoc(args[0])
			if err != nil {
				return err
			}
			reason = strings.TrimSpace(reason)
			if reason == "" {
				return exitcode.Newf(exitcode.Usage, "--reason is required: say why (and name the ruling for an in-force Decided spec)")
			}
			body, err := cmdutil.ResolveTextInput("summary", summary, summaryFile, f.IOStreams.In)
			if err != nil {
				return err
			}
			body = strings.TrimSpace(body)
			if body == "" {
				return exitcode.Newf(exitcode.Usage, "--summary is required and must not be empty: one line of what the spec used to say")
			}
			// One line each, so the abstract stays a single paragraph.
			reason, body = oneLine(reason), oneLine(body)
			now := time.Now().UTC().Format("2006-01-02")
			plan := rescindPlan(now, reason, body, withdrawn)
			// Checked before any request: the server rejects a longer abstract after the read.
			// The server counts the abstract in UTF-16 code units (`.length`), so a rune count would admit an
			// emoji-heavy abstract it rejects: the lint's own count is used, and the refusal is before any request.
			if n := abstractLength(&plan.abstractPrefixAndText); n > abstractHardMax {
				return exitcode.Newf(exitcode.Usage, "the abstract would be %d characters (counted as the server counts them); the cap is %d — shorten --reason or --summary", n, abstractHardMax)
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}

			// Read 1: the structure (edges). A spec that already names a successor is the
			// supersede case, never rescinded.
			shape, err := fetchSpecTaggedNode(cmd, client, memURN, loc)
			if err != nil {
				return err
			}
			if succ := supersededByTargets(shape); len(succ) > 0 {
				return exitcode.Newf(exitcode.Usage,
					"%s already has a %q edge to %s, so it is superseded, not rescinded; use `hadron spec supersede` for a spec with a successor (nothing was written)",
					shape.Loc, supersededByLabel, strings.Join(succ, ", "))
			}
			// Read 2: the stored body, abstract and revision, the snapshot the write is bound to.
			node, err := fetchSpecForEdit(cmd, client, memURN, loc)
			if err != nil {
				return err
			}
			if node.Id != shape.Id {
				return exitcode.Newf(exitcode.Conflict,
					"%s resolved to a different node between the two reads (%s, then %s), so nothing was written; rerun this command", loc, shape.Id, node.Id)
			}
			result := rescindResultDTO{
				Loc: node.Loc, MemoryID: node.MemoryId, NodeID: node.Id, Revision: node.Revision,
				Retired: boolRef(false), Wording: plan.banner, Abstract: plan.abstractPrefixAndText, Withdrawn: withdrawn, DryRun: dryRun,
				Tags: specTags(rescindTags(node.Tags)),
			}
			// Already rescinded: the tag AND the RESCINDED/WITHDRAWN prefix. A rerun writes nothing.
			if hasTag(node.Tags, supersededTag) {
				if isRescindedAbstract(node.Abstract) {
					// Report what is STORED, never the flags of this run: a rerun with different
					// wording or --withdrawn must not read as if it had applied them.
					result.AlreadyRescinded, result.Retired, result.Tags = true, boolRef(true), specTags(tagsOrEmpty(node.Tags))
					result.Abstract = derefStr(node.Abstract)
					result.Wording = storedWording(result.Abstract)
					result.Withdrawn = strings.HasPrefix(strings.TrimSpace(result.Abstract), withdrawnWord+" (")
					return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error { return renderRescind(w, result, plan) })
				}
				return exitcode.Newf(exitcode.Conflict,
					"%s is tagged %q but is not recorded as rescinded or withdrawn (its abstract has no %s/%s prefix) and has no successor edge; review it before retiring it, nothing was written",
					node.Loc, supersededTag, rescindedWord, withdrawnWord)
			}
			newBody := withBanner(derefStr(node.Content), plan.banner)
			render := func(w io.Writer) error { return renderRescind(w, result, plan) }
			if dryRun {
				if err := cmdutil.RequireWriteAccessForDryRun(cmd, client, memURN); err != nil {
					return err
				}
				return output.Write(f.IOStreams, f.JSON, result, render)
			}
			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Rescind %s with no successor? It is tagged %q and its approval ends.", node.Loc, supersededTag)); err != nil {
				return err
			}

			rev, id := node.Revision, node.Id
			tags := rescindTags(node.Tags)
			abstract := plan.abstractPrefixAndText
			in := gen.UpdateNodeInput{Id: &id, ExpectedRevision: &rev, Tags: tags, Content: &newBody, Abstract: &abstract}
			resp, werr := gen.UpdateSpecNode(cmd.Context(), client, &in)
			if werr != nil {
				switch {
				case api.HasErrorCode(werr, "NODE_WRITE_CONFLICT"):
					return exitcode.Newf(exitcode.Conflict,
						"%s changed since revision %d, which this rescind was based on — nothing was written by this command, though the server may have recorded out-of-band drift as a revision; rerun to start from a fresh read",
						node.Loc, rev)
				case unsupportedGuard(werr):
					return exitcode.Newf(exitcode.Usage,
						"this server does not support guarded saves (UpdateNodeInput.expectedRevision, hadron-server#1352), so %s cannot be rescinded without risking a silent overwrite of a concurrent change — nothing was written",
						node.Loc)
				case exitcode.FromError(api.MapError(werr)) == exitcode.Unavailable:
					// No answer: it may still have committed. Re-read; the tag AND the prefix prove it.
					result.Retired = rescindLanded(cmd, client, node.Id)
					if result.Retired != nil && *result.Retired {
						return output.Write(f.IOStreams, f.JSON, result, render)
					}
					_ = output.Write(f.IOStreams, f.JSON, result, render)
					return exitcode.Newf(exitcode.Error,
						"the rescind of %s got no answer (%v) and re-reading it did not confirm it, so whether it was rescinded is unknown; check `hadron spec get %s -m %s` for the %q tag and a %s/%s abstract prefix, then rerun",
						node.Loc, api.MapError(werr), node.Loc, cmdutil.CanonicalMemoryRef(memURN), supersededTag, rescindedWord, withdrawnWord)
				default:
					return api.MapError(werr)
				}
			}
			if resp == nil || resp.UpdateSpecNode == nil {
				return exitcode.Newf(exitcode.Error, "updateSpecNode returned no node for %s — the write can't be confirmed; check with `hadron spec get %s -m %s`",
					node.Loc, node.Loc, cmdutil.CanonicalMemoryRef(memURN))
			}
			result.Retired = boolRef(true)
			if werr := output.Write(f.IOStreams, f.JSON, result, render); werr != nil {
				// The write LANDED: do not let a failed stdout read as a failed rescind (or a clean one).
				return exitcode.Newf(exitcode.Error, "%s was rescinded (one guarded update, revision %d), but its result could not be written to the output: %v; check `hadron spec get %s -m %s`",
					node.Loc, node.Revision, werr, node.Loc, cmdutil.CanonicalMemoryRef(memURN))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to the memory set by hadron spec use, then the active memory)")
	cmd.Flags().StringVar(&reason, "reason", "", "why it is retired, and the ruling for an in-force Decided spec (required)")
	cmd.Flags().StringVar(&summary, "summary", "", "one line of what the spec used to say, kept in the abstract (required); \"-\" reads stdin")
	cmd.Flags().StringVar(&summaryFile, "summary-file", "", "read --summary from this file")
	cmd.Flags().BoolVar(&withdrawn, "withdrawn", false, "a draft that never took effect: \"Withdrawn, never in force\" instead of \"Rescinded\"")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "read only: print the planned tag, banner, abstract, node id and revision (exit 8 if you cannot write to the memory, as the real run)")
	return cmd
}

type rescindText struct {
	banner                string // the body banner, one blockquote line
	abstractPrefixAndText string // the whole new abstract
}

func rescindPlan(date, reason, summary string, withdrawn bool) rescindText {
	word, banner := rescindedWord, fmt.Sprintf("> **⚠️ Rescinded** (%s, %s), no successor. **Do not apply.** Retained as a tombstone; the citation is never renumbered.", date, reason)
	if withdrawn {
		word, banner = withdrawnWord, fmt.Sprintf("> **⚠️ Withdrawn, never in force** (%s, %s). **Do not apply.** Retained as a tombstone; the citation is never renumbered.", date, reason)
	}
	return rescindText{
		banner:                banner,
		abstractPrefixAndText: fmt.Sprintf("%s (%s); do not apply. %s Historical: %s", word, date, reason, summary),
	}
}

// oneLine collapses internal whitespace/newlines so the abstract stays one paragraph.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// storedWording is the stored abstract's own statement ("RESCINDED (<date>); do not apply."),
// the part of it that states the retirement, for an already-rescinded spec.
func storedWording(abstract string) string {
	a := strings.TrimSpace(abstract)
	if i := strings.Index(a, "do not apply."); i >= 0 {
		return a[:i+len("do not apply.")]
	}
	return a
}

// isRescindedAbstract reports whether an abstract already carries the rescind prefix.
func isRescindedAbstract(a *string) bool {
	if a == nil {
		return false
	}
	s := strings.TrimSpace(*a)
	return strings.HasPrefix(s, rescindedWord+" (") || strings.HasPrefix(s, withdrawnWord+" (")
}

// withBanner puts the banner after the body's first H1 line, or at the top without one.
func withBanner(body, banner string) string {
	lines := strings.SplitN(body, "\n", 2)
	if strings.HasPrefix(lines[0], "# ") {
		rest := ""
		if len(lines) == 2 {
			rest = strings.TrimLeft(lines[1], "\n")
		}
		return lines[0] + "\n\n" + banner + "\n\n" + rest
	}
	return banner + "\n\n" + body
}

// rescindTags is the spec's own tags plus superseded, never replacing what is there.
func rescindTags(tags []string) []string {
	out := append([]string{}, tags...)
	if !hasTag(out, supersededTag) {
		out = append(out, supersededTag)
	}
	return out
}

// rescindLanded re-reads the node after a write with no answer. It is true only when
// the tag AND the RESCINDED/WITHDRAWN prefix are both seen; false/unknown never claim it.
func rescindLanded(cmd *cobra.Command, client graphql.Client, nodeID string) *bool {
	resp, err := gen.GetSpecNodeForEdit(cmd.Context(), client, nodeID)
	if err != nil || resp.Node == nil {
		return nil
	}
	if hasTag(resp.Node.Tags, supersededTag) && isRescindedAbstract(resp.Node.Abstract) {
		return boolRef(true)
	}
	return nil
}

func renderRescind(w io.Writer, r rescindResultDTO, p rescindText) error {
	var err error
	say := func(format string, a ...any) {
		if err == nil {
			_, err = fmt.Fprintf(w, format, a...)
		}
	}
	switch {
	case r.AlreadyRescinded:
		say("already rescinded: %s (nothing written)\n", r.Loc)
	case r.DryRun:
		say("would rescind %s (no successor)\n  guarded on node %s at revision %d\n  tag: %s\n  banner: %s\n  abstract: %s\n", r.Loc, r.NodeID, r.Revision, supersededTag, p.banner, p.abstractPrefixAndText)
		say("dry run: nothing was written. Applying rescinds the spec and ends its approval; re-approval or re-mint is a separate step.\n")
	case r.Retired != nil && *r.Retired:
		say("✓ rescinded %s (no successor)\n  tagged %q; banner and abstract set in one guarded update (revision %d)\n  its approval has ended; re-approval or re-mint is a separate step\n", r.Loc, supersededTag, r.Revision)
	default:
		say("✗ not confirmed: %s\n", r.Loc)
	}
	return err
}
