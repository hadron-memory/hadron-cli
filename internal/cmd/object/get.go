package object

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <ref>",
		Short: "Read one object by id or node URN",
		Long: `Print one record by its id (shown by object create and object find) or its node
URN. Exits 4 (not found) when nothing readable has that id, or when it names a
node that is not a record.`,
		Example: `  hadron object get 01a189a8175c7145b6c3680628ef0101
  hadron object get hrn:node:acme.com:market:competitor:letta --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GetObject(cmd.Context(), client, cmdutil.CanonicalNodeRef(args[0]))
			if err != nil {
				return api.MapError(err)
			}
			// object(ref:) returns JSON null (not an error) when absent or not an
			// object — a nil pointer, or the literal `null`.
			if resp.Object == nil || string(*resp.Object) == "null" {
				return exitcode.Newf(exitcode.NotFound, "object %q not found", args[0])
			}
			return writeObject(f, *resp.Object)
		},
	}
}
