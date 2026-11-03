package node

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type nodeMintFindingDTO struct {
	Kind      string  `json:"kind"`
	Rule      *string `json:"rule"`
	Loc       string  `json:"loc"`
	TargetLoc *string `json:"targetLoc"`
	Message   string  `json:"message"`
}

type nodeMintReportDTO struct {
	Memory              string                         `json:"memory"`
	DryRun              bool                           `json:"dryRun"`
	Minted              bool                           `json:"minted"`
	MintedCount         int                            `json:"mintedCount"`
	MintLocs            []string                       `json:"mintLocs"`
	Blocked             bool                           `json:"blocked"`
	Blockers            []nodeMintFindingDTO           `json:"blockers"`
	StaleAbstracts      []nodeMintFindingDTO           `json:"staleAbstracts"`
	StaleAbstractsBlock bool                           `json:"staleAbstractsBlock"`
	OpenQuestions       []nodeMintOpenQuestionGroupDTO `json:"openQuestions"`
}

type nodeMintOpenQuestionGroupDTO struct {
	Decider   *string                   `json:"decider"`
	Questions []nodeMintOpenQuestionDTO `json:"questions"`
}

type nodeMintOpenQuestionDTO struct {
	Loc      string `json:"loc"`
	Question string `json:"question"`
}

func nodeMintReport(memory string, r *gen.MintMemoryNodesMintMemoryNodesSpecCorpusMintReport) nodeMintReportDTO {
	d := nodeMintReportDTO{Memory: memory, DryRun: r.DryRun, Minted: r.Minted,
		MintedCount: r.MintedCount, MintLocs: append([]string{}, r.MintLocs...),
		Blockers: []nodeMintFindingDTO{}, StaleAbstracts: []nodeMintFindingDTO{},
		StaleAbstractsBlock: r.StaleAbstractsBlock, OpenQuestions: []nodeMintOpenQuestionGroupDTO{}}
	for _, b := range r.Blockers {
		if b != nil {
			d.Blockers = append(d.Blockers, nodeMintFindingDTO{Kind: string(b.Kind), Rule: b.Rule, Loc: b.Loc, TargetLoc: b.TargetLoc, Message: b.Message})
		}
	}
	for _, b := range r.StaleAbstracts {
		if b != nil {
			d.StaleAbstracts = append(d.StaleAbstracts, nodeMintFindingDTO{Kind: string(b.Kind), Rule: b.Rule, Loc: b.Loc, TargetLoc: b.TargetLoc, Message: b.Message})
		}
	}
	for _, g := range r.OpenQuestions {
		if g == nil {
			continue
		}
		group := nodeMintOpenQuestionGroupDTO{Decider: g.Decider, Questions: []nodeMintOpenQuestionDTO{}}
		for _, q := range g.Questions {
			if q != nil {
				group.Questions = append(group.Questions, nodeMintOpenQuestionDTO{Loc: q.Loc, Question: q.Question})
			}
		}
		d.OpenQuestions = append(d.OpenQuestions, group)
	}
	d.Blocked = len(d.Blockers) > 0 || (d.StaleAbstractsBlock && len(d.StaleAbstracts) > 0)
	return d
}

func newCmdMint(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use: "mint", Short: "Record a memory's approved notes as permanent citations",
		Long: `Mark a memory's approved, not-yet-minted notes as minted: their loc becomes
a citation that other notes and repos can rely on, the way a law code cites a
section. It is used for spec memories (hadron spec mint is the same call); a
note is approved with node approve. A writer of the memory can mint.

The command checks first and prints a report: the nodes it would mint, and
anything that blocks it (a current version that is not approved or whose
approval no longer matches, a placeholder, a reference to a note that does
not exist or is not minted, or a lint error). Any blocker refuses the whole
mint with exit 5, and nothing is minted. Otherwise it asks before minting
(--yes skips the question, and is required when not at a terminal). --dry-run
prints the report only. If someone is writing to the memory at that moment,
the mint is refused with exit 5; try again.

Today a mint records that each node was minted; the server does not yet
enforce that a minted citation stays permanent, so treat it as permanent
yourself.`,
		Example: "  hadron node mint -m hrn:mem:hadronmemory.com:dev --dry-run\n  hadron node mint -m hrn:mem:hadronmemory.com:dev --yes --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(memory) == "" {
				return exitcode.Newf(exitcode.Usage, "-m/--memory is required for node mint")
			}
			memory = cmdutil.CanonicalMemoryRef(memory)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			call := func(dry bool) (nodeMintReportDTO, error) {
				resp, err := gen.MintMemoryNodes(cmd.Context(), client, memory, dry)
				if err != nil {
					return nodeMintReportDTO{}, api.MapError(err)
				}
				if resp.MintMemoryNodes == nil {
					return nodeMintReportDTO{}, exitcode.Newf(exitcode.Error, "server returned no mint report")
				}
				return nodeMintReport(memory, resp.MintMemoryNodes), nil
			}
			write := func(r nodeMintReportDTO) error {
				return output.Write(f.IOStreams, f.JSON, r, func(w io.Writer) error { return renderNodeMint(w, r) })
			}
			check, err := call(true)
			if err != nil {
				return err
			}
			if dryRun || check.Blocked {
				if err := write(check); err != nil {
					return err
				}
				if check.Blocked {
					return exitcode.Silent(exitcode.Conflict)
				}
				return nil
			}
			if !f.JSON || !yes {
				w := f.IOStreams.Out
				if f.JSON {
					w = f.IOStreams.ErrOut
				}
				if err := renderNodeMint(w, check); err != nil {
					return err
				}
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Mint %d node(s) in %s?", len(check.MintLocs), memory)); err != nil {
				return err
			}
			result, err := call(false)
			if err != nil {
				return err
			}
			return write(result)
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report eligible nodes and blockers without minting")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required in non-interactive use)")
	return cmd
}

func renderNodeMint(w io.Writer, r nodeMintReportDTO) error {
	fmt.Fprintf(w, "Mint report — %s\n  eligible: %d", r.Memory, len(r.MintLocs))
	if len(r.MintLocs) > 0 {
		fmt.Fprintf(w, " (%s)", strings.Join(r.MintLocs, ", "))
	}
	fmt.Fprintln(w)
	kinds := make([]string, 0, len(r.Blockers))
	for _, b := range r.Blockers {
		fmt.Fprintf(w, "  blocked %s: %s — %s\n", b.Loc, b.Kind, b.Message)
		kinds = append(kinds, b.Kind)
	}
	cmdutil.WriteMintApproveHint(w, r.Memory, kinds...)
	for _, b := range r.StaleAbstracts {
		fmt.Fprintf(w, "  stale abstract %s: %s\n", b.Loc, b.Message)
	}
	for _, g := range r.OpenQuestions {
		for _, q := range g.Questions {
			fmt.Fprintf(w, "  open question %s: %s\n", q.Loc, q.Question)
		}
	}
	if r.Minted {
		fmt.Fprintf(w, "✓ minted %d node(s)\n", r.MintedCount)
	}
	return nil
}
