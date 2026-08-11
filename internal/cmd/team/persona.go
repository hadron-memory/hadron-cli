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
	var names []string
	var role, prompt, description, org string
	var ownerMe bool
	cmd := &cobra.Command{
		Use:   "create --name <candidate> [--name <fallback>]... [--role <r>] [--prompt <p>]",
		Short: "Mint a persona (an Agent with persona metadata)",
		Long: `Create a persona. --name is repeatable: candidates are tried in order, and
a name the server rejects as taken (persona names are unique per owner,
case-insensitively, and NEVER re-minted — a retired persona keeps its name)
falls through to the next candidate. The first free name wins and becomes
both the persona name and the agent name.

A candidate whose CHAT HANDLE (the name lowercased/dash-folded — what
@mentions address) collides with an existing persona's is also skipped:
persona-name uniqueness is server-enforced, but "Dev Rufus" and "Dev-Rufus"
would both answer to @dev-rufus, making chat attribution ambiguous.

Pass --org for an org-owned persona, or --owner-me (or neither) for one in
your own namespace.`,
		Example: `  hadron team persona create --org acme.com --name Iris --name Ivy --role backend-engineer \
      --prompt "You are Iris, a senior backend engineer ..."`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if ownerMe && org != "" {
				return exitcode.Newf(exitcode.Usage, "--owner-me mints the persona in your own namespace; drop --org (or drop --owner-me for an organization persona)")
			}
			candidates := []string{}
			for _, n := range names {
				if strings.TrimSpace(n) == "" {
					return exitcode.Newf(exitcode.Usage, "--name must not be empty")
				}
				candidates = append(candidates, strings.TrimSpace(n))
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Folded chat handles of the existing roster: a candidate that
			// collides is skipped like a taken name — the server enforces
			// name uniqueness, but only pre-fold, and two personas answering
			// to one @handle would make chat attribution ambiguous. A
			// client-side guard, so a concurrent create can still race past
			// it — the window is accepted (the server-side name uniqueness
			// stays the hard gate).
			takenHandles := map[string]bool{}
			roster, err := scanPersonaAgents(cmd.Context(), client, optStr(org))
			if err != nil {
				return err
			}
			for _, p := range roster {
				if p.PersonaName == nil {
					continue
				}
				if h, err := handleFromPersona(*p.PersonaName); err == nil {
					takenHandles[h] = true
				}
			}
			exhausted := func() error {
				return exitcode.Newf(exitcode.Conflict,
					"every candidate name is taken or handle-colliding (%s) — a persona name binds to one persona forever, retired names included; retry with fresh names",
					strings.Join(candidates, ", "))
			}
			for i, cand := range candidates {
				if h, err := handleFromPersona(cand); err == nil && takenHandles[h] {
					if i < len(candidates)-1 {
						fmt.Fprintf(f.IOStreams.ErrOut, "persona name %q folds to chat handle @%s, which an existing persona already answers to — trying %q\n", cand, h, candidates[i+1])
						continue
					}
					return exhausted()
				}
				resp, err := gen.CreatePersonaAgent(cmd.Context(), client, cand, cand,
					optStr(org), optStr(description), optStr(role), optStr(prompt))
				if err != nil {
					if api.HasErrorCode(err, "PERSONA_NAME_TAKEN") {
						if i < len(candidates)-1 {
							fmt.Fprintf(f.IOStreams.ErrOut, "persona name %q is taken — trying %q\n", cand, candidates[i+1])
							continue
						}
						return exhausted()
					}
					return api.MapError(err)
				}
				if resp.CreateAgent == nil {
					return exitcode.Newf(exitcode.Error, "server returned no agent")
				}
				dto := personaDTOFromFields(resp.CreateAgent.PersonaAgentFields)
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					_, err := fmt.Fprintf(w, "✓ created persona %s%s (%s)\n", dto.PersonaName, roleSuffix(dto.PersonaRole), dto.URN)
					return err
				})
			}
			// Unreachable: the loop returns on success and on the last taken name.
			return exitcode.Newf(exitcode.Error, "no candidate name was tried")
		},
	}
	cmd.Flags().StringArrayVar(&names, "name", nil, "persona name candidate, tried in order (repeatable)")
	cmd.Flags().StringVar(&role, "role", "", "persona role, e.g. backend-engineer")
	cmd.Flags().StringVar(&prompt, "prompt", "", "persona identity paragraph (composed into the system prompt by clients)")
	cmd.Flags().StringVar(&description, "description", "", "agent description")
	cmd.Flags().StringVar(&org, "org", "", "owning organization (ID or URN); omit for a persona you own")
	cmd.Flags().BoolVar(&ownerMe, "owner-me", false, "mint the persona in your own namespace (the default when --org is absent)")
	_ = cmd.MarkFlagRequired("name")
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
