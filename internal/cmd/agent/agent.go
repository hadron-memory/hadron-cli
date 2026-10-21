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
	TasksIndexRef  *string  `json:"tasksIndexRef"`
	ProcessesIndex *string  `json:"processesIndexRef"`
	ExtendsAgentID *string  `json:"extendsAgentId"`
	SystemPrompt   *string  `json:"systemPrompt"`
	AiProvider     *string  `json:"aiProvider"`
	AiModel        *string  `json:"aiModel"`
	HasAiApiKey    bool     `json:"hasAiApiKey"`
	PersonaRole    *string  `json:"personaRole"`
	UsesTasks      bool     `json:"usesTasks"`
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
		TasksIndexRef: a.TasksIndexRef, ProcessesIndex: a.ProcessesIndexRef, ExtendsAgentID: a.ExtendsAgentId,
		AiProvider: a.AiProvider, AiModel: a.AiModel, HasAiApiKey: a.HasAiApiKey,
		PersonaRole: a.PersonaRole, UsesTasks: a.UsesTasks, CreatedAt: a.CreatedAt,
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
	cmd.AddCommand(newCmdAgentTasks(f))
	cmd.AddCommand(newCmdAgentProcesses(f))
	cmd.AddCommand(newCmdAgentSetBase(f))
	return cmd
}

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var org, typ, vis string
	var limit, offset int
	var public, ownedByMe bool
	cmd := &cobra.Command{
		Use:     "list [--org <id> | --owned-by-me] [--type <t>] [--visibility <v>] | --public [--type <t>]",
		Aliases: []string{"ls"},
		Short:   "List agents",
		Long: `List agents. By default this is the member-scoped view — agents in orgs you
belong to.

--public instead lists the cross-org marketplace slice: every live PUBLIC
agent, readable without org membership (a foreign public agent you can grab the
URN of to subscribe/install). It's a separate surface, so --org and
--visibility don't apply to it; --type still filters.

--owned-by-me narrows the member-scoped view to the agents you own outright:
your org-less ones (no organizationId), owned by you. It is the "my agents"
slice, answered by the server — the same predicate ` + "`app list --owned-by-me`" + `
and ` + "`memory list --owned-by-me`" + ` forward. Org-less by definition, so it
does not combine with --org and your active organization is not consulted; an
App-key caller gets an empty list. It also does not combine with --public,
since a PUBLIC agent is never user-owned. --type and --visibility still
narrow it.

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
			// PublicAgentFilter has no ownedByMe FIELD — the server rejects it at
			// the schema rather than returning an empty page, because a PUBLIC
			// agent is never user-owned. Refuse here so the reason names the
			// slices rather than arriving as a GraphQL validation error.
			if public && ownedByMe {
				return exitcode.Newf(exitcode.Usage, "--public is the cross-org PUBLIC slice and --owned-by-me is your own org-less one; a PUBLIC agent is never user-owned, so the two never overlap")
			}
			// The owner slice is org-less by definition (organizationId IS NULL),
			// so the server never consults orgId for it: combining them would be
			// empty by construction rather than a narrowing.
			// Changed(), not the value: `--org=` is "asked for nothing", a
			// different mistake from not asking at all, and a value test would
			// read it as the latter (review:an-empty-flag-is-not-an-absent-flag).
			if ownedByMe && cmd.Flags().Changed("org") {
				return exitcode.Newf(exitcode.Usage, "--owned-by-me lists the agents you own, which have no organization; drop --org (or drop --owned-by-me to list an organization's agents)")
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
				// No --limit/--offset means the whole list (cli#967): drain to
				// the total. An explicit flag is one deliberate page.
				rows, err := listPages(lim, off, func(l, o *int) ([]*agentRow, int, error) {
					resp, err := gen.PublicAgents(cmd.Context(), client, filter, l, o)
					if err != nil || resp.PublicAgents == nil {
						return nil, 0, err
					}
					out := make([]*agentRow, 0, len(resp.PublicAgents.Items))
					for _, a := range resp.PublicAgents.Items {
						if a != nil {
							out = append(out, &agentRow{&a.AgentFields})
						}
					}
					return out, resp.PublicAgents.Total, nil
				})
				if err != nil {
					return api.MapError(err)
				}
				for _, a := range rows {
					agents = append(agents, agentDTOFromFields(*a.AgentFields))
				}
			} else {
				var filter *gen.AgentFilter
				if at != nil || av != nil || ownedByMe {
					filter = &gen.AgentFilter{Type: at, Visibility: av}
					// Only ever set true: the clauses compose by AND, so a
					// literal false is a no-op the server still has to read,
					// and omitting it keeps an unfiltered request unchanged.
					if ownedByMe {
						filter.OwnedByMe = &ownedByMe
					}
				}
				var orgPtr *string
				if org != "" {
					orgPtr = &org
				}
				rows, err := listPages(lim, off, func(l, o *int) ([]*agentRow, int, error) {
					resp, err := gen.Agents(cmd.Context(), client, orgPtr, filter, l, o)
					if err != nil || resp.Agents == nil {
						return nil, 0, err
					}
					out := make([]*agentRow, 0, len(resp.Agents.Items))
					for _, a := range resp.Agents.Items {
						if a != nil {
							out = append(out, &agentRow{&a.AgentFields})
						}
					}
					return out, resp.Agents.Total, nil
				})
				if err != nil {
					return api.MapError(err)
				}
				for _, a := range rows {
					agents = append(agents, agentDTOFromFields(*a.AgentFields))
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
	cmd.Flags().BoolVar(&ownedByMe, "owned-by-me", false, "list only the org-less agents you own")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results (one page of that size); unset lists everything")
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
				fmt.Fprintf(w, "  description: %s\n  system memory: %s\n  tasks index: %s\n  processes index: %s\n  extends: %s\n  ai: %s/%s (key: %v)\n",
					dash(dto.Description), dash(dto.SystemMemoryID), dash(dto.TasksIndexRef), dash(dto.ProcessesIndex), dash(dto.ExtendsAgentID), dash(dto.AiProvider), dash(dto.AiModel), dto.HasAiApiKey)
				fmt.Fprintf(w, "  uses tasks: %t\n", dto.UsesTasks)
				if len(dto.Surfaces) > 0 {
					fmt.Fprintf(w, "  surfaces: %s\n", strings.Join(dto.Surfaces, ", "))
				}
				if dto.PersonaRole != nil {
					fmt.Fprintf(w, "  persona role: %s\n", dash(dto.PersonaRole))
				}
				if dto.SystemPrompt != nil && *dto.SystemPrompt != "" {
					fmt.Fprintf(w, "  system prompt: %s\n", *dto.SystemPrompt)
				}
				return nil
			})
		},
	}
}

// The old flags remain parseable only to give scripts a local, actionable
// refusal before credentials or a request are touched. They no longer map to
// a server field, and silently forwarding them to systemPrompt would risk
// overwriting a different template.
func refuseRetiredPersonaPromptFlags(cmd *cobra.Command) error {
	if cmd.Flags().Changed("persona-prompt") || cmd.Flags().Changed("persona-prompt-file") {
		return exitcode.Newf(exitcode.Usage,
			"--persona-prompt and --persona-prompt-file were retired; use --system-prompt or --system-prompt-file for the shared {{name}}/{{role}} template")
	}
	return nil
}

func registerRetiredPersonaPromptFlags(cmd *cobra.Command) {
	cmd.Flags().String("persona-prompt", "", "retired; use --system-prompt")
	cmd.Flags().String("persona-prompt-file", "", "retired; use --system-prompt-file")
	_ = cmd.Flags().MarkHidden("persona-prompt")
	_ = cmd.Flags().MarkHidden("persona-prompt-file")
}

// refuseBlankPromptFile rejects a --<flag>-file passed with an empty path — an
// unset shell variable is the usual cause. ResolveTextInput reads an empty
// path as an empty VALUE, so without this an `agent update --system-prompt-file
// "$UNSET"` is Changed-but-empty and silently CLEARS the prompt instead of
// reporting that no file was supplied (#541 codex P1; the
// an-empty-flag-is-not-an-absent-flag class). The inline --<flag> "" is left
// alone on purpose: an explicit empty there is a deliberate clear. flag is the
// base name; the file flag is derived as <flag>-file, and its value is read
// from the registry so the vars need not be threaded here.
func refuseBlankPromptFile(cmd *cobra.Command, flags ...string) error {
	for _, flag := range flags {
		name := flag + "-file"
		if cmd.Flags().Changed(name) && strings.TrimSpace(cmd.Flags().Lookup(name).Value.String()) == "" {
			return exitcode.Newf(exitcode.Usage,
				"--%s was given an empty path — pass a file, or drop the flag (an unset variable is the usual cause)", name)
		}
	}
	return nil
}

// resolvePromptFlag resolves an UPDATE-side long-text field to the pointer
// UpdateAgent wants: nil (preserve) when neither --<flag> nor --<flag>-file was
// passed, otherwise a pointer to the resolved value — so an explicit empty
// clears, matching changedStr. The value may come inline, from --<flag>-file,
// or from stdin ("-"), via the shared cmdutil resolver.
func resolvePromptFlag(cmd *cobra.Command, flag, value, file string, stdin io.Reader) (*string, error) {
	if !cmd.Flags().Changed(flag) && !cmd.Flags().Changed(flag+"-file") {
		return nil, nil
	}
	resolved, err := cmdutil.ResolveTextInput(flag, value, file, stdin)
	if err != nil {
		return nil, err
	}
	return &resolved, nil
}

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var org, name, description, typ, vis, systemPrompt, systemMemory string
	var personaRole, installInto string
	var systemPromptFile string
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
      --persona-role devops-engineer --system-prompt 'You are {{name}}…' \
      --install-into hrn:app:acme.com:eng-team

It is deliberately NOT spelled --app: that is the persistent App-CONTEXT flag,
which never acts on an App (see the note ` + "`agent list --app`" + ` prints, #383).
--install-into names a target and has an effect, so it gets its own name.

The two steps are not atomic — nothing on the server side spans them. If the
install fails the agent still EXISTS, and the failure says so and prints the
` + "`hadron app agent add`" + ` needed to finish; it is never silently rolled back,
because deleting an agent you asked to create is the more destructive repair.

A lost connection is reported as UNKNOWN, not failed (exit 7, #394): the
install may have committed before the answer went missing, so the remedy is
to check ` + "`app agent list`" + ` rather than to retry blind into a duplicate.

Authorization for the install is CONTRIBUTOR+ on the App's owning org (or
ownership of a user-owned App) — a stricter gate than creating the agent, so
it can refuse after the create succeeds.`,
		Example: `  hadron agent create --owner-me --name "My Agent" --type ASSISTANT
  hadron agent create --org acme.com --name "Support Bot" --type CHATBOT --visibility ORGANIZATION
  hadron agent create --org acme.com --name "DevOps Engineer" --persona-role devops-engineer --install-into hrn:app:acme.com:eng-team`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseRetiredPersonaPromptFlags(cmd); err != nil {
				return err
			}
			// spec 047: an agent is owned by EXACTLY ONE of an org or the caller.
			if ownerMe && org != "" {
				return exitcode.Newf(exitcode.Usage, "--owner-me creates an agent you own in your own namespace; drop --org (or drop --owner-me to create an organization agent)")
			}
			// #543 review (@codex P2). An EXPLICIT `--install-into=` is a usage
			// error, not an absent flag. `--install-into="$APP"` with an unset
			// variable arrives here as Changed-but-empty, and the emptiness test
			// further down is the FLAG-ABSENT path — so without this the agent is
			// created, "✓ created" is printed, and the command exits 0 having
			// silently skipped the install the caller asked for. Under --json the
			// `install` key is simply omitted, which is indistinguishable from
			// never having asked.
			//
			// Refused HERE, before CreateAgent, deliberately: refusing after the
			// create would leave an orphan agent behind on a pure usage error,
			// trading a silent no-op for a messier one.
			//
			// NORMALIZED, not just validated: `--install-into=" "` is checked and
			// forwarded as the same trimmed value. Testing TrimSpace while sending
			// the raw string is the worst of both — it admits a target the server
			// then fails to resolve, in a message naming a ref nobody typed.
			installInto = strings.TrimSpace(installInto)
			if cmd.Flags().Changed("install-into") && installInto == "" {
				return exitcode.Newf(exitcode.Usage,
					"--install-into needs an App (ID or URN); it was passed empty, which usually means an unset variable — drop the flag to create an agent without installing it")
			}
			// Shape-checked for the same reason it is refused when empty: a
			// target the server cannot resolve must not cost an orphan agent.
			var err error
			if installInto, err = cmdutil.CanonicalAppRef("--install-into", installInto); err != nil {
				return err
			}
			at, err := parseAgentType(typ)
			if err != nil {
				return err
			}
			av, err := ParseVisibility(vis)
			if err != nil {
				return err
			}
			if err := refusePromptStdin(f.IOStreams.IsInputTerminal(), systemPrompt); err != nil {
				return err
			}
			if err := refuseBlankPromptFile(cmd, "system-prompt"); err != nil {
				return err
			}
			systemPrompt, err = cmdutil.ResolveTextInput("system-prompt", systemPrompt, systemPromptFile, f.IOStreams.In)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateAgent(cmd.Context(), client, name, optStr(org),
				optStr(description), at, av, optStr(systemPrompt), optStr(systemMemory), surfaces,
				optStr(personaRole))
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateAgent == nil {
				return exitcode.Newf(exitcode.Error, "server returned no agent")
			}
			created := agentCreateDTO{agentDTO: agentDTOFromFields(resp.CreateAgent.AgentFields)}

			// Reaching here with an empty installInto now means ONE thing — the
			// flag was not passed. "Asked for nothing" was refused above, so do
			// not collapse that guard into this test on the grounds that both
			// look at the same emptiness: this branch is only unambiguous
			// BECAUSE the other one runs first.
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
	cmd.Flags().StringVar(&name, "name", "", "agent name (required)")
	cmd.Flags().StringVar(&description, "description", "", "agent description")
	cmd.Flags().StringVar(&typ, "type", "", "type: ASSISTANT or CHATBOT (server default when unset)")
	cmd.Flags().StringVar(&vis, "visibility", "", "visibility: ORGANIZATION, PERSONAL, or PUBLIC (server default when unset)")
	cmd.Flags().StringVar(&systemPrompt, "system-prompt", "", "shared agent and Worker prompt template; {{name}}/{{role}} bind for Workers (a lone - reads piped stdin)")
	cmd.Flags().StringVar(&systemMemory, "system-memory", "", "system memory ID")
	cmd.Flags().StringArrayVar(&surfaces, "surface", nil, "surface the agent is available on (repeatable)")
	cmd.Flags().StringVar(&personaRole, "persona-role", "", "persona dressing: the role this agent presents as (metadata)")
	cmd.Flags().StringVar(&systemPromptFile, "system-prompt-file", "", "read the shared prompt template from a file (multi-line safe); mutually exclusive with --system-prompt")
	cmd.Flags().StringVar(&installInto, "install-into", "", "App (ID or URN) to install the new agent into, so it is castable in one run")
	registerRetiredPersonaPromptFlags(cmd)
	cmd.MarkFlagsMutuallyExclusive("system-prompt", "system-prompt-file")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var name, description, typ, vis, systemPrompt, systemMemory, urn string
	var personaRole string
	var usesTasks bool
	var systemPromptFile string
	var tasksIndex, processesIndex string
	var surfaces []string
	cmd := &cobra.Command{
		Use:   "update <ref>",
		Short: "Update an agent by ID or URN (only the fields you pass change)",
		Long: `Update an agent. Only the fields you pass change.

--persona-role is metadata. --system-prompt sets the shared prompt TEMPLATE:
{{name}} and {{role}} bind when the agent is cast into an App as a named
Worker (` + "`hadron team worker cast`" + `). Existing workers also resolve
their prompt from this field.`,
		Example: `  hadron agent update hrn:agent:acme.com:support-bot --name "Support Bot v2" --visibility PUBLIC
  hadron agent update agt_123 --description "…"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseRetiredPersonaPromptFlags(cmd); err != nil {
				return err
			}
			changed := cmd.Flags().Changed
			if !changed("name") && !changed("description") && !changed("type") && !changed("visibility") &&
				!changed("system-prompt") && !changed("system-prompt-file") &&
				!changed("system-memory") && !changed("surface") && !changed("urn") &&
				!changed("persona-role") && !changed("uses-tasks") &&
				!changed("tasks-index") && !changed("processes-index") {
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
			var surfacesArg []string
			if changed("surface") {
				surfacesArg = surfaces
			}
			if err := refusePromptStdin(f.IOStreams.IsInputTerminal(), systemPrompt); err != nil {
				return err
			}
			if err := refuseBlankPromptFile(cmd, "system-prompt"); err != nil {
				return err
			}
			systemPromptArg, err := resolvePromptFlag(cmd, "system-prompt", systemPrompt, systemPromptFile, f.IOStreams.In)
			if err != nil {
				return err
			}
			// The client is built only after every argument-only check and the
			// prompt reads: building it resolves the server and credentials, and
			// a local refusal (#648's terminal guard above all) must not be
			// masked by an auth error, nor touch the credential store.
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var updated *gen.AgentFields
			if changed("name") || changed("description") || changed("type") || changed("visibility") ||
				changed("system-prompt") || changed("system-prompt-file") ||
				changed("system-memory") || changed("surface") || changed("urn") ||
				changed("persona-role") || changed("uses-tasks") {
				var usesTasksArg *bool
				if changed("uses-tasks") {
					usesTasksArg = &usesTasks
				}
				resp, err := gen.UpdateAgent(cmd.Context(), client, args[0],
					changedStr(cmd, "name", name), changedStr(cmd, "description", description),
					at, av, systemPromptArg,
					changedStr(cmd, "system-memory", systemMemory), surfacesArg, changedStr(cmd, "urn", urn),
					changedStr(cmd, "persona-role", personaRole), usesTasksArg)
				if err != nil {
					return api.MapError(err)
				}
				if resp.UpdateAgent == nil {
					return exitcode.Newf(exitcode.Error, "server returned no agent")
				}
				updated = &resp.UpdateAgent.AgentFields
			}
			// The two index refs are their own mutation (setAgentIndexRefs):
			// a value sets, an explicit empty clears, absent preserves.
			fields, err := applyAgentIndexRefChanges(cmd.Context(), client, args[0],
				changed("tasks-index"), tasksIndex, changed("processes-index"), processesIndex)
			if err != nil {
				return err
			}
			if fields == nil {
				fields = updated
			}
			if fields == nil {
				return exitcode.Newf(exitcode.Error, "server returned no agent")
			}
			return emitAgent(f, agentDTOFromFields(*fields), "✓ updated")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "agent name")
	cmd.Flags().StringVar(&description, "description", "", "agent description")
	cmd.Flags().StringVar(&typ, "type", "", "type: ASSISTANT or CHATBOT")
	cmd.Flags().StringVar(&vis, "visibility", "", "visibility: ORGANIZATION, PERSONAL, or PUBLIC")
	cmd.Flags().StringVar(&systemPrompt, "system-prompt", "", "shared agent and Worker prompt template; {{name}}/{{role}} bind for Workers (a lone - reads piped stdin)")
	cmd.Flags().StringVar(&systemMemory, "system-memory", "", "system memory ID")
	cmd.Flags().StringArrayVar(&surfaces, "surface", nil, "surface the agent is available on (repeatable; replaces the set)")
	cmd.Flags().StringVar(&urn, "urn", "", "agent URN path")
	cmd.Flags().StringVar(&personaRole, "persona-role", "", "persona dressing: the role this agent presents as (metadata)")
	cmd.Flags().StringVar(&tasksIndex, "tasks-index", "", `the Agent's tasks index node (a ref; an explicit empty clears it)`)
	cmd.Flags().StringVar(&processesIndex, "processes-index", "", `the Agent's processes index node (a ref; an explicit empty clears it)`)
	cmd.Flags().StringVar(&systemPromptFile, "system-prompt-file", "", "read the shared prompt template from a file (multi-line safe); mutually exclusive with --system-prompt")
	cmd.Flags().BoolVar(&usesTasks, "uses-tasks", false, "Whether workers from this Agent use assigned tasks; false keeps them expected to check the team channel without a task (omitted preserves)")
	registerRetiredPersonaPromptFlags(cmd)
	cmd.MarkFlagsMutuallyExclusive("system-prompt", "system-prompt-file")
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

// agentInstallDTO reports the --install-into outcome.
//
// Status is "installed", "failed", or "unknown" — the last for exit-code 7
// (#394), where the request never reached the server or its answer never came
// back, so whether the install committed is genuinely not known. Error carries
// the server's sentence so a --json caller can branch without parsing stderr.
//
// AppRef is what was sent — the caller's --install-into after
// cmdutil.CanonicalAppRef, so an App ID verbatim or the canonical
// hrn:app:<root>:<slug> whatever spelling was typed (#540); appId/appUrn are
// the server's resolved values and are therefore present only on success.
// Putting the ref in appUrn would let that field carry a non-URN (an id),
// which is worse than omitting it.
type agentInstallDTO struct {
	AppRef string `json:"appRef"`
	AppID  string `json:"appId,omitempty"`
	AppURN string `json:"appUrn,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// installStatusFor distinguishes a refusal from an unknown outcome. Only
// exitcode.Unavailable means the mutation may in fact have committed.
func installStatusFor(err error) string {
	if exitcode.FromError(err) == exitcode.Unavailable {
		return "unknown"
	}
	return "failed"
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
		incomplete := installIncompleteError(mapped, appRef, agentID)
		// #681 (PR #683 review, @copilot): `app agent add` fails the same way
		// until the MCP-only key is replaced, so it is the SECOND step here, not
		// the finishing one. MapError has already named the credential fix.
		if api.OAuthScopeRefusal(err) != "" {
			incomplete = exitcode.Newf(exitcode.FromError(mapped),
				"agent was CREATED but NOT installed into %s: %v\n"+
					"The agent exists — do not re-run `agent create`, it would make a second one.\n"+
					"Once the key is replaced, finish with: hadron app agent add %s %s",
				appRef, mapped, appRef, agentID)
		}
		return &agentInstallDTO{AppRef: appRef, Status: installStatusFor(mapped), Error: mapped.Error()}, incomplete
	}
	if resp.InstallAgentIntoApp == nil || resp.InstallAgentIntoApp.AppAgent == nil {
		err := exitcode.Newf(exitcode.Error, "server returned no AppAgent row")
		return &agentInstallDTO{AppRef: appRef, Status: "failed", Error: err.Error()},
			installIncompleteError(err, appRef, agentID)
	}
	dto := &agentInstallDTO{AppRef: appRef, Status: "installed"}
	if app := resp.InstallAgentIntoApp.AppAgent.App; app != nil {
		dto.AppID, dto.AppURN = app.Id, app.Urn
	}
	return dto, nil
}

// installIncompleteError states plainly that the create SUCCEEDED, and names the
// right next move for the two very different failures underneath.
//
// Without it, the natural reading of a failed `agent create` is that nothing was
// created, and the user's next move is to re-run it — making a second agent.
//
// The two cases must not be collapsed. A refusal is definite: the install did
// NOT happen, so `app agent add` finishes the job. Exit-code 7 (#394) is not a
// refusal — the request may have reached the server and committed — so telling
// the caller to run `app agent add` would invite a Conflict (exit 5, "duplicate
// install") on an install that actually succeeded. There, the honest
// instruction is to look before acting.
func installIncompleteError(cause error, appRef, agentID string) error {
	if exitcode.FromError(cause) == exitcode.Unavailable {
		return exitcode.Newf(exitcode.Unavailable,
			"agent was CREATED; whether it was installed into %s is UNKNOWN: %v\n"+
				"The agent exists — do not re-run `agent create`, it would make a second one.\n"+
				"Check before retrying: hadron app agent list %s\n"+
				"If it is not there: hadron app agent add %s %s",
			appRef, cause, appRef, appRef, agentID)
	}
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
			// A failed or unknown install is reported by the returned error, which
			// carries the remedy; repeating it here would say it twice.
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

// ---- #874: the Agent's two index refs (cor:dmo:050:15 / :16) --------------

// inheritedFromDTO names the base a listed node came through.
type inheritedFromDTO struct {
	AgentID  string `json:"agentId"`
	AgentUrn string `json:"agentUrn"`
}

// listedNodeDTO is the node half of a list row, in the server's own shape.
type listedNodeDTO struct {
	ID            string            `json:"id"`
	URN           string            `json:"urn"`
	Name          string            `json:"name"`
	InheritedFrom *inheritedFromDTO `json:"inheritedFrom"`
}

// listedTaskDTO is one `agent tasks list` row: the server shape unchanged.
type listedTaskDTO struct {
	Node    listedNodeDTO `json:"node"`
	Trigger *string       `json:"trigger"`
}

// listedProcessDTO is one `agent processes list` row; kind keeps the server's
// enum in --json and renders lower-case in the table.
type listedProcessDTO struct {
	Node    listedNodeDTO `json:"node"`
	Kind    string        `json:"kind"`
	Trigger *string       `json:"trigger"`
}

// inheritedCell renders the base for a table cell: the base Agent's URN when
// the node came through inheritance, blank for the Agent's own.
func inheritedCell(n listedNodeDTO) string {
	if n.InheritedFrom != nil {
		return n.InheritedFrom.AgentUrn
	}
	return ""
}

// triggerCell renders an index edge's name, blank when no edge lists the row.
func triggerCell(t *string) string {
	if t == nil {
		return ""
	}
	return *t
}

func newCmdAgentTasks(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tasks <command>",
		Short: "The Agent's effective task list",
	}
	cmd.AddCommand(newCmdAgentTasksList(f))
	return cmd
}

func newCmdAgentTasksList(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list <agentRef>",
		Aliases: []string{"ls"},
		Short:   "List the tasks the Agent's workers see (own list plus inherited)",
		Long: `List the Agent's effective task list — the tasks its own index
edge lists plus everything inherited from the base it extends, deduped by
the server (cor:dmo:050:15). TRIGGER is the index edge that lists the task
(blank when none does); INHERITED FROM names the base Agent for a task that
comes through inheritance (blank for the Agent's own).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "the agent reference is empty")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentTaskIndexList(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			rows := make([]listedTaskDTO, 0, len(resp.AgentTaskList))
			for _, row := range resp.AgentTaskList {
				rows = append(rows, listedTaskDTO{
					Node: listedNodeDTO{
						ID:            row.Node.Id,
						URN:           row.Node.Urn,
						Name:          row.Node.Name,
						InheritedFrom: taskInheritedFromDTO(row.Node.InheritedFrom),
					},
					Trigger: row.Trigger,
				})
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				t := output.NewTable(w, "TRIGGER", "NAME", "URN", "INHERITED FROM")
				for _, row := range rows {
					t.Row(triggerCell(row.Trigger), row.Node.Name, row.Node.URN, inheritedCell(row.Node))
				}
				return t.Flush()
			})
		},
	}
	return cmd
}

func newCmdAgentProcesses(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "processes <command>",
		Short: "The Agent's effective process list",
	}
	cmd.AddCommand(newCmdAgentProcessesList(f))
	return cmd
}

func newCmdAgentProcessesList(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list <agentRef>",
		Aliases: []string{"ls"},
		Short:   "List the processes the Agent's workers see (own list plus inherited)",
		Long: `List the Agent's effective process list — the processes its own
index edge lists plus everything inherited from the base it extends, deduped
by the server (cor:dmo:050:16). KIND is STARTS (the process is listed in the
Agent's own processes index) or INVOLVED (the process has a stage edge to
one of the Agent's tasks); TRIGGER is the index edge's name for STARTS rows
and blank for INVOLVED; INHERITED FROM names the base Agent where it applies.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "the agent reference is empty")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentProcessIndexList(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			rows := make([]listedProcessDTO, 0, len(resp.AgentProcessList))
			for _, row := range resp.AgentProcessList {
				rows = append(rows, listedProcessDTO{
					Node: listedNodeDTO{
						ID:            row.Node.Id,
						URN:           row.Node.Urn,
						Name:          row.Node.Name,
						InheritedFrom: processInheritedFromDTO(row.Node.InheritedFrom),
					},
					Kind:    string(row.Kind),
					Trigger: row.Trigger,
				})
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				t := output.NewTable(w, "TRIGGER", "KIND", "NAME", "URN", "INHERITED FROM")
				for _, row := range rows {
					t.Row(triggerCell(row.Trigger), strings.ToLower(row.Kind), row.Node.Name, row.Node.URN, inheritedCell(row.Node))
				}
				return t.Flush()
			})
		},
	}
	return cmd
}

// The generated row types are deeply nested; alias the ones reused here.
type taskListedInherited = gen.AgentTaskIndexListAgentTaskListAgentListedTaskNodeInheritedFrom
type processListedInherited = gen.AgentProcessIndexListAgentProcessListAgentListedProcessNodeInheritedFrom

func taskInheritedFromDTO(inherited *taskListedInherited) *inheritedFromDTO {
	if inherited == nil {
		return nil
	}
	return &inheritedFromDTO{AgentID: inherited.AgentId, AgentUrn: inherited.AgentUrn}
}

func processInheritedFromDTO(inherited *processListedInherited) *inheritedFromDTO {
	if inherited == nil {
		return nil
	}
	return &inheritedFromDTO{AgentID: inherited.AgentId, AgentUrn: inherited.AgentUrn}
}

// applyAgentIndexRefChanges applies --tasks-index/--processes-index: a value
// SETS the ref, an explicit empty CLEARS it, an absent flag PRESERVES it —
// the server's three-state contract (an omitted argument preserves, an
// explicit null clears). Value flags share one call; each cleared ref takes
// its own clear call, which preserves the other by omitting it.
func applyAgentIndexRefChanges(ctx context.Context, client graphql.Client, ref string, tasksChanged bool, tasksIndex string, processesChanged bool, processesIndex string) (*gen.AgentFields, error) {
	var tasksVal, processesVal *string
	if tasksChanged && tasksIndex != "" {
		t := tasksIndex
		tasksVal = &t
	}
	if processesChanged && processesIndex != "" {
		p := processesIndex
		processesVal = &p
	}
	var fields *gen.AgentFields
	if tasksVal != nil || processesVal != nil {
		resp, err := gen.SetAgentIndexRefs(ctx, client, ref, tasksVal, processesVal)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.SetAgentIndexRefs != nil {
			fields = &resp.SetAgentIndexRefs.AgentFields
		}
	}
	if tasksChanged && tasksIndex == "" {
		resp, err := gen.ClearAgentTasksIndex(ctx, client, ref, nil)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.SetAgentIndexRefs != nil {
			fields = &resp.SetAgentIndexRefs.AgentFields
		}
	}
	if processesChanged && processesIndex == "" {
		resp, err := gen.ClearAgentProcessesIndex(ctx, client, ref, nil)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.SetAgentIndexRefs != nil {
			fields = &resp.SetAgentIndexRefs.AgentFields
		}
	}
	return fields, nil
}

// newCmdAgentSetBase backs `hadron agent set-base` (#881, cor:dmo:050:14):
// a positional base sets the Agent's base; --clear removes it. Exactly one of
// the two is required — the command is deliberately explicit, because
// inheritance changes what every worker of the Agent sees.
func newCmdAgentSetBase(f *cmdutil.Factory) *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "set-base <agent> (<base> | --clear)",
		Short: "Set or clear the Agent's base (inheritance, cor:dmo:050:14)",
		Long: `Set the base this Agent extends, or clear it with --clear. The base's
system memory is inherited: its tasks and processes lists, and the nodes its
workers read, come through the chain (deduped by the server), and a chain may
hold at most four Agents. Setting a base needs a licence to it — a PUBLIC
base needs an active subscription, an org-owned one an org grant. Whoever
manages the Agent may set its base (cor:dmo:050:13).`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "the agent reference is empty")
			}
			var baseRef *string
			switch {
			case clear && len(args) == 2:
				return exitcode.Newf(exitcode.Usage, "pass either a base or --clear, not both")
			case clear:
				// An omitted baseRef is the server's clear.
			case len(args) == 2:
				b := strings.TrimSpace(args[1])
				if b == "" {
					return exitcode.Newf(exitcode.Usage, "the base reference is empty")
				}
				baseRef = &b
			default:
				return exitcode.Newf(exitcode.Usage, "pass the base to extend, or --clear to remove it")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetAgentBase(cmd.Context(), client, ref, baseRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp.SetAgentBase == nil {
				return exitcode.Newf(exitcode.Error, "server returned no agent")
			}
			verb := "✓ set base of"
			if clear {
				verb = "✓ cleared base of"
			}
			return emitAgent(f, agentDTOFromFields(resp.SetAgentBase.AgentFields), verb)
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the Agent's base instead of setting one")
	return cmd
}

// agentRow carries the shared AgentFields of one list item.
type agentRow struct{ *gen.AgentFields }

// listPages runs one deliberate page when the user passed --limit/--offset,
// and otherwise drains the whole list (cli#967): an unset limit never means
// "whatever the server defaults to".
func listPages(lim, off *int, fetch func(limit, offset *int) ([]*agentRow, int, error)) ([]*agentRow, error) {
	if lim != nil || off != nil {
		rows, _, err := fetch(lim, off)
		return rows, err
	}
	return api.CollectAll(func(l, o int) ([]*agentRow, int, error) { return fetch(&l, &o) })
}
