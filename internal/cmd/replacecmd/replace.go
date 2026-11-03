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
		Long: `Search and replace text in nodes. Its one command, replace text, finds a
piece of text across many nodes (a whole memory, a branch of it, or a list of
nodes) and replaces it in one step, after showing how many matches each node
has. Every change is saved to the node's version history and can be undone.
Comments, governed nodes and protected nodes are skipped; see replace text.`,
		Example: `  hadron replace text "Acme Corp" "Acme Inc" -m hrn:mem:acme.com:kb --field content --dry-run`,
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
		reason     string
		maxNodes   int
	)
	cmd := &cobra.Command{
		Use:   "text <old> <new> --field <field> (--node <urn> | -m <memory>)",
		Short: "Search and replace text across many nodes",
		Long: `Replace a piece of text across many nodes in one step, for example to correct
a wrong term everywhere it appears.

Choose the nodes with --node (a node URN or id, repeatable), with -m/--memory
(every node in that memory, repeatable), or both; at least one is required.

Some nodes are never changed, and are skipped without a warning: comments
(change them with comment edit), governed nodes such as specs and reviews
(they have their own commands), compiled boot prompts, and nodes at protected
addresses. Skipped nodes are not counted in "scanned", and this command does
not report how many were skipped.
--prefix narrows a --memory to one branch: the node at that loc and
everything below it (it matches on ':' boundaries, so 'auth' matches 'auth'
and 'auth:tokens' but not 'authoring').

--field is required: it names the text to search, and can be repeated:
content (the body), name, alias (the node's alternative name), description,
abstract (the summary) or tags. Matching is exact and case-sensitive unless
you pass -i/--ignore-case. With --regex, <old> is a regular expression and
<new> may use $1 for the first group in parentheses and $& for the whole match.

It always counts the matches first. --dry-run stops there and writes nothing.
Otherwise it shows the count per node and asks before writing; without a
terminal (a script or an AI agent) pass --yes, which skips the question but
still prints the totals to stderr before writing. --max-nodes N refuses the
whole change if more than N nodes would change: a guard against rewriting a
memory by mistake (a wrong --memory, a forgotten --prefix).

It prints the totals ("Replaced 3 occurrence(s) across 2 of 40 node(s)
scanned."), then a table of the changed nodes: LOC, REPLACEMENTS and FIELDS
(content×2 means two matches in the body). --json prints nodesScanned,
nodesChanged, totalReplacements, dryRun and results.

Before it changes a node, it saves the node's previous text as a revision
in its version history, labelled with --reason if you give one. To undo the
replacement on a node, find that revision in hadron node revision list <node>
(its LABEL is your --reason; it is the newest only until the node is edited
again) and restore it with hadron node revision restore <revision-id>.`,
		Example: `  # Preview only: count the matches, write nothing
  hadron replace text "Acme Corp" "Acme Inc" -m hrn:mem:acme.com:kb --field content --dry-run

  # Apply across one branch, in two fields (asks before writing)
  hadron replace text foo bar -m hrn:mem:acme.com:kb --prefix services --field content --field description

  # Regex with a group, on one node, no question
  hadron replace text '(\w+)@old\.com' '$1@new.com' --node hrn:node:acme.com:kb:contacts:bob --field content --regex --yes

  # Undo on one node
  hadron node revision list hrn:node:acme.com:kb:contacts:bob
  hadron node revision restore <revision-id>`,
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
			if maxNodes < 0 {
				return exitcode.Newf(exitcode.Usage, "--max-nodes must be zero (no limit) or a positive integer, got %d", maxNodes)
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
				// A blank/whitespace-only reason is treated as unset, so it can't
				// override the server's clientId/userId editedBy fallback.
				if r := strings.TrimSpace(reason); r != "" {
					input.Reason = &r
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

			// Real write. ALWAYS preview first — the affected count is the only
			// signal of blast radius, and a whole-memory `-m` scope (a wrong URN,
			// or a forgotten --prefix) can rewrite every node in one call. The
			// --yes path agents use previously skipped this entirely (#126).
			preview, err := run(true)
			if err != nil {
				return err
			}
			if preview.TotalReplacements == 0 {
				fmt.Fprintln(f.IOStreams.ErrOut, "No matches — nothing to replace.")
				// This is the real-run result (a no-op), not a preview: report it as
				// such so --json shows dryRun=false for a run the user did not flag
				// --dry-run on.
				preview.DryRun = false
				return writeReport(f, preview)
			}
			if maxNodes > 0 && preview.NodesChanged > maxNodes {
				return exitcode.Newf(exitcode.Usage,
					"refusing to replace across %d node(s): exceeds --max-nodes=%d — narrow the scope with --prefix/--node, or raise --max-nodes",
					preview.NodesChanged, maxNodes)
			}
			if yes {
				// No prompt, but never write blind: surface the count on stderr first.
				fmt.Fprintf(f.IOStreams.ErrOut, "Replacing %d occurrence(s) across %d node(s) (--yes)...\n",
					preview.TotalReplacements, preview.NodesChanged)
			} else {
				_ = renderReport(f.IOStreams.ErrOut, preview)
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
	cmd.Flags().StringVar(&prefix, "prefix", "", "only the node at this loc in --memory and everything below it")
	cmd.Flags().StringArrayVar(&nodes, "node", nil, "node URN or ID to edit (repeatable)")
	cmd.Flags().StringArrayVarP(&memories, "memory", "m", nil, "memory to edit every node in (ID or URN; repeatable)")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "text field to search, repeatable: content, name, alias, description, abstract, tags")
	cmd.Flags().BoolVar(&regex, "regex", false, "treat <old> as a regular expression and <new> as a replacement pattern")
	cmd.Flags().BoolVarP(&ignoreCase, "ignore-case", "i", false, "match case-insensitively")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview match counts without writing anything")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (required in non-interactive use)")
	cmd.Flags().IntVar(&maxNodes, "max-nodes", 0, "refuse to apply if more than N nodes would change (0 = no limit)")
	cmd.Flags().StringVar(&reason, "reason", "", "why this change was made (recorded in version history)")
	return cmd
}

func toDTO(r *gen.SearchReplaceInNodesSearchReplaceInNodesSearchReplaceResult) replaceResultDTO {
	dto := replaceResultDTO{
		NodesScanned:      r.NodesScanned,
		NodesChanged:      r.NodesChanged,
		TotalReplacements: r.TotalReplacements,
		DryRun:            r.DryRun,
		Results:           []replaceNodeDTO{},
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
		t.Row(output.SafeCell(n.Loc), fmt.Sprint(n.Replacements), output.SafeCell(strings.Join(parts, ", ")))
	}
	return t.Flush()
}
