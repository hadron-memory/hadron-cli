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
Name each node by its fully-qualified URN (hrn:node:<root>:<slug>:<loc>) or by a
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
		Example: `  hadron node merge hrn:node:acme.com:kb:findings:dup --into hrn:node:acme.com:kb:findings:canonical --yes
  hadron node merge findings:dup -m hrn:mem:acme.com:kb --into findings:canonical --field CONTENT --field EDGES --yes
  hadron node merge hrn:node:acme.com:kb:findings:dup --into hrn:node:acme.com:kb:findings:canonical --delete-source --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate flags before any network/auth so a bad combo is a usage
			// error, not an auth/transport failure.
			if strings.TrimSpace(into) == "" {
				return exitcode.Newf(exitcode.Usage, "specify the surviving node with --into <urn> (or <loc> with -m)")
			}
			include, err := cmdutil.ParseMergeFields(fields)
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
			prompt := fmt.Sprintf("About to merge %s into %s.", args[0], into)
			if deleteSource {
				// --delete-source hard-removes the source — call out that it's
				// irreversible, matching the deletion prompts' wording.
				prompt = fmt.Sprintf("About to merge %s into %s and hard-delete the source. This cannot be undone.", args[0], into)
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
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
			// mergeNodes is declared Node! so a conformant server never returns
			// null without an error, but guard the deref rather than panic on a
			// misbehaving one.
			if resp == nil || resp.MergeNodes == nil {
				return exitcode.Newf(exitcode.Error, "merge returned no node")
			}
			dto := mergeNodesDTO(resp.MergeNodes)
			dto.LintWarnings = loadLintWarnings(cmd, client, dto.ID)
			return writeWithLint(f, dto, dto.LintWarnings, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ merged", output.SafeCell(dto.Loc), output.SafeCell(dto.Name))
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve bare <loc> source/target against")
	cmd.Flags().StringVar(&into, "into", "", "the surviving target node (URN, or <loc> with -m)")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "field to fold in (repeatable): CONTENT|ABSTRACT|DESCRIPTION|TAGS|DATA|PROPERTIES|EDGES (default: all)")
	cmd.Flags().BoolVar(&deleteSource, "delete-source", false, "hard-delete the source node after a successful merge")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	enableLintWarnings(cmd)
	return cmd
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
		Role:       n.Role,
		UpdatedAt:  n.UpdatedAt,
	}
}
