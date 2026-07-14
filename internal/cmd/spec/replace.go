package spec

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// specReplaceFieldDTO / specReplaceNodeDTO / specReplaceResultDTO are the stable
// --json shape — the citation-keyed analogue of `hadron replace text`'s output.
type specReplaceFieldDTO struct {
	Field   string `json:"field"`
	Matches int    `json:"matches"`
}

type specReplaceNodeDTO struct {
	Citation     string                `json:"citation"`
	Replacements int                   `json:"replacements"`
	Fields       []specReplaceFieldDTO `json:"fields"`
}

type specReplaceResultDTO struct {
	SpecsScanned      int                  `json:"specsScanned"`
	SpecsChanged      int                  `json:"specsChanged"`
	TotalReplacements int                  `json:"totalReplacements"`
	DryRun            bool                 `json:"dryRun"`
	Results           []specReplaceNodeDTO `json:"results"`
	// Lint is the post-replace lint of the changed specs — populated only on a
	// real (non-dry) run. Empty means a clean re-lint.
	Lint []lintFindingDTO `json:"lint,omitempty"`
}

func newCmdReplace(f *cmdutil.Factory) *cobra.Command {
	var memory, prefix, field, reason string
	var useRegex, ignoreCase, dryRun, yes bool
	wordBoundary := true
	var maxSpecs int
	cmd := &cobra.Command{
		Use:   "replace <pattern> <replacement> [-m <memory>]",
		Short: "Bulk find/replace across spec bodies + abstracts",
		Long: `Search-and-replace a token across every spec's body and abstract in one
call — the spec-scoped, citation-aware analogue of ` + "`hadron replace text`" + `.

Matching is a literal token, and by default it is WORD-BOUNDARY-AWARE: only
whole-token occurrences are rewritten, so renaming ` + "`h-read-node`" + ` never
touches ` + "`h-read-nodes`" + ` or ` + "`h-read-next-node`" + `. Pass
--word-boundary=false for a raw substring replace, or --regex to treat
<pattern> as a regular expression (<replacement> may use $1/$& backrefs); in
--regex mode you control the boundaries yourself. -i folds case.

By default both the body and the abstract are rewritten; restrict with --field
content|abstract.

A real run previews the affected specs and per-citation match counts, then asks
for confirmation (or pass --yes; required non-interactively); --dry-run previews
without writing, and --max-specs N refuses a run that would change more than N
specs. Every change is saved to version history. After a real run the changed
specs are re-linted and any findings are reported — a bulk body rewrite can, for
example, leave an abstract out of sync with its content.`,
		Example: `  # Preview a corpus-wide rename
  hadron spec replace h-read-node hadron_get_node -m hadronmemory.com::specs --dry-run

  # Apply it (whole-token only), across one module
  hadron spec replace h-read-node hadron_get_node -m hadronmemory.com::specs --prefix cor:api --yes

  # Regex with a backref
  hadron spec replace 'h-chat-(\w+)' 'hadron_chatbot_$1' -m hadronmemory.com::specs --regex --yes`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pattern, replacement := args[0], args[1]
			if pattern == "" {
				return exitcode.Newf(exitcode.Usage, "<pattern> must not be empty")
			}
			fields, err := parseReplaceFields(field)
			if err != nil {
				return err
			}
			if maxSpecs < 0 {
				return exitcode.Newf(exitcode.Usage, "--max-specs must be zero (no limit) or a positive integer, got %d", maxSpecs)
			}
			// Fail fast for agents: a real write without --yes is only allowed
			// interactively (where we prompt). Mirrors the destructive-flow rule.
			if !dryRun && !yes && !f.IOStreams.IsInputTerminal() {
				return exitcode.Newf(exitcode.Usage,
					"refusing to write without --yes in non-interactive mode; pass --dry-run to preview or --yes to apply")
			}

			oldText, regexFlag, err := buildReplacePattern(pattern, useRegex, wordBoundary)
			if err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// searchReplaceInNodes.memoryIds accepts a memory ID or URN (as
			// `hadron replace text` relies on), so the resolved spec-memory URN
			// goes straight on the wire — no separate ID lookup.
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}

			run := func(dry bool) (specReplaceResultDTO, error) {
				input := gen.SearchReplaceInNodesInput{
					OldText:         oldText,
					NewText:         replacement,
					Fields:          fields,
					MemoryIds:       []string{memURN},
					CaseInsensitive: &ignoreCase,
					Regex:           &regexFlag,
					DryRun:          &dry,
				}
				if prefix != "" {
					input.Prefix = &prefix
				}
				if r := strings.TrimSpace(reason); r != "" {
					input.Reason = &r
				}
				resp, err := gen.SearchReplaceInNodes(cmd.Context(), client, &input)
				if err != nil {
					return specReplaceResultDTO{}, api.MapError(err)
				}
				return specReplaceDTO(resp.SearchReplaceInNodes), nil
			}

			// Preview-only.
			if dryRun {
				dto, err := run(true)
				if err != nil {
					return err
				}
				return writeSpecReplaceReport(f, dto)
			}

			// Real write. ALWAYS preview first — the affected count is the only
			// signal of blast radius, and a whole-memory scope (a wrong -m, or a
			// forgotten --prefix) can rewrite the entire corpus in one call.
			preview, err := run(true)
			if err != nil {
				return err
			}
			if preview.TotalReplacements == 0 {
				fmt.Fprintln(f.IOStreams.ErrOut, "No matches — nothing to replace.")
				preview.DryRun = false
				return writeSpecReplaceReport(f, preview)
			}
			if maxSpecs > 0 && preview.SpecsChanged > maxSpecs {
				return exitcode.Newf(exitcode.Usage,
					"refusing to replace across %d spec(s): exceeds --max-specs=%d — narrow the scope with --prefix, or raise --max-specs",
					preview.SpecsChanged, maxSpecs)
			}
			if yes {
				fmt.Fprintf(f.IOStreams.ErrOut, "Replacing %d occurrence(s) across %d spec(s) (--yes)...\n",
					preview.TotalReplacements, preview.SpecsChanged)
			} else {
				_ = renderSpecReplaceReport(f.IOStreams.ErrOut, preview)
				prompt := fmt.Sprintf("Replace %d occurrence(s) across %d spec(s)? Changes are saved to version history.",
					preview.TotalReplacements, preview.SpecsChanged)
				if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
					return err
				}
			}

			dto, err := run(false)
			if err != nil {
				return err
			}
			// Re-lint the specs we just rewrote and fold any findings into the
			// result — a bulk body rewrite can, e.g., desync an abstract from its
			// content (abstract-stale). Best-effort: a lint read error doesn't
			// undo a successful replace, so it's surfaced as a note, not an error.
			dto.Lint = relintChanged(cmd, client, memURN, dto.Results, f)
			return writeSpecReplaceReport(f, dto)
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to `hadron spec use`, then the active memory)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "restrict to a citation prefix (that node + its descendants)")
	cmd.Flags().StringVar(&field, "field", "", "restrict to one field: content or abstract (default: both)")
	cmd.Flags().BoolVar(&useRegex, "regex", false, "treat <pattern> as a regular expression and <replacement> as a pattern ($1/$&)")
	cmd.Flags().BoolVar(&wordBoundary, "word-boundary", true, "match whole tokens only (ignored with --regex)")
	cmd.Flags().BoolVarP(&ignoreCase, "ignore-case", "i", false, "match case-insensitively")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview affected specs + match counts without writing")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (required in non-interactive use)")
	cmd.Flags().IntVar(&maxSpecs, "max-specs", 0, "refuse to apply if more than N specs would change (0 = no limit)")
	cmd.Flags().StringVar(&reason, "reason", "", "why this change was made (recorded in version history)")
	return cmd
}

// parseReplaceFields maps the --field flag to the server enum. Empty rewrites
// both content and abstract (the whole prose surface of a spec).
func parseReplaceFields(field string) ([]gen.NodeTextField, error) {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "":
		return []gen.NodeTextField{gen.NodeTextFieldContent, gen.NodeTextFieldAbstract}, nil
	case "content", "body":
		return []gen.NodeTextField{gen.NodeTextFieldContent}, nil
	case "abstract":
		return []gen.NodeTextField{gen.NodeTextFieldAbstract}, nil
	default:
		return nil, exitcode.Newf(exitcode.Usage, "unknown --field %q (valid: content, abstract, or omit for both)", field)
	}
}

// buildReplacePattern turns the CLI flags into the (oldText, regex) pair the
// server's searchReplaceInNodes wants. --regex passes through untouched;
// otherwise the literal is quoted, and (by default) wrapped in \b…\b so only
// whole tokens match — sent as a regex because that is how word boundaries are
// expressed. --word-boundary=false is a plain literal replace.
func buildReplacePattern(pattern string, useRegex, wordBoundary bool) (oldText string, regex bool, err error) {
	switch {
	case useRegex:
		if _, cerr := regexp.Compile(pattern); cerr != nil {
			return "", false, exitcode.Newf(exitcode.Usage, "invalid --regex pattern %q: %v", pattern, cerr)
		}
		return pattern, true, nil
	case wordBoundary:
		// QuoteMeta escapes RE2 metacharacters; the escaped form is also valid in
		// the server's RegExp engine for identifier-like tokens (letters, digits,
		// -, _), which is what word boundaries are meaningful for.
		return `\b` + regexp.QuoteMeta(pattern) + `\b`, true, nil
	default:
		return pattern, false, nil
	}
}

// specReplaceDTO folds the server result into the citation-keyed shape.
func specReplaceDTO(r *gen.SearchReplaceInNodesSearchReplaceInNodesSearchReplaceResult) specReplaceResultDTO {
	dto := specReplaceResultDTO{
		SpecsScanned:      r.NodesScanned,
		SpecsChanged:      r.NodesChanged,
		TotalReplacements: r.TotalReplacements,
		DryRun:            r.DryRun,
		Results:           []specReplaceNodeDTO{},
	}
	for _, n := range r.Results {
		nd := specReplaceNodeDTO{Citation: n.Loc, Replacements: n.Replacements}
		for _, fres := range n.Fields {
			nd.Fields = append(nd.Fields, specReplaceFieldDTO{Field: string(fres.Field), Matches: fres.Matches})
		}
		dto.Results = append(dto.Results, nd)
	}
	sort.Slice(dto.Results, func(i, j int) bool { return dto.Results[i].Citation < dto.Results[j].Citation })
	return dto
}

// relintChanged re-lints the specs a real replace rewrote, returning their
// findings (sorted by citation). Best-effort: an unreadable spec is noted on
// stderr, never fatal — the replace already succeeded.
func relintChanged(cmd *cobra.Command, client graphql.Client, memURN string, changed []specReplaceNodeDTO, f *cmdutil.Factory) []lintFindingDTO {
	var findings []lintFindingDTO
	for _, c := range changed {
		n, err := fetchSpecNode(cmd, client, memURN, c.Citation)
		if err != nil || n == nil {
			fmt.Fprintf(f.IOStreams.ErrOut, "note: could not re-lint %s after replace\n", c.Citation)
			continue
		}
		findings = append(findings, lintNode(nodeFromGQL(n))...)
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Citation < findings[j].Citation })
	return findings
}

func writeSpecReplaceReport(f *cmdutil.Factory, dto specReplaceResultDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		return renderSpecReplaceReport(w, dto)
	})
}

func renderSpecReplaceReport(w io.Writer, dto specReplaceResultDTO) error {
	verb, suffix := "Replaced", "."
	if dto.DryRun {
		verb, suffix = "Would replace", " (dry run — nothing written)."
	}
	fmt.Fprintf(w, "%s %d occurrence(s) across %d of %d spec(s) scanned%s\n",
		verb, dto.TotalReplacements, dto.SpecsChanged, dto.SpecsScanned, suffix)
	if len(dto.Results) > 0 {
		t := output.NewTable(w, "CITATION", "REPLACEMENTS", "FIELDS")
		for _, n := range dto.Results {
			parts := make([]string, 0, len(n.Fields))
			for _, fr := range n.Fields {
				parts = append(parts, fmt.Sprintf("%s:%d", fr.Field, fr.Matches))
			}
			t.Row(n.Citation, fmt.Sprintf("%d", n.Replacements), strings.Join(parts, " "))
		}
		if err := t.Flush(); err != nil {
			return err
		}
	}
	// Post-replace lint (real runs only).
	if !dto.DryRun {
		if len(dto.Lint) == 0 {
			fmt.Fprintln(w, "✓ re-lint clean")
		} else {
			fmt.Fprintf(w, "\n⚠ re-lint found %d issue(s) in the rewritten spec(s):\n", len(dto.Lint))
			lt := output.NewTable(w, "CITATION", "SEVERITY", "RULE", "MESSAGE")
			for _, fnd := range dto.Lint {
				lt.Row(fnd.Citation, fnd.Severity, fnd.Rule, fnd.Message)
			}
			if err := lt.Flush(); err != nil {
				return err
			}
		}
	}
	return nil
}
