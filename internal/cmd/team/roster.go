package team

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// rosterMemberDTO is the stable --json shape of one roster row: an Agent
// installed in the team App. `personaName`/`personaRole` are null for an
// installed agent that is not a persona (the Team Agent itself, typically) —
// deliberately not filtered out, because "the Team Agent and no personas yet"
// is a true and useful answer that an empty list would hide.
type rosterMemberDTO struct {
	ID             string  `json:"id"`
	URN            string  `json:"urn"`
	AgentName      string  `json:"agentName"`
	PersonaName    *string `json:"personaName"`
	PersonaRole    *string `json:"personaRole"`
	Description    *string `json:"description"`
	OrganizationID *string `json:"organizationId"`
	CreatedAt      string  `json:"createdAt"`
}

// newCmdRoster builds `hadron team roster` — the roster read the team command
// group should have had from the start (#383).
//
// Under cor:agt:020:01 the roster IS the AppAgent join, and until this command
// existed only raw GraphQL exposed it. `persona list` and `agent list` are
// narrowings of the caller's whole READABLE agent set, so they answer "every
// persona I can read" — and because `--app` is the persistent App-CONTEXT flag
// rather than a filter, passing it changed nothing, which let them name a
// persona installed in another org's App as if it were on this team.
func newCmdRoster(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "roster",
		Aliases: []string{"members"},
		Short:   "Who is on this team — the App's installed agents",
		Long: `List the agents installed in the team App: the roster, read from the
AppAgent join (cor:agt:020:01) rather than narrowed client-side.

This is the command that answers "who is on this team?". ` + "`persona list`" + `
answers a different question — every persona YOU can read, across every org
and App — and ` + "`--app`" + ` does not scope it (it is the App-context flag, not a
filter), so it must not be read as a roster.

Rows with no persona name are installed agents that are not personas — the
Team Agent itself, typically. The team App resolves from --app (or the
configured App context), falling back to the worktree binding's team memory.`,
		Example: `  hadron team roster --app acme.com::eng-team
  hadron team roster --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			appRef, err := resolveTeamApp(ctx, f, b)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamRoster(ctx, client, appRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp.App == nil {
				return exitcode.Newf(exitcode.NotFound, "team App %q not found", appRef)
			}
			members := []rosterMemberDTO{}
			for _, a := range resp.App.Agents {
				if a == nil {
					continue
				}
				members = append(members, rosterMemberDTO{
					ID: a.Id, URN: a.Urn, AgentName: a.Name,
					PersonaName: a.PersonaName, PersonaRole: a.PersonaRole,
					Description: a.Description, OrganizationID: a.OrganizationId,
					CreatedAt: a.CreatedAt,
				})
			}
			return output.Write(f.IOStreams, f.JSON, members, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "%s (%s) — %d installed\n", resp.App.Name, resp.App.Urn, len(members)); err != nil {
					return err
				}
				t := output.NewTable(w, "PERSONA", "ROLE", "AGENT", "AGENT URN", "ID")
				for _, m := range members {
					t.Row(dash(m.PersonaName), dash(m.PersonaRole), m.AgentName, m.URN, m.ID)
				}
				return t.Flush()
			})
		},
	}
	return cmd
}
