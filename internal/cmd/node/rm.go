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
		Use:     "rm <loc>",
		Aliases: []string{"delete"},
		Short:   "Delete a node",
		Example: `  hadron node rm findings:flaky-ci -m acme.com:kb --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			node, err := fetchNode(cmd, client, args[0], memory)
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "node "+args[0]); err != nil {
				return err
			}
			if _, err := gen.DeleteNode(cmd.Context(), client, node.Loc, node.MemoryID); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"loc": node.Loc, "memoryId": node.MemoryID, "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Deleted node %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "resolve the loc within this memory (ID or URN)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
