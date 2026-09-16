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

An omitted flag preserves the current value — nothing is cleared by accident.`,
		Example: `  hadron scope update research --description "papers we actually cite"
  hadron scope update research -m hrn:mem:acme.com:papers -m hrn:mem:acme.com:notes
  hadron scope update research --name citations`,
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

			// Wire semantics: an OMITTED field preserves, an explicit null
			// clears. Every field is therefore set only when its flag was
			// actually CHANGED — not merely non-empty, which would make
			// `--description ""` indistinguishable from not passing it and
			// silently refuse a deliberate clear.
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
	cmd.Flags().StringVar(&description, "description", "", `what this scope is for ("" clears it)`)
	cmd.Flags().BoolVar(&byName, "by-name", false, byNameUsage)
	return cmd
}
