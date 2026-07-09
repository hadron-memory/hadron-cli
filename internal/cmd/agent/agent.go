// Package agent implements `hadron agent ...` — agent lifecycle management.
package agent

import (
	"fmt"
	"io"
	"strings"

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
	OrganizationID string   `json:"organizationId"`
	Surfaces       []string `json:"surfaces"`
	SystemMemoryID *string  `json:"systemMemoryId"`
	SystemPrompt   *string  `json:"systemPrompt"`
	AiProvider     *string  `json:"aiProvider"`
	AiModel        *string  `json:"aiModel"`
	HasAiApiKey    bool     `json:"hasAiApiKey"`
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
		AiProvider: a.AiProvider, AiModel: a.AiModel, HasAiApiKey: a.HasAiApiKey, CreatedAt: a.CreatedAt,
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

func parseAgentVisibility(s string) (*gen.AgentVisibility, error) {
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
		Use:     "ls [--org <id>] [--type <t>] [--visibility <v>] | --public [--type <t>]",
		Aliases: []string{"list"},
		Short:   "List agents",
		Long: `List agents. By default this is the member-scoped view — agents in orgs you
belong to.

--public instead lists the cross-org marketplace slice: every live PUBLIC
agent, readable without org membership (a foreign public agent you can grab the
URN of to subscribe/install). It's a separate surface, so --org and
--visibility don't apply to it; --type still filters.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			av, err := parseAgentVisibility(vis)
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
				var filter *gen.AgentFilter
				if at != nil {
					filter = &gen.AgentFilter{Type: at}
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
		Example: `  hadron agent get acme.com::support-bot --json`,
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
				return nil
			})
		},
	}
}

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var org, name, description, typ, vis, systemPrompt, systemMemory string
	var surfaces []string
	cmd := &cobra.Command{
		Use:     "create --org <id> --name <n>",
		Short:   "Create an agent",
		Example: `  hadron agent create --org acme.com --name "Support Bot" --type CHATBOT --visibility ORGANIZATION`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			at, err := parseAgentType(typ)
			if err != nil {
				return err
			}
			av, err := parseAgentVisibility(vis)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateAgent(cmd.Context(), client, name, org,
				optStr(description), at, av, optStr(systemPrompt), optStr(systemMemory), surfaces)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateAgent == nil {
				return exitcode.Newf(exitcode.Error, "server returned no agent")
			}
			return emitAgent(f, agentDTOFromFields(resp.CreateAgent.AgentFields), "✓ created")
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "owning organization (ID)")
	cmd.Flags().StringVar(&name, "name", "", "agent name")
	cmd.Flags().StringVar(&description, "description", "", "agent description")
	cmd.Flags().StringVar(&typ, "type", "", "type: ASSISTANT or CHATBOT (server default when unset)")
	cmd.Flags().StringVar(&vis, "visibility", "", "visibility: ORGANIZATION, PERSONAL, or PUBLIC (server default when unset)")
	cmd.Flags().StringVar(&systemPrompt, "system-prompt", "", "system prompt")
	cmd.Flags().StringVar(&systemMemory, "system-memory", "", "system memory ID")
	cmd.Flags().StringArrayVar(&surfaces, "surface", nil, "surface the agent is available on (repeatable)")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var name, description, typ, vis, systemPrompt, systemMemory, urn string
	var surfaces []string
	cmd := &cobra.Command{
		Use:     "update <id>",
		Short:   "Update an agent (only the fields you pass change)",
		Example: `  hadron agent update agt_123 --name "Support Bot v2" --visibility PUBLIC`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("name") && !changed("description") && !changed("type") && !changed("visibility") &&
				!changed("system-prompt") && !changed("system-memory") && !changed("surface") && !changed("urn") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}
			// The server prepends the org (`<org>:<urn>`), so --urn is the agent
			// slug — which may carry an author-org atom, hence a path check.
			if changed("urn") {
				if err := cmdutil.ValidateURNPath("--urn", urn); err != nil {
					return err
				}
			}
			at, err := parseAgentType(typ)
			if err != nil {
				return err
			}
			av, err := parseAgentVisibility(vis)
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
				changedStr(cmd, "system-memory", systemMemory), surfacesArg, changedStr(cmd, "urn", urn))
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
	cmd.Flags().StringVar(&urn, "urn", "", "agent URN")
	return cmd
}

func newCmdRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <id>",
		Aliases: []string{"delete"},
		Short:   "Delete an agent",
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
