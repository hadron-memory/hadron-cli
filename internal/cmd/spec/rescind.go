package spec

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#809 (split A, Vera 2026-10-31): retire a spec WITHOUT a successor.
//
// `supersede` always means a successor exists; `rescind` always means none. The
// citation stays and is never renumbered. cli#1736: the retirement is ONE
// retireSpec request (retire_door.go); the server tags the spec, composes its
// banner and abstract, and refuses what may not be retired.

const (
	rescindedWord = "RESCINDED"
	withdrawnWord = "WITHDRAWN"
)

// rescindResultDTO is the stable --json shape.
type rescindResultDTO struct {
	Loc      string `json:"loc"`
	MemoryID string `json:"memoryId"`
	NodeID   string `json:"nodeId"`
	// Revision is the revision the request was (or, with --dry-run, would be)
	// guarded on: the one this command read.
	Revision int `json:"revision"`
	// Retired is true after a success and false for a dry run. After a refused or
	// unanswered request it is what the re-read showed (see Current), null when
	// that is unknown.
	Retired *bool `json:"retired"`
	// Wording and Abstract are the server's, from its answer to a successful
	// retirement; null otherwise, because only the server composes them.
	Wording   *string `json:"wording"`
	Abstract  *string `json:"abstract"`
	Withdrawn bool    `json:"withdrawn"`
	DryRun    bool    `json:"dryRun"`
	// AlreadyRescinded is always false: the server refuses an already retired
	// spec (exit 5). The key stays for readers of the earlier shape.
	AlreadyRescinded bool              `json:"alreadyRescinded"`
	Tags             []string          `json:"tags"`
	Form             string            `json:"form"`
	Reason           *string           `json:"reason"`
	Summary          *string           `json:"summary"`
	Outcome          string            `json:"outcome"`
	Current          *retireCurrentDTO `json:"current"`
	// Note says, on a dry run, that the server composes the text; null otherwise.
	Note *string `json:"note"`
}

func newCmdRescind(f *cmdutil.Factory) *cobra.Command {
	var memory, reason, summary, summaryFile string
	var withdrawn, yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "rescind <citation>",
		Short: "Retire a spec with no successor (rescinded, or withdrawn never in force)",
		Long: `Retire a spec that has no successor: a rule that was in force and is withdrawn
(Rescinded), or a draft that never took effect (--withdrawn).

The spec stays at its citation, which is never renumbered or deleted. The command
reads the spec for its revision, then sends one retireSpec request. In one
transaction the server tags it "superseded", writes the banner and the abstract,
and ends its approval. The server composes that text; the CLI sends only --reason
and --summary. No edge is written: a spec retired in favour of another one uses
` + "`hadron spec supersede`" + `. Nothing is allocated, minted or re-linked, and
the specs that cite this one are not rewritten (that is a corpus task).

--reason and --summary are required; there is no inferred "in force" status, so say
--withdrawn yourself for a draft. Rescinding an in-force Decided spec needs a
recorded ruling: name it in --reason.

The server refuses a stale revision, a spec that is already retired and a spec
that already has a successor with exit 5, and writes nothing. A rerun on a retired
spec is exit 5, never a success (earlier releases returned exit 0). A spec linked to a
successor but not retired also exits 5: it needs manual review, and the CLI makes
no automatic repair. After such a refusal, or a request that got no usable answer
(its outcome is unknown, and it is not resent), the command reads the spec once and
reports what it currently shows, which is not proof of what this request did.
An unknown outcome exits 7 (no answer) or 1: check ` + "`hadron spec get`" + ` first and
rerun only if the spec is not retired. Success prints the server's statement and
the tags; --json adds the request's outcome and the re-read.

A server without retireSpec is refused; there is no fallback to separate writes.
--dry-run reads only and prints what would be sent: the spec, form, reason, summary
and revision.`,
		Example: `  hadron spec rescind msg:010:02 -m hrn:mem:micromentor.org:platform-specs --reason "ruling 0099 A" --summary "Placeholders were once confined to drafts." --yes
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

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}
			node, err := fetchForRetire(cmd, client, memURN, loc)
			if err != nil {
				return err
			}
			result := rescindResultDTO{
				Loc: node.Loc, MemoryID: node.MemoryId, NodeID: node.Id, Revision: node.Revision,
				Retired: boolRef(false), Withdrawn: withdrawn, DryRun: dryRun, Tags: tagsOrEmpty(node.Tags),
				Form: string(gen.RetireSpecFormRescind), Reason: &reason, Summary: &body, Outcome: retireOutcomePlanned,
			}
			render := func(w io.Writer) error { return renderRescind(w, result) }
			if dryRun {
				result.Note = noteRef(dryRunNote)
				if err := cmdutil.RequireWriteAccessForDryRun(cmd, client, memURN); err != nil {
					return err
				}
				return output.Write(f.IOStreams, f.JSON, result, render)
			}
			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Rescind %s with no successor? It is tagged %q and its approval ends.", node.Loc, supersededTag)); err != nil {
				return err
			}

			// The claim is the operator's --authored-with pair (cli#2018), nil without it.
			in := &gen.RetireSpecInput{
				AuthoredWith: cmdutil.AuthoredWith(cmd.Context()),
				Ref:          node.Id, Form: gen.RetireSpecFormRescind, Withdrawn: &withdrawn,
				ExpectedRevision: node.Revision, Reason: &reason, Summary: &body,
			}
			retired, failure := sendRetire(cmd, client, in)
			if failure != nil {
				report := reportRetireFailure(cmd, client, in, failure, "")
				result.Outcome, result.Current, result.Retired = report.outcome, report.current, nil
				if report.current != nil {
					result.Retired = report.current.Retired
					if werr := output.Write(f.IOStreams, f.JSON, result, render); werr != nil {
						return unprintedFailure(report.err, werr)
					}
				}
				return report.err
			}
			result.Retired, result.Outcome = boolRef(true), retireOutcomeRetired
			result.Tags = tagsOrEmpty(retired.Node.Tags)
			result.Abstract = retired.Node.Abstract
			if retired.Node.Abstract != nil {
				w := storedWording(*retired.Node.Abstract)
				result.Wording = &w
			}
			if werr := output.Write(f.IOStreams, f.JSON, result, render); werr != nil {
				// The server answered success: a failed stdout must not read as a failed rescind (or a clean one).
				return exitcode.Newf(exitcode.Error, "the spec (the citation given is not shown) was rescinded (the server answered at revision %d), but its result could not be written to the output: %v; check `hadron spec get <citation> -m <memory>`",
					retired.Node.Revision, werr)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to the memory set by hadron spec use, then the active memory)")
	cmd.Flags().StringVar(&reason, "reason", "", "why it is retired, and the ruling for an in-force Decided spec (required)")
	cmd.Flags().StringVar(&summary, "summary", "", "one line of what the spec used to say, kept in the abstract (required); \"-\" reads stdin")
	cmd.Flags().StringVar(&summaryFile, "summary-file", "", "read --summary from this file")
	cmd.Flags().BoolVar(&withdrawn, "withdrawn", false, "a draft that never took effect, instead of a rule that was in force")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "read only: print what would be sent (exit 8 if you cannot write to the memory)")
	return cmd
}

// storedWording is the abstract's own statement ("RESCINDED (<date>); do not apply."),
// the part of the server's text that states the retirement.
func storedWording(abstract string) string {
	a := strings.TrimSpace(abstract)
	if i := strings.Index(a, "do not apply."); i >= 0 {
		return a[:i+len("do not apply.")]
	}
	return a
}

func renderRescind(w io.Writer, r rescindResultDTO) error {
	say, failed := sayer(w)
	switch r.Outcome {
	case retireOutcomePlanned:
		say("would rescind %s (no successor, withdrawn: %t)\n", r.Loc, r.Withdrawn)
		renderInputs(say, gen.RetireSpecFormRescind, r.Loc, r.NodeID, r.Revision, r.Reason, r.Summary)
	case retireOutcomeRetired:
		say("✓ rescinded %s (no successor)\n", r.Loc)
		if r.Wording != nil {
			say("  %s\n", output.SafeLine(*r.Wording, 200))
		}
		say("  tags: %v\n  its approval has ended; re-approval or re-mint is a separate step\n", r.Tags)
	case retireOutcomeRefused:
		say("✗ retirement refused: %s\n", r.Loc)
	default:
		say("? retirement outcome unknown: %s\n", r.Loc)
	}
	if r.Current != nil {
		say("  %s\n", r.Current.Statement)
	}
	return failed()
}
