// Package replacecmd implements `hadron replace`, bulk search-and-replace
// across many nodes (the CLI mirror of the MCP tool hadron_replace_globally).
package replacecmd

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// replaceFieldDTO / replaceNodeDTO / replaceResultDTO are the stable --json shape.
type replaceFieldDTO struct {
	Field   string `json:"field"`
	Matches int    `json:"matches"`
}

type replaceNodeDTO struct {
	NodeID       string            `json:"nodeId"`
	Loc          string            `json:"loc"`
	MemoryID     string            `json:"memoryId"`
	Replacements int               `json:"replacements"`
	Fields       []replaceFieldDTO `json:"fields"`
}

type replaceResultDTO struct {
	NodesScanned      int              `json:"nodesScanned"`
	NodesChanged      int              `json:"nodesChanged"`
	TotalReplacements int              `json:"totalReplacements"`
	DryRun            bool             `json:"dryRun"`
	Results           []replaceNodeDTO `json:"results"`
}

// textFields maps a --field value to the generated enum.
var textFields = map[string]gen.NodeTextField{
	"content":     gen.NodeTextFieldContent,
	"name":        gen.NodeTextFieldName,
	"alias":       gen.NodeTextFieldAlias,
	"description": gen.NodeTextFieldDescription,
	"abstract":    gen.NodeTextFieldAbstract,
	"tags":        gen.NodeTextFieldTags,
}

// NewCmdReplace builds the top-level `hadron replace` command group.
func NewCmdReplace(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replace",
		Short: "Search and replace text in nodes",
	}
	cmd.AddCommand(newCmdReplaceText(f))
	return cmd
}

// newCmdReplaceText builds the `hadron replace text` subcommand.
func newCmdReplaceText(f *cmdutil.Factory) *cobra.Command {
	var (
		nodes      []string
		memories   []string
		prefix     string
		fields     []string
		regex      bool
		ignoreCase bool
		dryRun     bool
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "text <old> <new> --field <field> (--node <urn> | -m <memory>)",
		Short: "Search and replace text across many nodes",
		Long: `Search-and-replace a piece of text across many nodes in one call
(the CLI mirror of the MCP tool hadron_replace_globally).

Select nodes by any combination of explicit --node references and whole
--memory scopes; --prefix narrows a --memory scope to a parent loc plus its
descendants (matched on ':' path boundaries, so 'auth' matches 'auth' and
'auth:tokens' but not 'authoring'). At least one --node or --memory is
required.

--field chooses which text fields to search (repeatable): content, name,
alias, description, abstract, tags. Matching is literal by default; --regex
treats <old> as a regular expression and <new> as a replacement pattern
(with $1/$& backrefs). -i/--ignore-case folds case.

A real run previews the per-node match counts and asks for confirmation
before writing; pass --yes to skip the prompt (required in non-interactive
use), or --dry-run to preview without writing. Every change is saved to
version history, so replacements are undoable.`,
		Example: `  # Preview only
  hadron replace text oldtext newtext -m acme.com:kb --field content --dry-run

  # Apply across a subtree, two fields (prompts before writing)
  hadron replace text foo bar -m acme.com:kb --prefix services: --field content --field description

  # Regex with a backreference on specific nodes, no prompt
  hadron replace text '(\w+)@old\.com' '$1@new.com' --node acme.com:kb:contacts:bob --field content --regex --yes`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldText, newText := args[0], args[1]
			if oldText == "" {
				return exitcode.Newf(exitcode.Usage, "<old> must not be empty")
			}
			if len(nodes) == 0 && len(memories) == 0 {
				return exitcode.Newf(exitcode.Usage, "select nodes with at least one --node or --memory")
			}
			if prefix != "" && len(memories) == 0 {
				return exitcode.Newf(exitcode.Usage, "--prefix requires --memory (loc is only unique within a memory)")
			}
			if len(fields) == 0 {
				return exitcode.Newf(exitcode.Usage, "pass at least one --field (content, name, alias, description, abstract, tags)")
			}
			gqlFields := make([]gen.NodeTextField, 0, len(fields))
			for _, fl := range fields {
				tf, ok := textFields[strings.ToLower(fl)]
				if !ok {
					return exitcode.Newf(exitcode.Usage, "unknown --field %q (valid: content, name, alias, description, abstract, tags)", fl)
				}
				gqlFields = append(gqlFields, tf)
			}

			// Fail fast for agents: a real write without --yes is only allowed
			// interactively (where we prompt). Mirrors the destructive-flow rule.
			if !dryRun && !yes && !f.IOStreams.IsInputTerminal() {
				return exitcode.Newf(exitcode.Usage,
					"refusing to write without --yes in non-interactive mode; pass --dry-run to preview or --yes to apply")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			run := func(dry bool) (replaceResultDTO, error) {
				input := gen.SearchReplaceInNodesInput{
					OldText:         oldText,
					NewText:         newText,
					Fields:          gqlFields,
					NodeIds:         nodes,
					MemoryIds:       memories,
					CaseInsensitive: &ignoreCase,
					Regex:           &regex,
					DryRun:          &dry,
				}
				if prefix != "" {
					input.Prefix = &prefix
				}
				resp, err := gen.SearchReplaceInNodes(cmd.Context(), client, &input)
				if err != nil {
					return replaceResultDTO{}, api.MapError(err)
				}
				return toDTO(resp.SearchReplaceInNodes), nil
			}

			// Preview-only.
			if dryRun {
				dto, err := run(true)
				if err != nil {
					return err
				}
				return writeReport(f, dto)
			}

			// Real write. Unless --yes, preview first and confirm.
			if !yes {
				preview, err := run(true)
				if err != nil {
					return err
				}
				_ = renderReport(f.IOStreams.ErrOut, preview)
				if preview.TotalReplacements == 0 {
					fmt.Fprintln(f.IOStreams.ErrOut, "No matches — nothing to replace.")
					return writeReport(f, preview)
				}
				prompt := fmt.Sprintf("Replace %d occurrence(s) across %d node(s)? Changes are saved to version history.",
					preview.TotalReplacements, preview.NodesChanged)
				if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
					return err
				}
			}

			dto, err := run(false)
			if err != nil {
				return err
			}
			return writeReport(f, dto)
		},
	}
	cmd.Flags().StringVar(&prefix, "prefix", "", "restrict --memory selection to a loc prefix (parent + descendants)")
	cmd.Flags().StringArrayVar(&nodes, "node", nil, "node URN or ID to edit (repeatable)")
	cmd.Flags().StringArrayVarP(&memories, "memory", "m", nil, "memory to edit every node in (ID or URN; repeatable)")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "text field to search, repeatable: content, name, alias, description, abstract, tags")
	cmd.Flags().BoolVar(&regex, "regex", false, "treat <old> as a regular expression and <new> as a replacement pattern")
	cmd.Flags().BoolVarP(&ignoreCase, "ignore-case", "i", false, "match case-insensitively")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview match counts without writing anything")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (required in non-interactive use)")
	return cmd
}

func toDTO(r *gen.SearchReplaceInNodesSearchReplaceInNodesSearchReplaceResult) replaceResultDTO {
	dto := replaceResultDTO{
		NodesScanned:      r.NodesScanned,
		NodesChanged:      r.NodesChanged,
		TotalReplacements: r.TotalReplacements,
		DryRun:            r.DryRun,
	}
	for _, n := range r.Results {
		nd := replaceNodeDTO{
			NodeID:       n.NodeId,
			Loc:          n.Loc,
			MemoryID:     n.MemoryId,
			Replacements: n.Replacements,
		}
		for _, fres := range n.Fields {
			nd.Fields = append(nd.Fields, replaceFieldDTO{Field: string(fres.Field), Matches: fres.Matches})
		}
		dto.Results = append(dto.Results, nd)
	}
	sort.Slice(dto.Results, func(i, j int) bool { return dto.Results[i].Loc < dto.Results[j].Loc })
	return dto
}

// writeReport renders to stdout, honoring --json.
func writeReport(f *cmdutil.Factory, dto replaceResultDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		return renderReport(w, dto)
	})
}

// renderReport writes the human-readable summary + per-node table to w.
func renderReport(w io.Writer, dto replaceResultDTO) error {
	verb := "Replaced"
	suffix := "."
	if dto.DryRun {
		verb = "Would replace"
		suffix = " (dry run — nothing written)."
	}
	fmt.Fprintf(w, "%s %d occurrence(s) across %d of %d node(s) scanned%s\n",
		verb, dto.TotalReplacements, dto.NodesChanged, dto.NodesScanned, suffix)
	if len(dto.Results) == 0 {
		return nil
	}
	t := output.NewTable(w, "LOC", "REPLACEMENTS", "FIELDS")
	for _, n := range dto.Results {
		parts := make([]string, 0, len(n.Fields))
		for _, fres := range n.Fields {
			parts = append(parts, fmt.Sprintf("%s×%d", fres.Field, fres.Matches))
		}
		t.Row(n.Loc, fmt.Sprint(n.Replacements), strings.Join(parts, ", "))
	}
	return t.Flush()
}
