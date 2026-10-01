package spec

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Draft spec corpora and minting (hadron-server#1447, cli#777).
//
// A corpus created with `memory set --draft-corpus` is a DRAFT: its citations
// are not yet permanent. These commands are the draft's tools — reserve a
// citation as a placeholder, renumber a spec and have its URN references
// rewritten, read what refers to a spec — and `spec mint`, which ends the
// draft: one step for the whole corpus, one-way. Every one of them except mint
// works only on a draft; the server refuses a minted memory with
// SPEC_CORPUS_NOT_DRAFT (exit 5).

const memoryFlagHelp = "memory ID or fully-qualified URN (defaults to the memory set by hadron spec use, then the active memory)"

// referenceDTO is one reference to a spec in a draft corpus (the --json shape
// shared by `spec backlinks` and `spec unresolved`).
type referenceDTO struct {
	// Kind is URN (a node URN in the corpus text, inside URLs too) or EDGE
	// (a real edge from a corpus node).
	Kind string `json:"kind"`
	// Field is the text field the URN was found in; "edge" for an edge,
	// "pendingEdge" for an edge whose target is not a node yet.
	Field string `json:"field"`
	// Reason is MISSING or PLACEHOLDER; set only by `spec unresolved`.
	Reason       string `json:"reason,omitempty"`
	SourceLoc    string `json:"sourceLoc"`
	SourceNodeID string `json:"sourceNodeId"`
	TargetLoc    string `json:"targetLoc"`
	// Text is the URN as written (kind URN) or the edge's relationship name.
	Text string `json:"text,omitempty"`
}

type specReference interface {
	GetKind() gen.SpecReferenceKind
	GetField() string
	GetSourceLoc() string
	GetSourceNodeId() string
	GetTargetLoc() string
	GetText() *string
}

func referenceFrom(r specReference, reason *gen.SpecReferenceProblem) referenceDTO {
	d := referenceDTO{
		Kind: string(r.GetKind()), Field: r.GetField(),
		SourceLoc: r.GetSourceLoc(), SourceNodeID: r.GetSourceNodeId(), TargetLoc: r.GetTargetLoc(),
	}
	if t := r.GetText(); t != nil {
		d.Text = *t
	}
	if reason != nil {
		d.Reason = string(*reason)
	}
	return d
}

func renderReferences(w io.Writer, refs []referenceDTO, withReason bool) error {
	cols := []string{"FROM", "KIND", "FIELD", "TO", "TEXT"}
	if withReason {
		cols = []string{"FROM", "KIND", "FIELD", "TO", "PROBLEM", "TEXT"}
	}
	t := output.NewTable(w, cols...)
	for _, r := range refs {
		if withReason {
			t.Row(r.SourceLoc, r.Kind, r.Field, r.TargetLoc, r.Reason, r.Text)
		} else {
			t.Row(r.SourceLoc, r.Kind, r.Field, r.TargetLoc, r.Text)
		}
	}
	return t.Flush()
}

// ── spec reserve ─────────────────────────────────────────────────────────

type reserveDTO struct {
	Memory      string `json:"memory"`
	ID          string `json:"id"`
	Loc         string `json:"loc"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Placeholder bool   `json:"placeholder"`
}

func newCmdReserve(f *cmdutil.Factory) *cobra.Command {
	var memory, name string
	cmd := &cobra.Command{
		Use:   "reserve <citation>",
		Short: "Reserve a citation in a draft corpus as a placeholder spec",
		Long: `Reserve a citation in a DRAFT spec corpus: the server creates a placeholder
spec at <citation>, marked as a placeholder by the server (not by an empty
body). Other specs can link to it now, so ` + "`spec backlinks`" + ` sees those links,
and the placeholders still left are the corpus's to-do list.

Writing the spec — ` + "`spec edit <citation>`" + ` with real content — turns the
placeholder into a real spec at the same citation. ` + "`spec mint`" + ` refuses while
any placeholder remains.

Only a draft corpus can reserve (a minted one refuses, exit 5: its citations
are permanent once written, so create the spec directly with ` + "`spec new`" + `).
A citation that already holds a live node is refused too (exit 5).`,
		Example: `  hadron spec reserve pas:010:04 -m hrn:mem:micromentor.org:specs-draft
  hadron spec reserve pas:010:04 --name "Vacation mode can end by itself" --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := validateSpecLoc(args[0])
			if err != nil {
				return err
			}
			var nameArg *string
			if cmd.Flags().Changed("name") {
				n := strings.TrimSpace(name)
				if n == "" {
					return exitcode.Newf(exitcode.Usage, "--name is blank; omit it to let the server name the placeholder")
				}
				nameArg = &n
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, memURN, err := specMemoryID(f, cmd, client, memory)
			if err != nil {
				return err
			}
			resp, err := gen.ReserveSpecCitation(cmd.Context(), client, memID, loc, nameArg)
			if err != nil {
				return api.MapError(err)
			}
			n := resp.ReserveSpecCitation
			if n == nil {
				return exitcode.Newf(exitcode.Error, "server returned no node for the reserved citation")
			}
			dto := reserveDTO{Memory: memURN, ID: n.Id, Loc: n.Loc, Name: n.Name, Placeholder: n.IsPlaceholder}
			if n.Role != nil {
				dto.Role = *n.Role
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ reserved %s as a placeholder (%s)\n  write it with `hadron spec edit %s`\n", dto.Loc, dto.Name, dto.Loc)
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", memoryFlagHelp)
	cmd.Flags().StringVar(&name, "name", "", "the placeholder's name (omit to let the server name it)")
	return cmd
}

// ── spec backlinks ───────────────────────────────────────────────────────

type backlinksDTO struct {
	Memory     string         `json:"memory"`
	Loc        string         `json:"loc"`
	References []referenceDTO `json:"references"`
}

func newCmdBacklinks(f *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:   "backlinks <citation>",
		Short: "List what refers to a spec in a draft corpus",
		Long: `List what refers to the spec at <citation> in a DRAFT spec corpus: every node
URN in the corpus text that names it — in a name, description, abstract or
body, inside a URL too — and every real edge from a corpus node to it.

The citation is matched exactly; references to its descendants are not
included. A citation written only as bare text (a link label like
"[pas:010:01]") is not a reference and is not listed.

Only a draft corpus answers (a minted one refuses, exit 5).`,
		Example: `  hadron spec backlinks pas:010:01 -m hrn:mem:micromentor.org:specs-draft
  hadron spec backlinks pas:010:01 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := validateSpecLoc(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, memURN, err := specMemoryID(f, cmd, client, memory)
			if err != nil {
				return err
			}
			resp, err := gen.SpecBacklinks(cmd.Context(), client, memID, loc)
			if err != nil {
				return api.MapError(err)
			}
			dto := backlinksDTO{Memory: memURN, Loc: loc, References: []referenceDTO{}}
			for _, r := range resp.SpecBacklinks {
				if r != nil {
					dto.References = append(dto.References, referenceFrom(r, nil))
				}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.References) == 0 {
					_, err := fmt.Fprintf(w, "nothing in the corpus refers to %s\n", loc)
					return err
				}
				return renderReferences(w, dto.References, false)
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", memoryFlagHelp)
	return cmd
}

// ── spec unresolved ──────────────────────────────────────────────────────

type unresolvedDTO struct {
	Memory     string         `json:"memory"`
	References []referenceDTO `json:"references"`
}

func newCmdUnresolved(f *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:   "unresolved",
		Short: "List references in a draft corpus that don't reach a written spec",
		Long: `List the references in a DRAFT spec corpus that don't reach a written spec:
a node URN or edge naming a citation with no spec (MISSING), or one that is
only a placeholder (PLACEHOLDER), and pending edges whose target isn't a
written node yet.

These are the corpus's open references. ` + "`spec mint`" + ` refuses while any remain;
this lists them on their own. It exits 0 either way — listing them is not a
failure; ` + "`spec mint --dry-run`" + ` is the gate.

Only a draft corpus answers (a minted one refuses, exit 5).`,
		Example: `  hadron spec unresolved -m hrn:mem:micromentor.org:specs-draft
  hadron spec unresolved --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, memURN, err := specMemoryID(f, cmd, client, memory)
			if err != nil {
				return err
			}
			resp, err := gen.SpecUnresolvedReferences(cmd.Context(), client, memID)
			if err != nil {
				return api.MapError(err)
			}
			dto := unresolvedDTO{Memory: memURN, References: []referenceDTO{}}
			for _, r := range resp.SpecUnresolvedReferences {
				if r != nil {
					dto.References = append(dto.References, referenceFrom(r, r.Reason))
				}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.References) == 0 {
					_, err := fmt.Fprintln(w, "✓ every reference reaches a written spec")
					return err
				}
				return renderReferences(w, dto.References, true)
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", memoryFlagHelp)
	return cmd
}

// ── spec renumber ────────────────────────────────────────────────────────

type renumberMoveDTO struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type renumberRewriteDTO struct {
	NodeID string   `json:"nodeId"`
	Loc    string   `json:"loc"`
	Status string   `json:"status"`
	Fields []string `json:"fields"`
	Count  int      `json:"count"`
	Error  string   `json:"error,omitempty"`
}

type textCitationDTO struct {
	SourceNodeID string `json:"sourceNodeId"`
	SourceLoc    string `json:"sourceLoc"`
	Field        string `json:"field"`
	Citation     string `json:"citation"`
	Excerpt      string `json:"excerpt"`
}

type renumberDTO struct {
	Memory   string               `json:"memory"`
	From     string               `json:"from"`
	To       string               `json:"to"`
	DryRun   bool                 `json:"dryRun"`
	Moved    []renumberMoveDTO    `json:"moved"`
	Rewrites []renumberRewriteDTO `json:"rewrites"`
	// TextCitations are bare-text copies of a moved citation (a link label,
	// a node name, prose). Reported, never rewritten.
	TextCitations []textCitationDTO `json:"textCitations"`
	// Failed counts rewrites the server reported FAILED: the move stands, and
	// those nodes still name the old citation.
	Failed int `json:"failed"`
}

func newCmdRenumber(f *cmdutil.Factory) *cobra.Command {
	var (
		memory string
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "renumber <from-citation> <to-citation>",
		Short: "Renumber a spec in a draft corpus and rewrite the references to it",
		Long: `Renumber a spec in a DRAFT spec corpus: move the spec at <from-citation>,
and its subtree, to <to-citation>, then rewrite every node URN in the corpus
that names one of the moved specs. Each rewrite is a normal edit with revision
history. A draft records no URN alias for the old citation — nothing outside a
draft may cite it yet.

A citation that appears only as TEXT — a link label like "[pas:010:01]", a
node's name, prose — can't be told apart from other characters, so it is
REPORTED under "text citations", never rewritten. Review those yourself.

The move is atomic. Each rewrite is a separate guarded edit: one that is
refused, or keeps conflicting with a concurrent edit, is reported FAILED
without undoing the move, and the command exits 1 so a script doesn't read a
clean success. --dry-run shows the plan and writes nothing.

Only a draft corpus can renumber (a minted one refuses, exit 5: its
citations are permanent — supersede a spec instead).`,
		Example: `  hadron spec renumber pas:010:04 pas:010:02 -m hrn:mem:micromentor.org:specs-draft --dry-run
  hadron spec renumber pas:010:04 pas:010:02 --json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			from, err := validateSpecLoc(args[0])
			if err != nil {
				return err
			}
			to, err := validateSpecLoc(args[1])
			if err != nil {
				return err
			}
			if from == to {
				return exitcode.Newf(exitcode.Usage, "<from-citation> and <to-citation> are both %s; nothing to renumber", from)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, memURN, err := specMemoryID(f, cmd, client, memory)
			if err != nil {
				return err
			}
			resp, err := gen.RenumberSpec(cmd.Context(), client, memID, from, to, dryRun)
			if err != nil {
				return api.MapError(err)
			}
			r := resp.RenumberSpec
			if r == nil {
				return exitcode.Newf(exitcode.Error, "server returned no renumber result")
			}
			dto := renumberDTO{
				Memory: memURN, From: from, To: to, DryRun: r.DryRun,
				Moved: []renumberMoveDTO{}, Rewrites: []renumberRewriteDTO{}, TextCitations: []textCitationDTO{},
			}
			for _, m := range r.Moved {
				if m != nil {
					dto.Moved = append(dto.Moved, renumberMoveDTO{From: m.FromLoc, To: m.ToLoc})
				}
			}
			for _, rw := range r.Rewrites {
				if rw == nil {
					continue
				}
				d := renumberRewriteDTO{NodeID: rw.NodeId, Loc: rw.Loc, Status: string(rw.Status), Fields: []string{}, Count: rw.Count}
				d.Fields = append(d.Fields, rw.Fields...)
				if rw.Error != nil {
					d.Error = *rw.Error
				}
				if rw.Status == gen.SpecRewriteStatusFailed {
					dto.Failed++
				}
				dto.Rewrites = append(dto.Rewrites, d)
			}
			for _, tc := range r.TextCitations {
				if tc != nil {
					dto.TextCitations = append(dto.TextCitations, textCitationDTO{
						SourceNodeID: tc.SourceNodeId, SourceLoc: tc.SourceLoc, Field: tc.Field,
						Citation: tc.Citation, Excerpt: tc.Excerpt,
					})
				}
			}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderRenumber(w, dto)
			}); err != nil {
				return err
			}
			if dto.Failed > 0 {
				return exitcode.Newf(exitcode.Error,
					"%s moved to %s, but %d reference rewrite(s) FAILED — those nodes still name the old citation; see the rewrites above",
					from, to, dto.Failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", memoryFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the plan and write nothing")
	return cmd
}

func renderRenumber(w io.Writer, d renumberDTO) error {
	verb := "renumbered"
	if d.DryRun {
		verb = "would renumber (dry run, nothing written)"
	}
	fmt.Fprintf(w, "%s %s → %s\n", verb, d.From, d.To)
	for _, m := range d.Moved {
		fmt.Fprintf(w, "  move  %s → %s\n", m.From, m.To)
	}
	if len(d.Rewrites) == 0 {
		fmt.Fprintln(w, "  no URN references to rewrite")
	} else {
		fmt.Fprintln(w, "reference rewrites:")
		t := output.NewTable(w, "NODE", "STATUS", "COUNT", "FIELDS", "ERROR")
		for _, rw := range d.Rewrites {
			t.Row(rw.Loc, rw.Status, fmt.Sprint(rw.Count), strings.Join(rw.Fields, ","), rw.Error)
		}
		if err := t.Flush(); err != nil {
			return err
		}
	}
	if len(d.TextCitations) > 0 {
		fmt.Fprintln(w, "text citations (reported, NOT rewritten — review these):")
		t := output.NewTable(w, "NODE", "FIELD", "CITATION", "EXCERPT")
		for _, tc := range d.TextCitations {
			t.Row(tc.SourceLoc, tc.Field, tc.Citation, tc.Excerpt)
		}
		return t.Flush()
	}
	return nil
}

// ── spec mint ────────────────────────────────────────────────────────────

type mintFindingDTO struct {
	Kind      string `json:"kind"`
	Rule      string `json:"rule,omitempty"`
	Loc       string `json:"loc"`
	TargetLoc string `json:"targetLoc,omitempty"`
	Message   string `json:"message"`
}

type openQuestionDTO struct {
	Loc      string `json:"loc"`
	Question string `json:"question"`
}

type openQuestionGroupDTO struct {
	// Decider is who decides; "" when no "<Name> decides." was written
	// (reported as unassigned).
	Decider   string            `json:"decider"`
	Questions []openQuestionDTO `json:"questions"`
}

type mintDTO struct {
	Memory string `json:"memory"`
	DryRun bool   `json:"dryRun"`
	// Minted is true only when this call minted the corpus.
	Minted bool `json:"minted"`
	// Blocked is true when a blocker remains, so the corpus can't be minted.
	Blocked  bool             `json:"blocked"`
	Blockers []mintFindingDTO `json:"blockers"`
	// StaleAbstracts block only when StaleAbstractsBlock is true.
	StaleAbstracts      []mintFindingDTO       `json:"staleAbstracts"`
	StaleAbstractsBlock bool                   `json:"staleAbstractsBlock"`
	OpenQuestions       []openQuestionGroupDTO `json:"openQuestions"`
}

func mintDTOFrom(memURN string, r *gen.MintSpecCorpusMintSpecCorpusSpecCorpusMintReport) mintDTO {
	d := mintDTO{
		Memory: memURN, DryRun: r.DryRun, Minted: r.Minted, StaleAbstractsBlock: r.StaleAbstractsBlock,
		Blockers: []mintFindingDTO{}, StaleAbstracts: []mintFindingDTO{}, OpenQuestions: []openQuestionGroupDTO{},
	}
	finding := func(f *gen.MintSpecCorpusMintSpecCorpusSpecCorpusMintReportBlockersSpecMintFinding) mintFindingDTO {
		m := mintFindingDTO{Kind: string(f.Kind), Loc: f.Loc, Message: f.Message}
		if f.Rule != nil {
			m.Rule = *f.Rule
		}
		if f.TargetLoc != nil {
			m.TargetLoc = *f.TargetLoc
		}
		return m
	}
	for _, b := range r.Blockers {
		if b != nil {
			d.Blockers = append(d.Blockers, finding(b))
		}
	}
	for _, s := range r.StaleAbstracts {
		if s != nil {
			d.StaleAbstracts = append(d.StaleAbstracts, finding((*gen.MintSpecCorpusMintSpecCorpusSpecCorpusMintReportBlockersSpecMintFinding)(s)))
		}
	}
	for _, g := range r.OpenQuestions {
		if g == nil {
			continue
		}
		grp := openQuestionGroupDTO{Questions: []openQuestionDTO{}}
		if g.Decider != nil {
			grp.Decider = *g.Decider
		}
		for _, q := range g.Questions {
			if q != nil {
				grp.Questions = append(grp.Questions, openQuestionDTO{Loc: q.Loc, Question: q.Question})
			}
		}
		d.OpenQuestions = append(d.OpenQuestions, grp)
	}
	d.Blocked = len(d.Blockers) > 0
	return d
}

func newCmdMint(f *cmdutil.Factory) *cobra.Command {
	var (
		memory string
		dryRun bool
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "mint",
		Short: "Mint a draft spec corpus: its citations become permanent",
		Long: `Mint a DRAFT spec corpus: one step for the whole corpus, and ONE-WAY. After
minting, today's rules apply: citations are permanent, a spec is replaced by
superseding it, and the corpus can never return to draft.

The command always checks first and prints the mint report:
  blockers         a placeholder still reserved, a reference to a spec that
                   doesn't exist or is only a placeholder, or a structural
                   lint error (nodetype-info, tag-spec, serialization-leak,
                   duplicate-loc). Any blocker refuses the mint.
  stale abstracts  an abstract written for an earlier body. Reported; they
                   block only once the server says so (staleAbstractsBlock).
  open questions   grouped by who decides — a bullet under an "Open
                   Questions" heading ending "<Name> decides.". Never block.

With blockers, nothing is minted and the command exits 5. Without them it
asks for confirmation on a terminal (non-interactively --yes is required),
then mints. --dry-run prints the report and stops, exiting 5 if blocked.

A write in flight to the corpus makes the server refuse the mint for the
moment (exit 5); retry. Only the corpus's owner or an org admin can mint.`,
		Example: `  hadron spec mint -m hrn:mem:micromentor.org:specs-draft --dry-run
  hadron spec mint -m hrn:mem:micromentor.org:specs-draft --yes --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A mint is irreversible, so an explicitly blank -m (an unset "$M"
			// in a script) is refused rather than falling back to the spec use /
			// active memory the way an omitted -m does.
			if cmd.Flags().Changed("memory") && strings.TrimSpace(memory) == "" {
				return exitcode.Newf(exitcode.Usage, "-m/--memory is blank; name the corpus to mint (omit -m to use the spec use / active memory). Nothing was minted")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, memURN, err := specMemoryID(f, cmd, client, memory)
			if err != nil {
				return err
			}
			// Always check first: a mint is irreversible, so the report is shown
			// before anything is asked, and a mint already known to be blocked is
			// never offered (review:confirm-prompt-tells-the-truth).
			check, err := gen.MintSpecCorpus(cmd.Context(), client, memID, true)
			if err != nil {
				return api.MapError(err)
			}
			if check.MintSpecCorpus == nil {
				return exitcode.Newf(exitcode.Error, "server returned no mint report")
			}
			report := mintDTOFrom(memURN, check.MintSpecCorpus)

			if dryRun || report.Blocked {
				if err := writeMint(f, report); err != nil {
					return err
				}
				if report.Blocked {
					return exitcode.Silent(exitcode.Conflict)
				}
				return nil
			}

			// Printed before the prompt so the caller confirms having seen it.
			// With --json, stdout carries only the final document, so the report
			// goes to stderr, where the prompt is — and only when a prompt
			// follows (--yes asks nothing).
			switch {
			case !f.JSON:
				if err := renderMint(f.IOStreams.Out, report); err != nil {
					return err
				}
			case !yes:
				if err := renderMint(f.IOStreams.ErrOut, report); err != nil {
					return err
				}
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf(
				"Mint %s? Its citations become permanent and it can never return to draft.", memURN)); err != nil {
				return err
			}
			resp, err := gen.MintSpecCorpus(cmd.Context(), client, memID, false)
			if err != nil {
				return api.MapError(err)
			}
			if resp.MintSpecCorpus == nil {
				return exitcode.Newf(exitcode.Error, "server returned no mint report")
			}
			minted := mintDTOFrom(memURN, resp.MintSpecCorpus)
			if f.JSON {
				return writeMint(f, minted)
			}
			_, err = fmt.Fprintf(f.IOStreams.Out, "✓ minted %s — its citations are now permanent\n", memURN)
			return err
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", memoryFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the mint report and mint nothing (exits 5 if blocked)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (required in non-interactive use)")
	return cmd
}

func writeMint(f *cmdutil.Factory, d mintDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		return renderMint(w, d)
	})
}

func renderMint(w io.Writer, d mintDTO) error {
	fmt.Fprintf(w, "Mint report — %s\n", d.Memory)
	if len(d.Blockers) == 0 {
		fmt.Fprintln(w, "  blockers: none")
	} else {
		fmt.Fprintf(w, "  blockers: %d\n", len(d.Blockers))
		t := output.NewTable(w, "LOC", "KIND", "RULE", "TARGET", "MESSAGE")
		for _, b := range d.Blockers {
			t.Row(b.Loc, b.Kind, b.Rule, b.TargetLoc, b.Message)
		}
		if err := t.Flush(); err != nil {
			return err
		}
	}
	block := "reported only"
	if d.StaleAbstractsBlock {
		block = "these block"
	}
	fmt.Fprintf(w, "  stale abstracts: %d (%s)\n", len(d.StaleAbstracts), block)
	for _, s := range d.StaleAbstracts {
		fmt.Fprintf(w, "    %s  %s\n", s.Loc, s.Message)
	}
	n := 0
	for _, g := range d.OpenQuestions {
		n += len(g.Questions)
	}
	fmt.Fprintf(w, "  open questions: %d (never block)\n", n)
	for _, g := range d.OpenQuestions {
		who := g.Decider
		if who == "" {
			who = "unassigned"
		}
		fmt.Fprintf(w, "    %s decides:\n", who)
		for _, q := range g.Questions {
			fmt.Fprintf(w, "      %s  %s\n", q.Loc, q.Question)
		}
	}
	switch {
	case d.Minted:
		fmt.Fprintln(w, "✓ minted")
	case d.Blocked:
		fmt.Fprintf(w, "✗ not mintable: %d blocker(s) remain\n", len(d.Blockers))
	case d.DryRun:
		fmt.Fprintln(w, "✓ mintable — run without --dry-run to mint")
	}
	return nil
}

// ── draft awareness for the read commands (cli#777 slice 2) ─────────────

// draftInfo is what lint/list/get need to know about a corpus's draft state.
type draftInfo struct {
	// Draft is true only when the server says DRAFT and supports the
	// placeholder scan. Minted corpora and older server slices take the
	// generic read path.
	Draft bool
	// Placeholders are the locs of reserved, unwritten specs (draft only).
	Placeholders map[string]bool
}

// loadDraftInfo reads the corpus state and, for a draft, its placeholders
// under prefix ("" = the whole corpus). One extra request for a minted corpus
// or an older server; the placeholder scan runs only for a draft.
func loadDraftInfo(ctx context.Context, client graphql.Client, memRef, prefix string) (draftInfo, error) {
	info := draftInfo{Placeholders: map[string]bool{}}
	st, err := gen.SpecCorpusState(ctx, client, memRef)
	switch {
	case err != nil && api.IsGraphQLValidationFor(err, "corpusState"):
		return info, nil
	case err != nil:
		return info, api.MapError(err)
	case st.Memory == nil || st.Memory.CorpusState != gen.CorpusStateDraft:
		return info, nil
	}
	info.Draft = true
	var prefixArg *string
	if prefix != "" {
		prefixArg = &prefix
	}
	filter := newNodeFilter(&memRef, prefixArg, nil)
	role := api.SpecNodeRole
	filter.Role = &role
	for offset := 0; ; offset += nodesPageSize {
		resp, err := gen.SpecPlaceholderScan(ctx, client, filter, gen.NodeSortLoc, nodesPageSize, offset)
		if err != nil {
			// #1451 exposed corpusState before #1452 added isPlaceholder.
			// Keep the older server's generic read behavior rather than fail
			// list/get/lint for a draft whose placeholders it cannot report.
			if api.IsGraphQLValidationFor(err, "isPlaceholder") {
				return draftInfo{Placeholders: map[string]bool{}}, nil
			}
			return info, api.MapError(err)
		}
		if resp.FindNodes == nil {
			return info, nil
		}
		for _, h := range resp.FindNodes.Hits {
			if h != nil && h.Node != nil && h.Node.IsPlaceholder {
				info.Placeholders[h.Node.Loc] = true
			}
		}
		if len(resp.FindNodes.Hits) < nodesPageSize {
			return info, nil
		}
	}
}

// unresolvedFindings turns a draft corpus's unresolved references into lint
// warnings at the citing spec, for the specs in scope. Warnings, not errors:
// in a draft an open reference is work in progress, and `spec mint` is the
// gate that refuses it (`--strict` escalates them like any warning).
func unresolvedFindings(ctx context.Context, client graphql.Client, memRef string, inScope map[string]bool) ([]lintFindingDTO, error) {
	resp, err := gen.SpecUnresolvedReferences(ctx, client, memRef)
	if err != nil {
		// #1453 added this query after draft state and placeholders. Lint
		// still runs its ordinary rules when this report is unavailable.
		if api.IsGraphQLValidationFor(err, "specUnresolvedReferences") {
			return nil, nil
		}
		return nil, api.MapError(err)
	}
	var out []lintFindingDTO
	for _, r := range resp.SpecUnresolvedReferences {
		if r == nil || !inScope[r.SourceLoc] {
			continue
		}
		what := "which has no spec"
		if r.Reason != nil && *r.Reason == gen.SpecReferenceProblemPlaceholder {
			what = "which is only a placeholder"
		}
		how := "a URN in its " + r.Field
		switch r.Field {
		case "edge":
			how = "an edge"
		case "pendingEdge":
			how = "a pending edge"
		}
		out = append(out, lintFindingDTO{Citation: r.SourceLoc, Rule: "unresolved-reference", Severity: sevWarning,
			Message: fmt.Sprintf("refers to %s, %s (%s) — `spec mint` refuses while it remains", r.TargetLoc, what, how)})
	}
	return out, nil
}

// placeholderFinding is the one lint finding a placeholder gets, from `spec
// lint` and `spec get` alike.
func placeholderFinding(loc string) lintFindingDTO {
	return lintFindingDTO{Citation: loc, Rule: "placeholder", Severity: sevWarning,
		Message: fmt.Sprintf("reserved placeholder, not yet written — write it with `hadron spec edit %s`; `spec mint` refuses while it remains", loc)}
}

// placeholderLabel is a list row's NAME, marked when the spec is a placeholder.
func placeholderLabel(s specDTO) string {
	if s.Placeholder {
		return s.Name + "  [placeholder]"
	}
	return s.Name
}
