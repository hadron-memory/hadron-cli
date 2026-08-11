// Package team implements `hadron team ...` — the team-coordination
// surface (#369): personas and coding sessions. A persona IS an Agent
// (personaName/personaRole/personaPrompt are pure metadata — no behavior
// forks on "is a persona"), and "taken right now" derives from an active
// Session. Group chat and the worklog are later slices of #369.
package team

import (
	"context"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// NewCmdTeam builds the `hadron team` command group.
func NewCmdTeam(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team <command>",
		Short: "Team coordination: personas and coding sessions",
		Long: `Coordinate a team of humans and AI agents. A persona is a named AI team
member ("Iris") that is re-driven across many sessions — by the same or a
different human. A session binds the current git worktree to a persona and
records provenance (host, tool, transcript path) so a merged PR traces back
to the session that produced it.`,
	}
	cmd.AddCommand(newCmdPersona(f))
	cmd.AddCommand(newCmdSession(f))
	return cmd
}

// personaDTO is the stable --json shape for a persona.
type personaDTO struct {
	ID             string  `json:"id"`
	URN            string  `json:"urn"`
	AgentName      string  `json:"agentName"`
	PersonaName    string  `json:"personaName"`
	PersonaRole    *string `json:"personaRole"`
	PersonaPrompt  *string `json:"personaPrompt"`
	Description    *string `json:"description"`
	OrganizationID *string `json:"organizationId"`
	CreatedAt      string  `json:"createdAt"`
}

func personaDTOFromFields(a gen.PersonaAgentFields) personaDTO {
	name := ""
	if a.PersonaName != nil {
		name = *a.PersonaName
	}
	return personaDTO{
		ID: a.Id, URN: a.Urn, AgentName: a.Name, PersonaName: name,
		PersonaRole: a.PersonaRole, PersonaPrompt: a.PersonaPrompt,
		Description: a.Description, OrganizationID: a.OrganizationId, CreatedAt: a.CreatedAt,
	}
}

const rosterPageSize = 200

// scanPersonaAgents pages the agents list to exhaustion (the server caps an
// unbounded list at one default page) and returns the persona rows — agents
// with a non-empty personaName. AgentFilter has no persona clause, so the
// narrowing is client-side by design (#928: personas are metadata, not a
// server-side kind).
//
// Two passes: the unfiltered list is the caller's MEMBER-ORG scope only,
// while a persona minted without --org is a user-owned, org-less agent that
// only `filter.ownedByMe: true` returns (#782) — so without an explicit org
// scope both slices are read and merged (dedup by id, defensively; the
// slices are disjoint by construction).
func scanPersonaAgents(ctx context.Context, client graphql.Client, orgID *string) ([]gen.PersonaAgentFields, error) {
	personas := []gen.PersonaAgentFields{}
	seen := map[string]bool{}
	collect := func(filter *gen.AgentFilter) error {
		limit := rosterPageSize
		for offset := 0; ; {
			off := offset
			resp, err := gen.PersonaAgents(ctx, client, orgID, filter, &limit, &off)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Agents == nil {
				return nil
			}
			for _, item := range resp.Agents.Items {
				if item == nil || seen[item.Id] {
					continue
				}
				seen[item.Id] = true
				if item.PersonaName != nil && *item.PersonaName != "" {
					personas = append(personas, item.PersonaAgentFields)
				}
			}
			offset += len(resp.Agents.Items)
			if len(resp.Agents.Items) < rosterPageSize || offset >= resp.Agents.Total {
				return nil
			}
		}
	}
	if err := collect(nil); err != nil {
		return nil, err
	}
	if orgID == nil {
		ownedByMe := true
		if err := collect(&gen.AgentFilter{OwnedByMe: &ownedByMe}); err != nil {
			return nil, err
		}
	}
	return personas, nil
}

// resolvePersona turns a persona name or an agent ref (ID / URN) into the
// persona's agent row. A ref containing ":" goes straight to the server; a
// bare argument is matched case-insensitively against the roster's persona
// names first (the server's uniqueness is per owner, so the same name can
// exist in two orgs — that ambiguity is an error asking for --org or a URN),
// then falls back to an agent-ID lookup.
func resolvePersona(ctx context.Context, client graphql.Client, orgID *string, arg string) (gen.PersonaAgentFields, error) {
	var zero gen.PersonaAgentFields
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return zero, exitcode.Newf(exitcode.Usage, "empty persona reference")
	}
	if strings.Contains(arg, ":") {
		return getPersonaAgent(ctx, client, arg)
	}
	personas, err := scanPersonaAgents(ctx, client, orgID)
	if err != nil {
		return zero, err
	}
	matches := []gen.PersonaAgentFields{}
	for _, p := range personas {
		if p.PersonaName != nil && strings.EqualFold(*p.PersonaName, arg) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		// Not a known persona name — maybe a bare agent ID.
		if p, err := getPersonaAgent(ctx, client, arg); err == nil {
			return p, nil
		}
		return zero, exitcode.Newf(exitcode.NotFound, "no persona named %q — `hadron team persona list` shows the roster", arg)
	default:
		urns := make([]string, len(matches))
		for i, m := range matches {
			urns[i] = m.Urn
		}
		return zero, exitcode.Newf(exitcode.Conflict,
			"persona name %q exists for more than one owner (%s) — pass --org, or the agent URN", arg, strings.Join(urns, ", "))
	}
}

// getPersonaAgent fetches one agent by ref and requires it to be a persona.
func getPersonaAgent(ctx context.Context, client graphql.Client, ref string) (gen.PersonaAgentFields, error) {
	var zero gen.PersonaAgentFields
	resp, err := gen.GetPersonaAgent(ctx, client, ref)
	if err != nil {
		return zero, api.MapError(err)
	}
	if resp.Agent == nil {
		return zero, exitcode.Newf(exitcode.NotFound, "persona %q not found", ref)
	}
	if resp.Agent.PersonaName == nil || *resp.Agent.PersonaName == "" {
		return zero, exitcode.Newf(exitcode.Usage, "agent %s has no persona name — not a persona (see `hadron agent get`)", resp.Agent.Urn)
	}
	return resp.Agent.PersonaAgentFields, nil
}

// optStr returns a pointer for a non-empty value, else nil (omitted on the wire).
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
