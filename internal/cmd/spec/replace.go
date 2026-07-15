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
	NodeID       string                `json:"nodeId"`
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

			oldText, regexFlag := buildReplacePattern(pattern, useRegex, wordBoundary)

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}
			// Scope the rewrite to the SPEC nodes explicitly: searchReplaceInNodes
			// with memoryIds would rewrite every live node in the memory (the
			// register, any non-spec node), but this command is citation-aware. So
			// list the spec-tagged citation nodes in scope (--prefix narrows here,
			// not on the wire) and pass their ids as nodeIds. Doubles as the id set
			// for the re-lint below.
			var prefixPtr *string
			if prefix != "" {
				prefixPtr = &prefix
			}
			all, err := scanAllNodes(cmd.Context(), client, &memURN, prefixPtr, []string{"spec"})
			if err != nil {
				return err
			}
			specIDs := make([]string, 0, len(all))
			for _, n := range all {
				if n == nil {
					continue
				}
				if _, perr := ParseCitation(n.Loc); perr != nil {
					continue
				}
				specIDs = append(specIDs, n.Id)
			}
			if len(specIDs) == 0 {
				fmt.Fprintln(f.IOStreams.ErrOut, "No specs in scope — nothing to replace.")
				return writeSpecReplaceReport(f, specReplaceResultDTO{DryRun: dryRun, Results: []specReplaceNodeDTO{}})
			}

			run := func(dry bool) (specReplaceResultDTO, error) {
				input := gen.SearchReplaceInNodesInput{
					OldText:         oldText,
					NewText:         replacement,
					Fields:          fields,
					NodeIds:         specIDs,
					CaseInsensitive: &ignoreCase,
					Regex:           &regexFlag,
					DryRun:          &dry,
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
			// signal of blast radius, and an over-broad scope (a wrong -m, or a
			// forgotten --prefix) can rewrite the whole corpus in one call.
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
			dto.Lint = relintChanged(cmd, client, dto.Results, f)
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
// server's searchReplaceInNodes wants. --regex passes the pattern through
// untouched — the server (a JS RegExp, not Go's RE2) is the source of truth for
// its validity, so a malformed pattern surfaces as the server's error rather
// than a Go/JS engine-mismatch false-reject here. Otherwise the literal is
// quoted, and (by default) wrapped in \b so only whole tokens match — sent as a
// regex because that is how word boundaries are expressed. --word-boundary=false
// is a plain literal replace.
func buildReplacePattern(pattern string, useRegex, wordBoundary bool) (oldText string, regex bool) {
	switch {
	case useRegex:
		return pattern, true
	case wordBoundary:
		// \b asserts a boundary between a word char ([A-Za-z0-9_]) and a non-word
		// char, so it is only meaningful next to a word char. Anchor each end only
		// when its outermost rune is a word char — otherwise a leading/trailing
		// non-word char (e.g. "@handle", "h-read-node!") would make \b fail to
		// match and the replace silently do nothing. The escaped form is valid in
		// the server's RegExp engine too. (Internal '-' in a token like
		// h-read-node is fine — only the outer boundaries are anchored.)
		out := regexp.QuoteMeta(pattern)
		r := []rune(pattern)
		if len(r) > 0 {
			if isWordRune(r[0]) {
				out = `\b` + out
			}
			if isWordRune(r[len(r)-1]) {
				out += `\b`
			}
		}
		return out, true
	default:
		return pattern, false
	}
}

// isWordRune reports whether r is a regex word character ([A-Za-z0-9_]) — the
// class \b is defined against.
func isWordRune(r rune) bool {
	return r == '_' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
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
		nd := specReplaceNodeDTO{Citation: n.Loc, NodeID: n.NodeId, Replacements: n.Replacements}
		for _, fres := range n.Fields {
			nd.Fields = append(nd.Fields, specReplaceFieldDTO{Field: string(fres.Field), Matches: fres.Matches})
		}
		dto.Results = append(dto.Results, nd)
	}
	sort.Slice(dto.Results, func(i, j int) bool { return dto.Results[i].Citation < dto.Results[j].Citation })
	return dto
}

// relintChanged re-lints the specs a real replace rewrote, in a single bulk
// nodeBatch read (not a per-spec fetch loop), returning their findings sorted by
// citation. Best-effort: a read error or an unreadable spec is noted on stderr,
// never fatal — the replace already succeeded.
func relintChanged(cmd *cobra.Command, client graphql.Client, changed []specReplaceNodeDTO, f *cmdutil.Factory) []lintFindingDTO {
	ids := make([]string, 0, len(changed))
	for _, c := range changed {
		if c.NodeID != "" {
			ids = append(ids, c.NodeID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	nodes, unavailable, err := api.CollectNodeBatch(ids, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
		resp, err := gen.NodeBatch(cmd.Context(), client, chunk)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp == nil {
			return nil, nil
		}
		return resp.NodeBatch, nil
	})
	if err != nil {
		fmt.Fprintf(f.IOStreams.ErrOut, "note: could not re-lint the rewritten specs: %v\n", err)
		return nil
	}
	if len(unavailable) > 0 {
		fmt.Fprintf(f.IOStreams.ErrOut, "note: %d rewritten spec(s) unreadable — not re-linted\n", len(unavailable))
	}
	var findings []lintFindingDTO
	for _, n := range nodes {
		if n == nil {
			continue
		}
		findings = append(findings, lintNode(nodeFromBatch(n))...)
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
