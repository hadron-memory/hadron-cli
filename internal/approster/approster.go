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
	"github.com/hadron-memory/hadron-cli/internal/bootpreview"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// MemberDTO is the stable --json shape of one installed Agent. `personaRole`
// is the agent's persona DRESSING (null for an undressed agent — a Team
// Agent, or any agent in a non-team App); named identities are Workers and
// deliberately not part of this read.
type MemberDTO struct {
	bootpreview.Fields
	preview        *bootpreview.Preview
	ID             string  `json:"id"`
	URN            string  `json:"urn"`
	AgentName      string  `json:"agentName"`
	PersonaRole    *string `json:"personaRole"`
	Description    *string `json:"description"`
	OrganizationID *string `json:"organizationId"`
	CreatedAt      string  `json:"createdAt"`
	// The install's boot-prompt pointer (cli#916 C2, AppAgent.bootPromptRef),
	// joined by agent id. NOT omitempty: both null means unset, withheld, or
	// no readable join row for this Agent.
	BootPromptRef           *string `json:"bootPromptRef"`
	BootPromptTargetDeleted *bool   `json:"bootPromptTargetDeleted"`
	// Explicit nulls preserve the server's whole-App visibility decision.
	InstallMethod *string      `json:"installMethod"`
	Importer      *ImporterDTO `json:"importer"`
}

// ImporterDTO identifies the installed Agent supplying a derived installation.
type ImporterDTO struct {
	ID   string `json:"id"`
	URN  string `json:"urn"`
	Name string `json:"name"`
}

// Result is one App plus its installed agents.
type Result struct {
	AppName string
	AppURN  string
	Members []MemberDTO
	// Withheld is independent of the known installation count.
	Withheld  bool
	Installed *int
	Hidden    int
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
	return rosterDTO(&resp.App.AppAgentRosterData), nil
}

// FetchInstallations adds provenance only for the App listing. The role-warning
// consumer of Fetch keeps its original operation and provider requirements.
func FetchInstallations(ctx context.Context, client graphql.Client, appRef string) (*Result, error) {
	resp, err := gen.AppAgentInstallationRoster(ctx, client, appRef)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.App == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "App %q not found", appRef)
	}
	result := rosterDTO(&resp.App.AppAgentRosterData)
	type provenance struct {
		method   *string
		importer *ImporterDTO
	}
	byID := map[string]provenance{}
	for _, row := range resp.App.AppAgents {
		if row == nil || row.Agent == nil {
			continue
		}
		var p provenance
		if row.InstallMethod != nil {
			method := string(*row.InstallMethod)
			p.method = &method
		}
		if row.Importer != nil {
			p.importer = &ImporterDTO{ID: row.Importer.Id, URN: row.Importer.Urn, Name: row.Importer.Name}
		}
		byID[row.Agent.Id] = p
	}
	for i := range result.Members {
		p := byID[result.Members[i].ID]
		result.Members[i].InstallMethod, result.Members[i].Importer = p.method, p.importer
	}
	return result, nil
}

// FetchEffective opts only the listing into next-bind projection. The legacy
// operation is issued at most once, only for the exact initial missing-field
// validation refusal from the old provider.
func FetchEffective(ctx context.Context, client graphql.Client, appRef string) (*Result, error) {
	guard := &bootpreview.ValidationRead{Client: client}
	resp, err := gen.AppAgentEffectiveRoster(ctx, guard, appRef)
	if err != nil {
		if guard.MissingField(err, "AppAgentEffectiveRoster", "AppAgent") {
			return FetchInstallations(ctx, client, appRef)
		}
		return nil, api.MapError(err)
	}
	if resp == nil || resp.App == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "App %q not found", appRef)
	}
	result := rosterDTO(&resp.App.AppAgentRosterData)
	rows := map[string]*gen.AppAgentEffectiveRosterAppAppAgentsAppAgent{}
	for _, row := range resp.App.AppAgents {
		if row != nil && row.Agent != nil {
			rows[row.Agent.Id] = row
		}
	}
	for i := range result.Members {
		m := &result.Members[i]
		var outcome *gen.BootPromptPreviewFields
		if row := rows[m.ID]; row != nil {
			if row.InstallMethod != nil {
				v := string(*row.InstallMethod)
				m.InstallMethod = &v
			}
			if row.Importer != nil {
				m.Importer = &ImporterDTO{ID: row.Importer.Id, URN: row.Importer.Urn, Name: row.Importer.Name}
			}
			if row.BootPromptServe != nil {
				outcome = row.BootPromptServe
			}
		}
		preview := bootpreview.Project(outcome, "APP_AGENT", m.BootPromptRef)
		m.Fields = preview.Fields
		m.preview = &preview
	}
	return result, nil
}

// Complete reports whether counts grounded in this roster may drive warnings.
func (r *Result) Complete() bool { return !r.Withheld && r.Hidden == 0 }

func rosterDTO(app *gen.AppAgentRosterData) *Result {
	result := &Result{AppName: app.Name, AppURN: app.Urn, Withheld: app.Agents == nil || app.AppAgents == nil}
	if app.AppAgents != nil {
		n := len(app.AppAgents)
		result.Installed = &n
		for _, row := range app.AppAgents {
			if row == nil || row.Agent == nil {
				result.Hidden++
			}
		}
	}
	if result.Withheld {
		return result
	}

	type pointer struct {
		ref     *string
		deleted *bool
	}
	pointers := map[string]pointer{}
	for _, aa := range app.AppAgents {
		if aa == nil || aa.Agent == nil {
			continue
		}
		pointers[aa.Agent.Id] = pointer{aa.BootPromptRef, aa.BootPromptTargetDeleted}
	}
	members := []MemberDTO{}
	for _, a := range app.Agents {
		if a == nil {
			continue
		}
		members = append(members, MemberDTO{
			ID: a.Id, URN: a.Urn, AgentName: a.Name,
			PersonaRole: a.PersonaRole,
			Description: a.Description, OrganizationID: a.OrganizationId,
			CreatedAt:               a.CreatedAt,
			BootPromptRef:           pointers[a.Id].ref,
			BootPromptTargetDeleted: pointers[a.Id].deleted,
		})
	}
	result.Members = members
	return result
}

// Render writes the human table. The ROLE column stays even for an App with
// no dressed agents: a blank column is the honest answer for a plain
// single-agent App.
func Render(w io.Writer, r *Result) error {
	label := fmt.Sprintf("%d readable agents", len(r.Members))
	if r.Withheld {
		label = "roster withheld (not visible to you)"
	}
	if r.Installed != nil && (r.Hidden > 0 || r.Withheld) {
		label = fmt.Sprintf("%d installed (%d not visible to you)", *r.Installed, r.Hidden)
		if r.Withheld {
			label += "; roster withheld (not visible to you)"
		}
	}
	if _, err := fmt.Fprintf(w, "%s (%s) — %s\n", output.SafeCell(r.AppName), output.SafeCell(r.AppURN), label); err != nil {
		return err
	}
	if r.Withheld {
		return nil
	}
	header := "BOOT PROMPT"
	for _, m := range r.Members {
		if m.preview != nil {
			header = "BOOT PROMPT (NEXT BIND)"
			break
		}
	}
	t := output.NewTable(w, "AGENT", "ROLE", "AGENT URN", "ID", header, "INSTALL", "IMPORTER")
	for _, m := range r.Members {
		label := cmdutil.BootPromptLabel(m.BootPromptRef, m.BootPromptTargetDeleted)
		if m.preview != nil {
			label = m.preview.Label()
			if m.BootPromptTargetDeleted != nil && *m.BootPromptTargetDeleted {
				label += "; own pointer: " + cmdutil.BootPromptLabel(m.BootPromptRef, m.BootPromptTargetDeleted)
			}
		}
		t.Row(output.SafeCell(m.AgentName), output.SafeCell(dash(m.PersonaRole)), output.SafeCell(m.URN), output.SafeCell(m.ID), output.SafeCell(label), output.SafeCell(installLabel(m.InstallMethod)), output.SafeCell(importerLabel(m.Importer, m.InstallMethod)))
	}
	return t.Flush()
}

func dash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

func installLabel(method *string) string {
	if method == nil {
		return "?"
	}
	switch *method {
	case "ROW":
		return "installed"
	case "DERIVED":
		return "derived"
	default:
		return *method
	}
}

func importerLabel(importer *ImporterDTO, method *string) string {
	if method == nil {
		return "?"
	}
	if importer == nil {
		return "—"
	}
	return importer.Name + " (" + importer.URN + ")"
}
