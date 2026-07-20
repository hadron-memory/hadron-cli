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
		Long: `Read an object by its id (the value printed on create) or its node URN, and
print the flat record { id, type, ...fields }. Exits 4 (not found) when the ref
names nothing readable or a node that isn't an object.`,
		Example: `  hadron object get 019f7d…            # by object id
  hadron object get acme.com::market::competitor:letta --json`,
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
