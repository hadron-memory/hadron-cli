package memory

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

// memory merge (cli#1340): mergeMemories. Every rule about who may merge which
// memories (classes, encryption, write access, tombstoned addresses) is the
// server's; the CLI confirms, sends once and maps the refusal.
func newCmdMerge(f *cmdutil.Factory) *cobra.Command {
	var (
		into         string
		fields       []string
		deleteSource bool
		yes          bool
	)
	cmd := &cobra.Command{
		Use:   "merge <memoryRef> --into <memoryRef>",
		Short: "Move every node of one memory into another",
		Long: `Move every node of the source memory into the target memory.

A source node whose loc is free in the target moves there unchanged. A source
node whose loc the target already uses is folded into that node, as node merge
does. By default every field folds in; restrict it with repeated --field:

  CONTENT       append the source content after the target's
  ABSTRACT      append the source abstract after the target's
  DESCRIPTION   append the source description after the target's
  TAGS          union the tags
  DATA          merge the data JSON (the target wins on a shared key)
  PROPERTIES    merge the properties JSON (the target wins on a shared key)
  EDGES         move the source node's edges onto the target node

Edges move with their nodes. The source memory is left empty; --delete-source
also deletes it.

The server refuses encrypted memories, system and app memories, and a source
or target you may not write. It also refuses when the target holds a deleted
node at an address the merge needs (MERGE_TARGET_LOC_TOMBSTONED, naming up to
25 locs): delete that node for good, or create a node there so it folds, then
run the merge again.

A large merge commits in batches. If it is refused part-way, the batches
already done stay done, the source is not deleted, and the error says how far
it got. Run the same merge again to finish it.

The merge changes both memories, so it asks for confirmation on a terminal;
--yes skips the question and is required when input is not a terminal
(otherwise exit 2).`,
		Example: `  hadron memory merge acme.com:old-kb --into acme.com:kb --yes
  hadron memory merge acme.com:old-kb --into acme.com:kb --field CONTENT --field TAGS --yes
  hadron memory merge acme.com:old-kb --into acme.com:kb --delete-source --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Usage errors before credentials or the network.
			if strings.TrimSpace(into) == "" {
				return exitcode.Newf(exitcode.Usage, "name the memory to merge into with --into <memory>. Nothing was sent")
			}
			include, err := cmdutil.ParseMergeFields(fields)
			if err != nil {
				return err
			}
			for _, ref := range []string{args[0], into} {
				if err := validateMemoryRef(ref); err != nil {
					return err
				}
			}
			// The server takes an ID or a URN for both, so no lookup round trip.
			source, target := cmdutil.CanonicalMemoryRef(args[0]), cmdutil.CanonicalMemoryRef(into)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Merge memory %s into %s? Every node moves or folds into the target; the source is left empty.", args[0], into)
			if deleteSource {
				prompt = fmt.Sprintf("Merge memory %s into %s and delete %s? This cannot be undone.", args[0], into, args[0])
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			del := deleteSource
			resp, err := gen.MergeMemories(cmd.Context(), client, &gen.MergeMemoriesInput{
				Source: source, Target: target, Include: include, DeleteSourceMem: &del,
			})
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.MergeMemories == nil {
				return exitcode.Newf(exitcode.Error, "server returned no memory")
			}
			m := dtoFromMemory(resp.MergeMemories)
			return output.Write(f.IOStreams, f.JSON, m, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ merged into", output.SafeCell(m.URN), output.SafeCell(m.Name))
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&into, "into", "", "the memory that receives the nodes (ID or URN)")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "field to fold into an existing node (repeatable): CONTENT|ABSTRACT|DESCRIPTION|TAGS|DATA|PROPERTIES|EDGES (default: all)")
	cmd.Flags().BoolVar(&deleteSource, "delete-source", false, "delete the source memory after a successful merge")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (required non-interactively)")
	return cmd
}
