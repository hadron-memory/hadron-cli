package memory

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
	cmd := &cobra.Command{
		Use:     "rm <memoryRef>",
		Aliases: []string{"delete"},
		Short:   "Delete a memory and all its nodes",
		Long: `Delete a memory, with every node in it and their history. No command brings
a deleted memory back, so save what you may need first: memory export writes
its nodes to Markdown files, and memory clone copies it into a new memory.

It asks "Delete memory <memoryRef> and all its nodes? This cannot be undone.
(y/N)" on a terminal. --yes skips the question, and is required when input is
not a terminal (otherwise exit 2). On success it prints "✓ Deleted memory
<memoryRef>".

Who may delete it: the owner of a personal, private or user-owned memory; for
an organization's memory, an organization admin. An Agent's own system memory
is deleted only by deleting the Agent, and a memory that holds a chat Channel
is refused until the Channel is deleted.`,
		Example: `  hadron memory export acme.com:scratch --out ./scratch-backup
  hadron memory rm acme.com:scratch --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "memory "+args[0]+" and all its nodes"); err != nil {
				return err
			}
			if _, err := gen.DeleteMemory(cmd.Context(), client, args[0]); err != nil {
				return api.MapError(err)
			}
			dto := map[string]string{"memory": args[0], "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Deleted memory %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (required when input is not a terminal)")
	return cmd
}
