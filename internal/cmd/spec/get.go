package spec

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var abstractOnly bool
	cmd := &cobra.Command{
		Use:   "get <citation>",
		Short: "Show a spec by its citation",
		Long: `Show a spec node: its abstract, edges, body, and a lint summary.

--abstract-only prints the metadata and abstract without the body.`,
		Example: `  hadron spec get msg:010:02 -m micromentor.org::platform-specs
  hadron spec get msg:010:02 -m micromentor.org::platform-specs --abstract-only --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			memURN, err := memoryURNFromFlag(memory)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			n, err := fetchSpecNode(cmd, client, memURN, args[0])
			if err != nil {
				return err
			}

			findings := lintNode(nodeFromGQL(n))
			dto := specDetailDTO{
				Citation:  n.Loc,
				MemoryID:  n.MemoryId,
				Name:      n.Name,
				NodeType:  n.NodeType,
				Tags:      n.Tags,
				Abstract:  n.Abstract,
				Lint:      findings,
				UpdatedAt: n.UpdatedAt,
			}
			if !abstractOnly {
				dto.Content = n.Content
			}
			for _, e := range n.OutgoingEdges {
				if e != nil && e.Target != nil {
					dto.Edges = append(dto.Edges, specEdgeDTO{Direction: "out", Label: e.Label, Loc: e.Target.Loc, MemoryID: e.Target.MemoryId})
				}
			}
			for _, e := range n.IncomingEdges {
				if e != nil && e.Source != nil {
					dto.Edges = append(dto.Edges, specEdgeDTO{Direction: "in", Label: e.Label, Loc: e.Source.Loc, MemoryID: e.Source.MemoryId})
				}
			}

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintln(w, n.Name)
				fmt.Fprintln(w, specNodeRef(memURN, n.Loc))
				if len(n.Tags) > 0 {
					fmt.Fprintf(w, "Tags: %s\n", strings.Join(n.Tags, ", "))
				}
				if n.Abstract != nil && strings.TrimSpace(*n.Abstract) != "" {
					fmt.Fprintf(w, "\nAbstract:\n%s\n", strings.TrimSpace(*n.Abstract))
				}
				if len(dto.Edges) > 0 {
					fmt.Fprintln(w, "\nEdges:")
					for _, e := range dto.Edges {
						arrow := "→"
						if e.Direction == "in" {
							arrow = "←"
						}
						fmt.Fprintf(w, "  %s %s  %s\n", arrow, e.Loc, e.Label)
					}
				}
				if len(findings) == 0 {
					fmt.Fprintln(w, "\nLint: ✓ ok")
				} else {
					fmt.Fprintf(w, "\nLint: %d finding(s)\n", len(findings))
					for _, fnd := range findings {
						fmt.Fprintf(w, "  [%s] %s: %s\n", fnd.Severity, fnd.Rule, fnd.Message)
					}
				}
				if !abstractOnly && n.Content != nil && strings.TrimSpace(*n.Content) != "" {
					fmt.Fprintf(w, "\n%s\n", *n.Content)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().BoolVar(&abstractOnly, "abstract-only", false, "print metadata + abstract, omit the body")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}
