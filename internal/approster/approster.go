// Package approster holds the App-agents read — the AppAgent join
// (cor:dmo:050:03) that answers "which Agents are installed in this App?".
//
// Under the Worker model (#428, cor:agt:020:01) this is the INSTALL roster:
// the agents available to cast. The App's STAFF — its named workers — is
// `hadron team worker list`, which reads the Workers query. `hadron app agent
// list` is this read's command surface; the former second surface,
// `hadron team roster`, retired with the persona model (#407/#428).
//
// The DTO is an explicit struct, never a genqlient type, so regeneration
// cannot move it (it is the one DTO shared across command packages, and
// deliberately so).
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

// MemberDTO is the stable --json shape of one installed Agent. `personaRole`
// is the agent's persona DRESSING (null for an undressed agent — a Team
// Agent, or any agent in a non-team App); named identities are Workers and
// deliberately not part of this read.
type MemberDTO struct {
	ID             string  `json:"id"`
	URN            string  `json:"urn"`
	AgentName      string  `json:"agentName"`
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
	resp, err := gen.AppAgentRoster(ctx, client, appRef)
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
			PersonaRole: a.PersonaRole,
			Description: a.Description, OrganizationID: a.OrganizationId,
			CreatedAt: a.CreatedAt,
		})
	}
	return &Result{AppName: resp.App.Name, AppURN: resp.App.Urn, Members: members}, nil
}

// Render writes the human table. The ROLE column stays even for an App with
// no dressed agents: a blank column is the honest answer for a plain
// single-agent App.
func Render(w io.Writer, r *Result) error {
	if _, err := fmt.Fprintf(w, "%s (%s) — %d installed\n", r.AppName, r.AppURN, len(r.Members)); err != nil {
		return err
	}
	t := output.NewTable(w, "AGENT", "ROLE", "AGENT URN", "ID")
	for _, m := range r.Members {
		t.Row(m.AgentName, dash(m.PersonaRole), m.URN, m.ID)
	}
	return t.Flush()
}

func dash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}
