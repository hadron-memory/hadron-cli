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

func newCmdMerge(f *cmdutil.Factory) *cobra.Command {
	var (
		memory       string
		into         string
		fields       []string
		deleteSource bool
		yes          bool
	)
	cmd := &cobra.Command{
		Use:   "merge <source-urn> | <loc> -m <memory> --into <target>",
		Short: "Merge (fold) one node into another",
		Long: `Fold a source node into a target node, returning the surviving target.
Name each node by its fully-qualified URN (<org>::<memory>::<loc>) or by a
bare <loc> with -m/--memory (which scopes BOTH the source and --into target).

By default every mergeable field folds in; restrict with repeated --field:

  CONTENT       concatenate source content after the target's (blank-line separated)
  ABSTRACT      concatenate source abstract after the target's (capped)
  DESCRIPTION   concatenate source description after the target's
  TAGS          union the tag sets (target order first)
  DATA          shallow-merge the data JSON (target wins on key collisions)
  PROPERTIES    shallow-merge the properties JSON (target wins)
  EDGES         re-point the source's incoming/outgoing edges onto the target

The source node is left in place unless you pass --delete-source, which
hard-removes it after a successful merge. The merge mutates the target (and,
with EDGES, re-points relationships), so it prompts for confirmation on a
terminal and requires --yes non-interactively.`,
		Example: `  hadron node merge acme.com::kb::findings:dup --into acme.com::kb::findings:canonical --yes
  hadron node merge findings:dup -m acme.com::kb --into findings:canonical --field CONTENT --field EDGES --yes
  hadron node merge acme.com::kb::findings:dup --into acme.com::kb::findings:canonical --delete-source --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate flags before any network/auth so a bad combo is a usage
			// error, not an auth/transport failure.
			if strings.TrimSpace(into) == "" {
				return exitcode.Newf(exitcode.Usage, "specify the surviving node with --into <urn> (or <loc> with -m)")
			}
			include, err := parseMergeFields(fields)
			if err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// -m scopes bare locs for both endpoints (mirrors `edge add`).
			// Resolve before confirming so a bad ref errors before the prompt
			// and the gate is only reached for a real merge (as `node rm` does).
			sourceRef, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			targetRef, err := cmdutil.ResolveNodeRef(cmd, client, memory, into)
			if err != nil {
				return err
			}

			// Merging mutates the target and can delete the source — gate it like
			// the other destructive/bulk-write commands.
			what := fmt.Sprintf("merge %s into %s", args[0], into)
			if deleteSource {
				what += " and hard-delete the source"
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, "About to "+what+"."); err != nil {
				return err
			}

			input := gen.MergeNodesInput{
				Source:  sourceRef,
				Target:  targetRef,
				Include: include, // nil = every mergeable field
			}
			if deleteSource {
				input.DeleteSource = &deleteSource
			}
			resp, err := gen.MergeNodes(cmd.Context(), client, &input)
			if err != nil {
				return api.MapError(err)
			}
			dto := mergeNodesDTO(resp.MergeNodes)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ merged", dto.Loc, dto.Name)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve bare <loc> source/target against")
	cmd.Flags().StringVar(&into, "into", "", "the surviving target node (URN, or <loc> with -m)")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "field to fold in (repeatable): CONTENT|ABSTRACT|DESCRIPTION|TAGS|DATA|PROPERTIES|EDGES (default: all)")
	cmd.Flags().BoolVar(&deleteSource, "delete-source", false, "hard-delete the source node after a successful merge")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// parseMergeFields maps repeated --field values (case-insensitive) to the
// NodeMergeField enum. An empty selection returns nil so the server merges
// every mergeable field. An unknown value is a usage error naming the valid set.
func parseMergeFields(fields []string) ([]gen.NodeMergeField, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	valid := map[string]gen.NodeMergeField{
		"ABSTRACT":    gen.NodeMergeFieldAbstract,
		"CONTENT":     gen.NodeMergeFieldContent,
		"DATA":        gen.NodeMergeFieldData,
		"DESCRIPTION": gen.NodeMergeFieldDescription,
		"EDGES":       gen.NodeMergeFieldEdges,
		"PROPERTIES":  gen.NodeMergeFieldProperties,
		"TAGS":        gen.NodeMergeFieldTags,
	}
	seen := map[gen.NodeMergeField]bool{}
	out := []gen.NodeMergeField{}
	for _, f := range fields {
		key := strings.ToUpper(strings.TrimSpace(f))
		mf, ok := valid[key]
		if !ok {
			return nil, exitcode.Newf(exitcode.Usage,
				"invalid --field %q (want ABSTRACT, CONTENT, DATA, DESCRIPTION, EDGES, PROPERTIES, or TAGS)", f)
		}
		if !seen[mf] {
			seen[mf] = true
			out = append(out, mf)
		}
	}
	// Deterministic order regardless of flag order — the set is what matters.
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func mergeNodesDTO(n *gen.MergeNodesMergeNodesNode) nodeDTO {
	return nodeDTO{
		ID:         n.Id,
		MemoryID:   n.MemoryId,
		Loc:        n.Loc,
		Name:       n.Name,
		NodeType:   n.NodeType,
		Tags:       n.Tags,
		Seq:        nil,
		IsRunnable: boolVal(n.IsRunnable),
		UpdatedAt:  n.UpdatedAt,
	}
}
