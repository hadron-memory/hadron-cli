package node

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

func newCmdReplace(f *cmdutil.Factory) *cobra.Command {
	var (
		oldText    string
		newText    string
		nodes      []string
		memories   []string
		prefix     string
		fields     []string
		regex      bool
		ignoreCase bool
		dryRun     bool
	)
	cmd := &cobra.Command{
		Use:     "replace --old <text> --new <text> --field <field> (--node <urn> | -m <memory>)",
		Aliases: []string{"search-replace"},
		Short:   "Search and replace text across many nodes",
		Long: `Search-and-replace a piece of text across many nodes in one call.

Select nodes by any combination of explicit --node references and whole
--memory scopes; --prefix narrows the --memory scope to a parent loc plus
its descendants (matched on ':' path boundaries, so 'auth' matches 'auth'
and 'auth:tokens' but not 'authoring'). At least one --node or --memory is
required.

--field chooses which text fields to search (repeatable): content, name,
alias, description, abstract, tags. Matching is literal by default; --regex
treats --old as a regular expression and --new as a replacement pattern
(with $1/$& backrefs). --ignore-case folds case.

ALWAYS run with --dry-run first to preview the match counts before writing —
this can rewrite many nodes at once. Each change is captured in version
history.`,
		Example: `  # Preview first
  hadron node replace --old teh --new the -m acme.com:kb --field content --dry-run

  # Apply across a subtree, two fields
  hadron node replace --old foo --new bar -m acme.com:kb --prefix services: --field content --field description

  # Regex with a backreference on specific nodes
  hadron node replace --old '(\w+)@old\.com' --new '$1@new.com' --node acme.com:kb:contacts:bob --field content --regex`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("old") {
				return exitcode.Newf(exitcode.Usage, "--old is required")
			}
			if !changed("new") {
				return exitcode.Newf(exitcode.Usage, `--new is required (use --new "" to delete the matched text)`)
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

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			input := gen.SearchReplaceInNodesInput{
				OldText:         oldText,
				NewText:         newText,
				Fields:          gqlFields,
				NodeIds:         nodes,
				MemoryIds:       memories,
				CaseInsensitive: &ignoreCase,
				Regex:           &regex,
				DryRun:          &dryRun,
			}
			if prefix != "" {
				input.Prefix = &prefix
			}

			resp, err := gen.SearchReplaceInNodes(cmd.Context(), client, &input)
			if err != nil {
				return api.MapError(err)
			}
			r := resp.SearchReplaceInNodes

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

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
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
			})
		},
	}
	cmd.Flags().StringVar(&oldText, "old", "", "text (or regex with --regex) to find (required)")
	cmd.Flags().StringVar(&newText, "new", "", `replacement text (use "" to delete; required)`)
	cmd.Flags().StringArrayVar(&nodes, "node", nil, "node URN or ID to edit (repeatable)")
	cmd.Flags().StringArrayVarP(&memories, "memory", "m", nil, "memory to edit every node in (ID or URN; repeatable)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "restrict --memory selection to a loc prefix (parent + descendants)")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "text field to search, repeatable: content, name, alias, description, abstract, tags")
	cmd.Flags().BoolVar(&regex, "regex", false, "treat --old as a regular expression and --new as a replacement pattern")
	cmd.Flags().BoolVar(&ignoreCase, "ignore-case", false, "match case-insensitively")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview match counts without writing anything")
	return cmd
}
