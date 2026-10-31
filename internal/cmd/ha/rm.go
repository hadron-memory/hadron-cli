package ha

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// deletedDTO is the stable --json shape for a delete.
type deletedDTO struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func newCmdRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <id>",
		Aliases: []string{"delete"},
		Short:   "Delete a registered Home Assistant instance (org admin)",
		Long: `Delete a registered Home Assistant instance for good. You must be an admin or
owner of its organization. <id> is the ID column of "hadron ha list".

This cannot be undone, and nothing is repaired: a node whose data.tools still
names one of its ops (ha__<slug>__<op>) makes its runs fail until you edit it.
To stop the instance being used but keep it, use "ha update <id> --disabled".

Asks for confirmation on a terminal; elsewhere --yes is required, and without
it nothing is sent. If you get "not found", either no instance has that ID or
it belongs to an organization you cannot manage; the server does not say which.

Prints "✓ deleted Home Assistant instance <id>"; --json prints
{"id": "<id>", "status": "deleted"}.`,
		Example: `  hadron ha rm <instance-id>
  hadron ha rm <instance-id> --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, fmt.Sprintf("Home Assistant instance %s", id)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			check := "hadron ha get " + cmdutil.ShellQuote(id)
			resp, err := gen.DeleteHomeAssistantInstance(cmd.Context(), client, id)
			if err != nil {
				return writeError(err, check, "")
			}
			// The server refuses a missing row with NOT_FOUND; false is not a
			// delete, so it is never reported as one.
			if resp == nil || !resp.DeleteHomeAssistantInstance {
				return exitcode.Newf(exitcode.Error, "the server did not confirm the deletion of %s; run `%s` to see whether it is still registered", id, check)
			}
			dto := deletedDTO{ID: id, Status: "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ deleted Home Assistant instance %s\n", output.SafeCell(id))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
