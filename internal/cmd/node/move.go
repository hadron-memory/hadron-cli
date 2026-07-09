package node

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdMove(f *cmdutil.Factory) *cobra.Command {
	var (
		memory   string
		toURN    string
		toMemory string
	)
	cmd := &cobra.Command{
		Use:   "move <node-urn> | <loc> -m <memory> (--to-urn <urn> | --to-memory <memory>)",
		Short: "Move a node to a new loc and/or memory",
		Long: `Relocate a node, keeping its id — so every incoming and outgoing edge
reference stays valid. Name the source by its fully-qualified URN
(<org>::<memory>::<loc>) or by a bare <loc> with -m/--memory.

Give the destination exactly one of:

  --to-urn <org>::<memory>::<loc>   the node's new full URN (new memory
                                    and/or loc).
  --to-memory <org::memory>         a destination memory; the node keeps its
                                    current loc, only its memory changes.

Fails loudly if a live node already occupies the destination.`,
		Example: `  hadron node move acme.com::kb::findings:flaky-ci --to-urn acme.com::kb::archive:flaky-ci
  hadron node move findings:flaky-ci -m acme.com::kb --to-memory acme.com::archive`,
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
			resp, err := gen.MoveNode(cmd.Context(), client, sourceRef, targetUrn, targetMemoryRef)
			if err != nil {
				return api.MapError(err)
			}
			dto := moveDTO(resp.MoveNode)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ moved", "to: "+dto.URN)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> source against")
	cmd.Flags().StringVar(&toURN, "to-urn", "", "destination node URN <org>::<memory>::<loc> (new memory and/or loc)")
	cmd.Flags().StringVar(&toMemory, "to-memory", "", "destination memory (org::memory); keeps the current loc")
	return cmd
}

// relocationDestination validates and normalizes the shared destination flags
// of `node move` and `node clone`. It enforces the "exactly one destination"
// rule client-side (the server enforces it too, but a local error is
// friendlier) and is pure — no network — so callers run it before requiring an
// authenticated client. Inputs are trimmed first, so a whitespace-only flag is
// treated as unset (not smuggled through as an empty ref). Exactly one of the
// returned targetUrn / targetMemoryRef is non-nil.
func relocationDestination(toURN, toMemory string) (*string, *string, error) {
	toURN = strings.TrimSpace(toURN)
	toMemory = strings.TrimSpace(toMemory)
	switch {
	case toURN != "" && toMemory != "":
		return nil, nil, exitcode.Newf(exitcode.Usage, "--to-urn and --to-memory are mutually exclusive")
	case toURN == "" && toMemory == "":
		return nil, nil, exitcode.Newf(exitcode.Usage, "specify a destination: --to-urn <org>::<memory>::<loc> or --to-memory <org>::<memory>")
	}
	if toURN != "" {
		urn, err := cmdutil.CanonicalNodeURN(toURN)
		if err != nil {
			return nil, nil, err
		}
		return &urn, nil, nil
	}
	memRef := cmdutil.CanonicalMemoryRef(toMemory)
	return nil, &memRef, nil
}

func moveDTO(n *gen.MoveNodeMoveNode) nodeDTO {
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
