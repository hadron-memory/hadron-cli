package team

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

func newCmdPersona(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "persona <command>",
		Aliases: []string{"personas"},
		Short:   "Manage the persona roster",
		Long: `A persona is an Agent with a persona name ("Iris"), a role, and an identity
prompt. The name binds to one persona forever: retiring a persona keeps its
name reserved (PR trailers and chat history reference it), so there is no
persona rm — only retire.`,
	}
	cmd.AddCommand(newCmdPersonaCreate(f))
	cmd.AddCommand(newCmdPersonaList(f))
	cmd.AddCommand(newCmdPersonaGet(f))
	cmd.AddCommand(newCmdPersonaRetire(f))
	return cmd
}

func newCmdPersonaCreate(f *cmdutil.Factory) *cobra.Command {
	var role, name, teamAgent string
	cmd := &cobra.Command{
		Use:   "create --role <role> [--name <n>] [--team-agent <ref>]",
		Short: "Mint a persona for a role (server-side allocation and install)",
		Long: `Mint a persona: ONE platform call (createTeamPersona, spec cor:agt:020:01).
The server locates the team App's Team Agent, reads the role node
(roles:<role> in its system memory — prompt template with {{name}}/{{role}}
placeholders, ordered name register in data.names), allocates a free name,
composes the persona prompt, creates the Agent, and installs it into the
App. The CLI passes the arguments through and retries nothing.

The team App comes from the persistent --app flag (or the configured App
context). --name overrides the register with an explicit name — only then
can PERSONA_NAME_TAKEN come back (a register-allocated name is retried
server-side). --team-agent picks the Team Agent explicitly when the App
installs several agents with roles branches.

The created persona's prompt is its boot briefing — printed on success.`,
		Example: `  hadron team persona create --app acme.com::eng-team --role backend-engineer
  hadron team persona create --app acme.com::eng-team --role qa --name Uma`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := f.App()
			if err != nil {
				return err
			}
			if appRef == "" {
				return exitcode.Newf(exitcode.Usage, "no team App — pass --app <ref> (or set a default App context)")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateTeamPersona(cmd.Context(), client, appRef, role, optStr(teamAgent), optStr(name))
			if err != nil {
				// Thin by directive: the server's typed errors carry the
				// guidance (PERSONA_ROLE_NOT_FOUND lists the available roles,
				// TEAM_AGENT_AMBIGUOUS says to pass teamAgentRef) — map to
				// exit codes, surface the message verbatim.
				return api.MapError(err)
			}
			if resp.CreateTeamPersona == nil {
				return exitcode.Newf(exitcode.Error, "server returned no persona")
			}
			dto := personaDTOFromFields(resp.CreateTeamPersona.PersonaAgentFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "✓ created persona %s%s (%s)\n", dto.PersonaName, roleSuffix(dto.PersonaRole), dto.URN)
				if dto.PersonaPrompt != nil && *dto.PersonaPrompt != "" {
					fmt.Fprintf(w, "\n%s\n", *dto.PersonaPrompt)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "the role to mint (a roles:<role> node in the Team Agent's system memory)")
	cmd.Flags().StringVar(&name, "name", "", "explicit persona name (overrides the register; taken-name refusals are yours to handle)")
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent ref, when the App installs more than one agent with roles")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newCmdPersonaList(f *cmdutil.Factory) *cobra.Command {
	var org, role string
	cmd := &cobra.Command{
		Use:     "list [--org <ref>] [--role <r>]",
		Aliases: []string{"ls"},
		Short:   "List the persona roster",
		Long: `List every persona you can read (agents with a persona name). The narrowing
is client-side — the server's agent filter has no persona clause — so the
whole agent list is paged through.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			rows, err := scanPersonaAgents(cmd.Context(), client, optStr(org))
			if err != nil {
				return err
			}
			personas := []personaDTO{}
			for _, r := range rows {
				dto := personaDTOFromFields(r)
				if role != "" && (dto.PersonaRole == nil || !strings.EqualFold(*dto.PersonaRole, role)) {
					continue
				}
				personas = append(personas, dto)
			}
			return output.Write(f.IOStreams, f.JSON, personas, func(w io.Writer) error {
				t := output.NewTable(w, "PERSONA", "ROLE", "AGENT URN", "ID")
				for _, p := range personas {
					t.Row(p.PersonaName, dash(p.PersonaRole), p.URN, p.ID)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "restrict to one organization (ID or URN)")
	cmd.Flags().StringVar(&role, "role", "", "filter by persona role (case-insensitive)")
	return cmd
}

func newCmdPersonaGet(f *cmdutil.Factory) *cobra.Command {
	var org string
	cmd := &cobra.Command{
		Use:   "get <name-or-ref>",
		Short: "Show a persona (by persona name, agent ID, or agent URN)",
		Example: `  hadron team persona get Iris
  hadron team persona get hrn:agent:acme.com:iris --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			p, err := resolvePersona(cmd.Context(), client, optStr(org), args[0])
			if err != nil {
				return err
			}
			dto := personaDTOFromFields(p)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "%s%s\n  agent: %s (%s)\n", dto.PersonaName, roleSuffix(dto.PersonaRole), dto.URN, dto.ID)
				fmt.Fprintf(w, "  description: %s\n", dash(dto.Description))
				if dto.PersonaPrompt != nil && *dto.PersonaPrompt != "" {
					fmt.Fprintf(w, "  prompt: %s\n", *dto.PersonaPrompt)
				}
				fmt.Fprintf(w, "  created: %s\n", dto.CreatedAt)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "disambiguate a persona name that exists in more than one org")
	return cmd
}

// retireResultDTO is the stable --json shape of `persona retire`.
type retireResultDTO struct {
	ID          string `json:"id"`
	PersonaName string `json:"personaName"`
	Status      string `json:"status"`
}

func newCmdPersonaRetire(f *cmdutil.Factory) *cobra.Command {
	var org string
	var yes bool
	cmd := &cobra.Command{
		Use:   "retire <name-or-ref>",
		Short: "Retire a persona — permanent; its name is never re-minted",
		Long: `Retire a persona. Retirement is permanent: the persona leaves the roster,
but its name stays bound to it forever — PR trailers and chat history
reference the name, so it is never freed for a new persona. (This is why the
command is retire, not rm.)`,
		Example: `  hadron team persona retire Iris --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			p, err := resolvePersona(cmd.Context(), client, optStr(org), args[0])
			if err != nil {
				return err
			}
			dto := personaDTOFromFields(p)
			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Retire persona %s (%s)? Retirement is permanent and the name is never re-minted.", dto.PersonaName, dto.URN)); err != nil {
				return err
			}
			resp, err := gen.DeleteAgent(cmd.Context(), client, p.Id)
			if err != nil {
				return api.MapError(err)
			}
			if !resp.DeleteAgent {
				return exitcode.Newf(exitcode.Error, "persona %s was not retired", dto.PersonaName)
			}
			result := retireResultDTO{ID: dto.ID, PersonaName: dto.PersonaName, Status: "retired"}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ retired persona %s — the name stays bound to it and is never re-minted\n", dto.PersonaName)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "disambiguate a persona name that exists in more than one org")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func roleSuffix(role *string) string {
	if role == nil || *role == "" {
		return ""
	}
	return " (" + *role + ")"
}

func dash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}
