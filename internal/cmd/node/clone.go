package node

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdClone(f *cmdutil.Factory) *cobra.Command {
	var (
		memory   string
		toURN    string
		toMemory string
	)
	cmd := &cobra.Command{
		Use:   "clone <node-urn> | <loc> -m <memory> (--to-urn <urn> | --to-memory <memory>)",
		Short: "Copy a node to a new loc and/or memory",
		Long: `Copy a node to a new location, returning the NEW node (a fresh id). Name
the source by its fully-qualified URN (<org>::<memory>::<loc>) or by a
bare <loc> with -m/--memory.

Give the destination exactly one of:

  --to-urn <org>::<memory>::<loc>   the clone's full URN (new memory
                                    and/or loc).
  --to-memory <org::memory>         a destination memory; the clone keeps the
                                    source's loc, only its memory changes.

Copies the node's own fields plus the outgoing edges that naturally resolve
at the destination; incoming edges are not copied. Fails loudly if a node
already occupies the destination loc.`,
		Example: `  hadron node clone acme.com::kb::templates:base --to-urn acme.com::kb::findings:new
  hadron node clone templates:base -m acme.com::kb --to-memory acme.com::sandbox`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate the destination flags before touching the network, so a
			// bad flag combo is a usage error even when unauthenticated.
			targetUrn, targetMemoryRef, err := relocationDestination(toURN, toMemory)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			sourceRef, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.CloneNode(cmd.Context(), client, sourceRef, targetUrn, targetMemoryRef)
			if err != nil {
				return api.MapError(err)
			}
			dto := cloneDTO(resp.CloneNode)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ cloned", "to: "+dto.URN)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> source against")
	cmd.Flags().StringVar(&toURN, "to-urn", "", "destination node URN <org>::<memory>::<loc> (new memory and/or loc)")
	cmd.Flags().StringVar(&toMemory, "to-memory", "", "destination memory (org::memory); keeps the source's loc")
	return cmd
}

func cloneDTO(n *gen.CloneNodeCloneNode) nodeDTO {
	return nodeDTO{
		ID:         n.Id,
		URN:        n.Urn,
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
