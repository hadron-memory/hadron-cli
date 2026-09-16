package scope

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var (
		byName      bool
		newName     string
		memories    []string
		description string
	)
	cmd := &cobra.Command{
		Use:   "update <name|id>",
		Short: "Rename a scope, replace its memories, or change its description",
		Long: `Update a scope.

--memory REPLACES the scope's memory list; it does not append to it. Pass every
memory the scope should end up with, in the order you want them. Omitting the
flag leaves the existing list untouched.

An omitted flag preserves the current value — nothing is cleared by accident.

There is currently no way to CLEAR a description: the server clears on an
explicit null, which this operation cannot send, so --description "" stores an
empty string rather than removing the value.`,
		Example: `  hadron scope update research --description "papers we actually cite"
  hadron scope update research -m hrn:mem:acme.com:papers -m hrn:mem:acme.com:notes
  hadron scope update research --name citations`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The input is built and validated BEFORE the client and before
			// ref resolution: resolveScopeID can issue a ScopeExplain round
			// trip, so a no-op update would otherwise report auth, not-found
			// or network errors instead of the local usage error below —
			// after a lookup that cannot change the outcome.
			//
			// Wire semantics: an OMITTED field preserves, an explicit null
			// clears. Every field is therefore set only when its flag was
			// actually CHANGED, not merely non-empty.
			input := gen.UpdateScopeInput{}
			touched := false
			if cmd.Flags().Changed("name") {
				input.Name = &newName
				touched = true
			}
			if cmd.Flags().Changed("description") {
				input.Description = &description
				touched = true
			}
			if cmd.Flags().Changed("memory") {
				if len(memories) == 0 {
					return exitcode.Newf(exitcode.Usage,
						"--memory replaces the scope's memory list, so it cannot be empty — omit it to leave the list unchanged")
				}
				input.MemoryRefs = memories
				touched = true
			}
			if !touched {
				return exitcode.Newf(exitcode.Usage,
					"nothing to update — pass --name, --description or -m/--memory")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := resolveScopeID(cmd, f, client, args[0], byName)
			if err != nil {
				return err
			}

			resp, err := gen.UpdateScope(cmd.Context(), client, id, &input)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.UpdateScope == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server returned no scope for %q", args[0])
			}
			d := dtoFromFields(resp.UpdateScope.ScopeFields)
			d.Memories = memoriesFromEntries(resp.UpdateScope.ScopeMemories)
			return writeScope(f, d)
		},
	}
	cmd.Flags().StringVar(&newName, "name", "", "rename the scope")
	cmd.Flags().StringArrayVarP(&memories, "memory", "m", nil, "memory in the scope (ID or URN; repeatable; REPLACES the list, order preserved)")
	// NOT documented as clearing. The server clears on an explicit null; with
	// the omitempty this operation needs to preserve unset fields, a nil
	// pointer is OMITTED rather than sent as null, so `--description ""`
	// sends "" and stores a blank string. Promising a clear here would be a
	// claim the wire cannot honour (@copilot, #594).
	cmd.Flags().StringVar(&description, "description", "", "what this scope is for (cannot be cleared; see --help)")
	cmd.Flags().BoolVar(&byName, "by-name", false, byNameUsage)
	return cmd
}
