// Package agent implements `hadron agent ...` — agent lifecycle management.
package agent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// agentDTO is the stable --json shape for an agent.
type agentDTO struct {
	ID             string   `json:"id"`
	URN            string   `json:"urn"`
	Name           string   `json:"name"`
	Description    *string  `json:"description"`
	Type           string   `json:"type"`
	Visibility     string   `json:"visibility"`
	OrganizationID *string  `json:"organizationId"`
	Surfaces       []string `json:"surfaces"`
	SystemMemoryID *string  `json:"systemMemoryId"`
	SystemPrompt   *string  `json:"systemPrompt"`
	AiProvider     *string  `json:"aiProvider"`
	AiModel        *string  `json:"aiModel"`
	HasAiApiKey    bool     `json:"hasAiApiKey"`
	PersonaRole    *string  `json:"personaRole"`
	PersonaPrompt  *string  `json:"personaPrompt"`
	CreatedAt      string   `json:"createdAt"`
}

func agentDTOFromFields(a gen.AgentFields) agentDTO {
	surfaces := a.Surfaces
	if surfaces == nil {
		surfaces = []string{}
	}
	return agentDTO{
		ID: a.Id, URN: a.Urn, Name: a.Name, Description: a.Description,
		Type: string(a.Type), Visibility: string(a.Visibility), OrganizationID: a.OrganizationId,
		Surfaces: surfaces, SystemMemoryID: a.SystemMemoryId, SystemPrompt: a.SystemPrompt,
		AiProvider: a.AiProvider, AiModel: a.AiModel, HasAiApiKey: a.HasAiApiKey,
		PersonaRole: a.PersonaRole, PersonaPrompt: a.PersonaPrompt, CreatedAt: a.CreatedAt,
	}
}

func dash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

// parseAgentType returns nil for an unset flag (so it's omitted), else the enum.
func parseAgentType(s string) (*gen.AgentType, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ASSISTANT":
		t := gen.AgentTypeAssistant
		return &t, nil
	case "CHATBOT":
		t := gen.AgentTypeChatbot
		return &t, nil
	default:
		return nil, exitcode.Newf(exitcode.Usage, "invalid --type %q (want ASSISTANT or CHATBOT)", s)
	}
}

// ParseVisibility is exported for reuse outside this package (#405 precedent)
// — one place to change if the enum grows a member.
func ParseVisibility(s string) (*gen.AgentVisibility, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ORGANIZATION":
		v := gen.AgentVisibilityOrganization
		return &v, nil
	case "PERSONAL":
		v := gen.AgentVisibilityPersonal
		return &v, nil
	case "PUBLIC":
		v := gen.AgentVisibilityPublic
		return &v, nil
	default:
		return nil, exitcode.Newf(exitcode.Usage, "invalid --visibility %q (want ORGANIZATION, PERSONAL, or PUBLIC)", s)
	}
}

// NewCmdAgent builds the `hadron agent` command group.
func NewCmdAgent(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "agent <command>",
		Aliases: []string{"agents"},
		Short:   "Work with agents",
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdRm(f))
	return cmd
}

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var org, typ, vis string
	var limit, offset int
	var public bool
	cmd := &cobra.Command{
		Use:     "list [--org <id>] [--type <t>] [--visibility <v>] | --public [--type <t>]",
		Aliases: []string{"ls"},
		Short:   "List agents",
		Long: `List agents. By default this is the member-scoped view — agents in orgs you
belong to.

--public instead lists the cross-org marketplace slice: every live PUBLIC
agent, readable without org membership (a foreign public agent you can grab the
URN of to subscribe/install). It's a separate surface, so --org and
--visibility don't apply to it; --type still filters.

--app does NOT narrow this listing: it is the persistent App-context flag,
not a filter, so the same rows come back for any App. For the agents
installed in one App, use ` + "`hadron team roster --app <ref>`" + `, which reads
the AppAgent join.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			f.NoteAppIsContextOnly("agents you can read")
			if limit < 0 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit and --offset must be non-negative")
			}
			if public && (org != "" || vis != "") {
				return exitcode.Newf(exitcode.Usage, "--public is the cross-org PUBLIC slice — --org and --visibility don't apply to it")
			}
			at, err := parseAgentType(typ)
			if err != nil {
				return err
			}
			av, err := ParseVisibility(vis)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var lim, off *int
			if cmd.Flags().Changed("limit") {
				lim = &limit
			}
			if cmd.Flags().Changed("offset") {
				off = &offset
			}

			agents := []agentDTO{}
			if public {
				// PublicAgentFilter is deliberately narrower than AgentFilter:
				// only --type narrows the marketplace slice.
				var filter *gen.PublicAgentFilter
				if at != nil {
					filter = &gen.PublicAgentFilter{Type: at}
				}
				resp, err := gen.PublicAgents(cmd.Context(), client, filter, lim, off)
				if err != nil {
					return api.MapError(err)
				}
				if resp.PublicAgents != nil {
					for _, a := range resp.PublicAgents.Items {
						if a == nil {
							continue
						}
						agents = append(agents, agentDTOFromFields(a.AgentFields))
					}
				}
			} else {
				var filter *gen.AgentFilter
				if at != nil || av != nil {
					filter = &gen.AgentFilter{Type: at, Visibility: av}
				}
				var orgPtr *string
				if org != "" {
					orgPtr = &org
				}
				resp, err := gen.Agents(cmd.Context(), client, orgPtr, filter, lim, off)
				if err != nil {
					return api.MapError(err)
				}
				if resp.Agents != nil {
					for _, a := range resp.Agents.Items {
						if a == nil {
							continue
						}
						agents = append(agents, agentDTOFromFields(a.AgentFields))
					}
				}
			}
			return output.Write(f.IOStreams, f.JSON, agents, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "URN", "NAME", "TYPE", "VISIBILITY")
				for _, a := range agents {
					t.Row(a.ID, a.URN, a.Name, a.Type, a.Visibility)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "restrict to one organization (ID)")
	cmd.Flags().StringVar(&typ, "type", "", "filter by type: ASSISTANT or CHATBOT")
	cmd.Flags().StringVar(&vis, "visibility", "", "filter by visibility: ORGANIZATION, PERSONAL, or PUBLIC")
	cmd.Flags().BoolVar(&public, "public", false, "list the cross-org PUBLIC marketplace slice instead of your member-scoped agents")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results (server default when unset)")
	cmd.Flags().IntVar(&offset, "offset", 0, "results to skip")
	return cmd
}

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "get <ref>",
		Short:   "Show an agent (by ID or URN)",
		Example: `  hadron agent get hrn:agent:acme.com:support-bot --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GetAgent(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.Agent == nil {
				return exitcode.Newf(exitcode.NotFound, "agent %q not found", args[0])
			}
			dto := agentDTOFromFields(resp.Agent.AgentFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "%s\n  urn: %s\n  id: %s\n  type: %s   visibility: %s\n", dto.Name, dto.URN, dto.ID, dto.Type, dto.Visibility)
				fmt.Fprintf(w, "  description: %s\n  system memory: %s\n  ai: %s/%s (key: %v)\n",
					dash(dto.Description), dash(dto.SystemMemoryID), dash(dto.AiProvider), dash(dto.AiModel), dto.HasAiApiKey)
				if len(dto.Surfaces) > 0 {
					fmt.Fprintf(w, "  surfaces: %s\n", strings.Join(dto.Surfaces, ", "))
				}
				// The persona dressing (#428) — shown only on a dressed agent,
				// so an undressed one prints exactly what it always did.
				if dto.PersonaRole != nil || dto.PersonaPrompt != nil {
					fmt.Fprintf(w, "  persona role: %s\n", dash(dto.PersonaRole))
					if dto.PersonaPrompt != nil && *dto.PersonaPrompt != "" {
						fmt.Fprintf(w, "  persona prompt: %s\n", *dto.PersonaPrompt)
					}
				}
				return nil
			})
		},
	}
}

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var org, name, description, typ, vis, systemPrompt, systemMemory string
	var personaRole, personaPrompt, installInto string
	var surfaces []string
	var ownerMe bool
	cmd := &cobra.Command{
		Use:   "create --name <n> [--org <id> | --owner-me] [--install-into <app>]",
		Short: "Create an agent (org-owned, or user-owned with --owner-me)",
		Long: `Create an agent. Pass --org to create an org-owned agent (you must be an org
ADMIN). Otherwise the agent is user-owned, in your own @handle namespace (spec
047) — pass --owner-me to say so explicitly, or just omit --org. A user-owned
agent is PERSONAL/owner-only in v1: the server derives the @handle:<slug> URN
and rejects a non-PERSONAL --visibility. --org and --owner-me are mutually
exclusive.

A NEW AGENT IS IN NO APP'S CAST POOL, so it cannot be cast as a worker yet
(#535). --install-into <app> does that second step in the same run — the
common case when the agent is a role agent for a team you already have:

  hadron agent create --org acme.com --name "DevOps Engineer" \
      --persona-role devops-engineer --persona-prompt '…' \
      --install-into hrn:app:acme.com:eng-team

It is deliberately NOT spelled --app: that is the persistent App-CONTEXT flag,
which never acts on an App (see the note ` + "`agent list --app`" + ` prints, #383).
--install-into names a target and has an effect, so it gets its own name.

The two steps are not atomic — nothing on the server side spans them. If the
install fails the agent still EXISTS, and the failure says so and prints the
` + "`hadron app agent add`" + ` needed to finish; it is never silently rolled back,
because deleting an agent you asked to create is the more destructive repair.

Authorization for the install is CONTRIBUTOR+ on the App's owning org (or
ownership of a user-owned App) — a stricter gate than creating the agent, so
it can refuse after the create succeeds.`,
		Example: `  hadron agent create --owner-me --name "My Agent" --type ASSISTANT
  hadron agent create --org acme.com --name "Support Bot" --type CHATBOT --visibility ORGANIZATION
  hadron agent create --org acme.com --name "DevOps Engineer" --persona-role devops-engineer --install-into hrn:app:acme.com:eng-team`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// spec 047: an agent is owned by EXACTLY ONE of an org or the caller.
			if ownerMe && org != "" {
				return exitcode.Newf(exitcode.Usage, "--owner-me creates an agent you own in your own namespace; drop --org (or drop --owner-me to create an organization agent)")
			}
			at, err := parseAgentType(typ)
			if err != nil {
				return err
			}
			av, err := ParseVisibility(vis)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateAgent(cmd.Context(), client, name, optStr(org),
				optStr(description), at, av, optStr(systemPrompt), optStr(systemMemory), surfaces,
				optStr(personaRole), optStr(personaPrompt))
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateAgent == nil {
				return exitcode.Newf(exitcode.Error, "server returned no agent")
			}
			created := agentCreateDTO{agentDTO: agentDTOFromFields(resp.CreateAgent.AgentFields)}

			if installInto == "" {
				// #535: "✓ created" reads as done, but the agent is inert until it
				// is in some App's cast pool. Stderr keeps the --json contract clean.
				defer f.NoteAgentNotInstalled(created.URN)
				return emitAgent(f, created.agentDTO, "✓ created")
			}

			inst, installErr := installCreatedAgent(cmd.Context(), client, installInto, created.ID)
			created.Install = inst
			// The agent exists either way, so it is always emitted — losing the URN
			// of a just-created agent is the one outcome with no cheap recovery.
			if emitErr := emitCreatedAgent(f, created); emitErr != nil {
				return emitErr
			}
			return installErr
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "owning organization (ID); omit (or use --owner-me) for a user-owned agent")
	cmd.Flags().BoolVar(&ownerMe, "owner-me", false, "create a user-owned agent in your own @handle namespace (org-less; PERSONAL only)")
	cmd.Flags().StringVar(&name, "name", "", "agent name")
	cmd.Flags().StringVar(&description, "description", "", "agent description")
	cmd.Flags().StringVar(&typ, "type", "", "type: ASSISTANT or CHATBOT (server default when unset)")
	cmd.Flags().StringVar(&vis, "visibility", "", "visibility: ORGANIZATION, PERSONAL, or PUBLIC (server default when unset)")
	cmd.Flags().StringVar(&systemPrompt, "system-prompt", "", "system prompt")
	cmd.Flags().StringVar(&systemMemory, "system-memory", "", "system memory ID")
	cmd.Flags().StringArrayVar(&surfaces, "surface", nil, "surface the agent is available on (repeatable)")
	cmd.Flags().StringVar(&personaRole, "persona-role", "", "persona dressing: the role this agent presents as (metadata)")
	cmd.Flags().StringVar(&personaPrompt, "persona-prompt", "", "persona dressing: identity prompt TEMPLATE with {{name}}/{{role}} placeholders")
	cmd.Flags().StringVar(&installInto, "install-into", "", "App (ID or URN) to install the new agent into, so it is castable in one run")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var name, description, typ, vis, systemPrompt, systemMemory, urn string
	var personaRole, personaPrompt string
	var surfaces []string
	cmd := &cobra.Command{
		Use:   "update <ref>",
		Short: "Update an agent by ID or URN (only the fields you pass change)",
		Long: `Update an agent. Only the fields you pass change.

--persona-role and --persona-prompt set the agent's persona DRESSING
(cor:agt:020:01): the role is pure metadata, and the prompt is a TEMPLATE —
{{name}} and {{role}} are bound when the agent is cast into an App as a
named Worker (` + "`hadron team worker cast`" + `). Editing the template evolves
every future casting's boot briefing centrally; existing workers resolve
their prompt from it too.`,
		Example: `  hadron agent update hrn:agent:acme.com:support-bot --name "Support Bot v2" --visibility PUBLIC
  hadron agent update agt_123 --description "…"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("name") && !changed("description") && !changed("type") && !changed("visibility") &&
				!changed("system-prompt") && !changed("system-memory") && !changed("surface") && !changed("urn") &&
				!changed("persona-role") && !changed("persona-prompt") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}
			// The server prepends the owner namespace, so --urn is the agent
			// slug — which may carry an author org or @handle atom, hence an
			// agent-context path check rather than a plain node-loc check.
			if changed("urn") {
				if err := cmdutil.ValidateAgentURNPath("--urn", urn); err != nil {
					return err
				}
			}
			at, err := parseAgentType(typ)
			if err != nil {
				return err
			}
			av, err := ParseVisibility(vis)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var surfacesArg []string
			if changed("surface") {
				surfacesArg = surfaces
			}
			resp, err := gen.UpdateAgent(cmd.Context(), client, args[0],
				changedStr(cmd, "name", name), changedStr(cmd, "description", description),
				at, av, changedStr(cmd, "system-prompt", systemPrompt),
				changedStr(cmd, "system-memory", systemMemory), surfacesArg, changedStr(cmd, "urn", urn),
				changedStr(cmd, "persona-role", personaRole), changedStr(cmd, "persona-prompt", personaPrompt))
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateAgent == nil {
				return exitcode.Newf(exitcode.Error, "server returned no agent")
			}
			return emitAgent(f, agentDTOFromFields(resp.UpdateAgent.AgentFields), "✓ updated")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "agent name")
	cmd.Flags().StringVar(&description, "description", "", "agent description")
	cmd.Flags().StringVar(&typ, "type", "", "type: ASSISTANT or CHATBOT")
	cmd.Flags().StringVar(&vis, "visibility", "", "visibility: ORGANIZATION, PERSONAL, or PUBLIC")
	cmd.Flags().StringVar(&systemPrompt, "system-prompt", "", "system prompt")
	cmd.Flags().StringVar(&systemMemory, "system-memory", "", "system memory ID")
	cmd.Flags().StringArrayVar(&surfaces, "surface", nil, "surface the agent is available on (repeatable; replaces the set)")
	cmd.Flags().StringVar(&urn, "urn", "", "agent URN path")
	cmd.Flags().StringVar(&personaRole, "persona-role", "", "persona dressing: the role this agent presents as (metadata)")
	cmd.Flags().StringVar(&personaPrompt, "persona-prompt", "", "persona dressing: identity prompt TEMPLATE with {{name}}/{{role}} placeholders")
	return cmd
}

func newCmdRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <ref>",
		Aliases: []string{"delete"},
		Short:   "Delete an agent by ID or URN",
		Example: `  hadron agent rm hrn:agent:acme.com:support-bot --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "agent "+args[0]); err != nil {
				return err
			}
			resp, err := gen.DeleteAgent(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if !resp.DeleteAgent {
				return exitcode.Newf(exitcode.Error, "agent %s was not deleted", args[0])
			}
			dto := map[string]string{"id": args[0], "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ deleted agent %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func emitAgent(f *cmdutil.Factory, dto agentDTO, verb string) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "%s agent %s (%s)\n", verb, dto.Name, dto.URN)
		return err
	})
}

// agentInstallDTO reports the --install-into outcome. Status is "installed" or
// "failed"; on failure Error carries the server's sentence so a --json caller
// can branch without parsing stderr.
type agentInstallDTO struct {
	AppID  string `json:"appId"`
	AppURN string `json:"appUrn"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// agentCreateDTO is `agent create`'s --json shape: the agent, plus an install
// report ONLY when --install-into was passed. The embedded agentDTO inlines its
// fields, and the omitempty pointer means a plain create emits byte-identical
// JSON to before — the existing contract is extended, never changed.
type agentCreateDTO struct {
	agentDTO
	Install *agentInstallDTO `json:"install,omitempty"`
}

// installCreatedAgent runs the second half of `agent create --install-into`.
// It always returns a DTO — a failed install is a reported outcome, not a
// missing one — alongside the error that should become the exit code.
func installCreatedAgent(ctx context.Context, client graphql.Client, appRef, agentID string) (*agentInstallDTO, error) {
	resp, err := gen.InstallAgentIntoApp(ctx, client, appRef, agentID, nil)
	if err != nil {
		mapped := cmdutil.InstallForbiddenGuidance(err)
		return &agentInstallDTO{AppURN: appRef, Status: "failed", Error: mapped.Error()},
			installIncompleteError(mapped, appRef, agentID)
	}
	if resp.InstallAgentIntoApp == nil || resp.InstallAgentIntoApp.AppAgent == nil {
		err := exitcode.Newf(exitcode.Error, "server returned no AppAgent row")
		return &agentInstallDTO{AppURN: appRef, Status: "failed", Error: err.Error()},
			installIncompleteError(err, appRef, agentID)
	}
	dto := &agentInstallDTO{AppURN: appRef, Status: "installed"}
	if app := resp.InstallAgentIntoApp.AppAgent.App; app != nil {
		dto.AppID, dto.AppURN = app.Id, app.Urn
	}
	return dto, nil
}

// installIncompleteError states plainly that the create SUCCEEDED and only the
// install failed, and prints the command that finishes the job. Without this the
// natural reading of a failed `agent create` is that nothing was created, and
// the user's next move is to re-run it — which would create a second agent.
func installIncompleteError(cause error, appRef, agentID string) error {
	return exitcode.Newf(exitcode.FromError(cause),
		"agent was CREATED but NOT installed into %s: %v\n"+
			"The agent exists — do not re-run `agent create`, it would make a second one.\n"+
			"Finish with: hadron app agent add %s %s",
		appRef, cause, appRef, agentID)
}

// emitCreatedAgent renders the create+install result: the agent line first
// (so the URN is on screen even when the install failed), then the install.
func emitCreatedAgent(f *cmdutil.Factory, dto agentCreateDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "✓ created agent %s (%s)\n", dto.Name, dto.URN); err != nil {
			return err
		}
		if dto.Install == nil || dto.Install.Status != "installed" {
			return nil
		}
		_, err := fmt.Fprintf(w, "✓ installed into %s\n", dto.Install.AppURN)
		return err
	})
}

// optStr returns a pointer for a non-empty value, else nil (omitted on the wire).
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// changedStr returns a pointer only when the flag was explicitly set, so an
// unset flag is omitted (preserve) while an explicit "" is sent (clear).
func changedStr(cmd *cobra.Command, flag, val string) *string {
	if cmd.Flags().Changed(flag) {
		return &val
	}
	return nil
}
