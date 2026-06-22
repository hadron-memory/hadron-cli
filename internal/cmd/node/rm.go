package node

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	var memory string
	cmd := &cobra.Command{
		Use:     "delete <node-urn> | <loc> -m <memory>",
		Aliases: []string{"rm"},
		Short:   "Delete a node",
		Example: `  hadron node rm acme.com:kb:findings:flaky-ci --yes
  hadron node rm findings:flaky-ci -m acme.com:kb --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			node, err := fetchNode(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "node "+args[0]); err != nil {
				return err
			}
			if _, err := gen.DeleteNode(cmd.Context(), client, node.Loc, node.MemoryId); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"loc": node.Loc, "memoryId": node.MemoryId, "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Deleted node %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org:memory) to resolve a bare <loc> against")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
