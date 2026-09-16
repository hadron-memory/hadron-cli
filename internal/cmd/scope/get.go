package scope

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	var byName bool
	cmd := &cobra.Command{
		Use:   "get <name|id>",
		Short: "Show a scope and the memories it lists",
		Long: `Show a scope and the memories it lists, in scope order.

A NAME resolves in an App's context (App > Agent > organization), taken from
the persistent --app flag or the active App. An ID needs no context.

Only the memories you may read are listed; the rest are reported as a count.`,
		Example: `  hadron scope get research --app hrn:app:acme.com:dev-team
  hadron scope get 01a0a702033c7099b23cbd358cd0c269`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := resolveScopeID(cmd, f, client, args[0], byName)
			if err != nil {
				return err
			}
			resp, err := gen.GetScope(cmd.Context(), client, id)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.Scope == nil {
				// Missing and unreadable are the same null by design ("no
				// existence disclosure"), so the message asserts only that we
				// could not read it — not that it does not exist.
				return exitcode.Newf(exitcode.NotFound, "no scope %q is readable here", args[0])
			}
			d := dtoFromFields(resp.Scope.ScopeFields)
			d.Memories = memoriesFromEntries(resp.Scope.ScopeMemories)
			return writeScope(f, d)
		},
	}
	cmd.Flags().BoolVar(&byName, "by-name", false, byNameUsage)
	return cmd
}
