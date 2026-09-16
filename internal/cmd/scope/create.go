package scope

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var (
		org         string
		app         string
		agent       string
		memories    []string
		description string
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a named scope over an ordered list of memories",
		Long: `Create a named scope owned by exactly one organization, App, or Agent.

--memory is REPEATABLE and ORDERED: the order you pass is the scope's order,
and it is what "which memory wins for this address" follows.

The name rules (1-64 characters of [a-z0-9_-], unique per owner, and never
"global" or "app" — those are reserved) are enforced by the server, so a
rejected name is reported in the server's own words.`,
		Example: `  hadron scope create research --owner-org acme.com -m hrn:mem:acme.com:papers -m hrn:mem:acme.com:notes
  hadron scope create team-lens --owner-app hrn:app:acme.com:dev-team -m hrn:mem:acme.com:dev --description "what the dev team reads"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Local flag validation runs BEFORE the client, which needs
			// credentials: otherwise a bad flag combination reports
			// AuthRequired instead of the usage error written here.
			ownerRef, ownerType, err := exactlyOneOwner(org, app, agent)
			if err != nil {
				return err
			}
			if len(memories) == 0 {
				return exitcode.Newf(exitcode.Usage,
					"a scope lists at least one memory — pass -m/--memory (repeatable; the order is the scope's order)")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			input := gen.CreateScopeInput{
				Name: args[0],
				// Order preserved exactly as given — this is an ordered list,
				// not a set, and reordering it changes which memory wins.
				MemoryRefs: memories,
			}
			switch ownerType {
			case gen.ScopeOwnerTypeOrganization:
				input.OrganizationRef = &ownerRef
			case gen.ScopeOwnerTypeApp:
				input.AppRef = &ownerRef
			case gen.ScopeOwnerTypeAgent:
				input.AgentRef = &ownerRef
			}
			if description != "" {
				input.Description = &description
			}
			resp, err := gen.CreateScope(cmd.Context(), client, &input)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.CreateScope == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server returned no scope for %q", args[0])
			}
			d := dtoFromFields(resp.CreateScope.ScopeFields)
			d.Memories = memoriesFromEntries(resp.CreateScope.ScopeMemories)
			return writeScope(f, d)
		},
	}
	cmd.Flags().StringVar(&org, "owner-org", "", "own the scope with this organization (ID or URN)")
	cmd.Flags().StringVar(&app, "owner-app", "", "own the scope with this App (ID or URN)")
	cmd.Flags().StringVar(&agent, "owner-agent", "", "own the scope with this installed Agent (ID or URN)")
	cmd.Flags().StringArrayVarP(&memories, "memory", "m", nil, "memory in the scope (ID or URN; repeatable; ORDER IS PRESERVED)")
	cmd.Flags().StringVar(&description, "description", "", "what this scope is for")
	return cmd
}
