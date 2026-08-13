// Package approster holds the App-agents read — the AppAgent join
// (cor:dmo:050:03) that answers "which Agents are installed in this App?".
//
// It lives outside both command groups because the same read is correct under
// two nouns (#408): `hadron team roster` is the team-flavoured name, and
// `hadron app agent list` is where someone holding a plain multi-agent App
// looks. Multi-agent Apps predate the team feature (spec 023), so filing the
// only answer under `team` sent those callers to `agent list --app`, which
// looks right and silently returns every readable agent — the #383 trap.
//
// The DTO is defined here rather than per command deliberately: the two
// surfaces must not drift into two --json shapes for one read. It is still an
// explicit struct, never a genqlient type, so regeneration cannot move it.
package approster

import (
	"context"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// MemberDTO is the stable --json shape of one installed Agent.
// `personaName`/`personaRole` are null for an installed agent that is not a
// persona (a Team Agent, or any agent in a non-team App) — deliberately not
// filtered out, because "the Team Agent and no personas yet" is a true and
// useful answer that an empty list would hide, and a plain WORKSTATION App has
// no personas at all.
type MemberDTO struct {
	ID             string  `json:"id"`
	URN            string  `json:"urn"`
	AgentName      string  `json:"agentName"`
	PersonaName    *string `json:"personaName"`
	PersonaRole    *string `json:"personaRole"`
	Description    *string `json:"description"`
	OrganizationID *string `json:"organizationId"`
	CreatedAt      string  `json:"createdAt"`
}

// Result is one App plus its installed agents.
type Result struct {
	AppName string
	AppURN  string
	Members []MemberDTO
}

// Fetch reads the AppAgent join for one App. No paging: App.agents is the
// 023-app-shape convenience over App.appAgents, and an App's installed set is
// small and bounded — unlike agents().
func Fetch(ctx context.Context, client graphql.Client, appRef string) (*Result, error) {
	resp, err := gen.TeamRoster(ctx, client, appRef)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.App == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "App %q not found", appRef)
	}
	members := []MemberDTO{}
	for _, a := range resp.App.Agents {
		if a == nil {
			continue
		}
		members = append(members, MemberDTO{
			ID: a.Id, URN: a.Urn, AgentName: a.Name,
			PersonaName: a.PersonaName, PersonaRole: a.PersonaRole,
			Description: a.Description, OrganizationID: a.OrganizationId,
			CreatedAt: a.CreatedAt,
		})
	}
	return &Result{AppName: resp.App.Name, AppURN: resp.App.Urn, Members: members}, nil
}

// Render writes the human table. The persona columns stay even for an App with
// no personas: a blank column is the honest answer for a plain single-agent
// App, and hiding them would make the two surfaces print differently.
func Render(w io.Writer, r *Result) error {
	if _, err := fmt.Fprintf(w, "%s (%s) — %d installed\n", r.AppName, r.AppURN, len(r.Members)); err != nil {
		return err
	}
	t := output.NewTable(w, "PERSONA", "ROLE", "AGENT", "AGENT URN", "ID")
	for _, m := range r.Members {
		t.Row(dash(m.PersonaName), dash(m.PersonaRole), m.AgentName, m.URN, m.ID)
	}
	return t.Flush()
}

func dash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}
