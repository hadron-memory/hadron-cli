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

func newCmdWorker(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "worker <command>",
		Aliases: []string{"workers", "staff"},
		Short:   "Manage the team's staff — named castings of installed agents",
		Long: `A worker is the NAMED CASTING of an installed agent into this App
(cor:dmo:050:11): "Iris" is the backend-engineer agent cast into the
eng-team App. The name is unique per App and binds to this one casting
forever — retiring a worker keeps its name reserved (PR trailers and chat
history reference it), so there is no rename, and ` + "`worker rm`" + ` only removes
a casting that never did anything.

The worker carries the identity; the agent carries the reusable persona
dressing (role + prompt template, edited with ` + "`agent update`" + `). One agent
can be cast many times — Iris and Henry can both be the backend-engineer.

Commands take the worker's name (resolved within the App from --app, the
App context, or the worktree binding), its URN, or its id. The URN
(hrn:worker:<root>:<app-slug>:<slug>, #991) keys on a permanent DERIVED
slug — never on the name, since two names may legally slugify to one token
— and needs no App context, which makes it the portable way to name a
worker in scripts.`,
	}
	cmd.AddCommand(newCmdWorkerCast(f))
	cmd.AddCommand(newCmdWorkerList(f))
	cmd.AddCommand(newCmdWorkerGet(f))
	cmd.AddCommand(newCmdWorkerRetire(f))
	cmd.AddCommand(newCmdWorkerRm(f))
	return cmd
}

// castPreviewDTO is the stable --json shape of `worker cast --dry-run` —
// scalars plus the ids behind each name (the actionable refs). Reserved is
// always false: the preview holds nothing (cor:agt:020:03 — no lease by law).
type castPreviewDTO struct {
	Name               string  `json:"name"`
	Role               *string `json:"role"`
	AgentID            string  `json:"agentId"`
	AgentName          string  `json:"agentName"`
	TeamAgentID        *string `json:"teamAgentId"`
	TeamAgentName      *string `json:"teamAgentName"`
	Prompt             *string `json:"prompt"`
	HasNamePlaceholder *bool   `json:"hasNamePlaceholder"`
	Reserved           bool    `json:"reserved"`
}

func newCmdWorkerCast(f *cmdutil.Factory) *cobra.Command {
	var role, name, agentRef, teamAgent, promptOverride string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "cast (--role <role> | --agent <ref>) [--name <n>] [--prompt-override <text>] [--dry-run]",
		Short: "Cast an installed agent as a named worker (server-side allocation)",
		Long: `Cast a worker: ONE platform call (castWorker, cor:agt:020:01). The server
resolves the agent — --agent names it directly (it must be installed in
this App), or --role picks the single installed agent whose persona role
matches (zero or several candidates refuse typed, never a guess) — then
allocates the name and provisions the worker's working memory.

Names (cor:agt:020:02): --name claims an explicit name in one attempt
(WORKER_NAME_TAKEN is the answer); without it the Team Agent's cast-list
register for the role (the roles:<role> node's data.names) is walked
server-side past taken names — so an App with no cast-list register
refuses (TEAM_AGENT_NOT_FOUND); pass --name there. Either way the name is
PERMANENT for this App: retirement and uninstall never free it.

--prompt-override layers per-worker individuality over the agent's prompt
template; the resolved boot briefing (template with {{name}}/{{role}}
bound, then the override) is printed on success.

--dry-run (#404, castWorkerPreview) runs the cast's EXACT resolution —
same arguments, same typed refusals, same mint gate — up to but not
including the writes, and shows what would be created: the name, the
agent, the composed prompt. A refusal on the dry run IS the answer that
the real cast would refuse the same way. THE PREVIEW RESERVES NOTHING:
no lease exists, so a previewed name may be gone at cast time.

The team App comes from the persistent --app flag (or the configured App
context). --team-agent picks the cast-list holder explicitly when the App
installs several agents with roles branches.`,
		Example: `  hadron team worker cast --app acme.com:eng-team --role backend-engineer
  hadron team worker cast --app acme.com:eng-team --agent hrn:agent:acme.com:qa --name Uma`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := f.App()
			if err != nil {
				return err
			}
			if appRef == "" {
				return exitcode.Newf(exitcode.Usage, "no team App — pass --app <ref> (or set a default App context)")
			}
			if role == "" && agentRef == "" {
				return exitcode.Newf(exitcode.Usage, "pass --role <role> or --agent <ref> — the server never guesses the agent to cast")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if dryRun {
				// A Query, not a dryRun flag on the mutation (#964): previewing
				// needs no mutation permission and mints no fake Worker row.
				// Same typed refusals as the cast — surfaced verbatim, because
				// a refusal here IS the dry-run's answer.
				preview, perr := gen.CastWorkerPreview(cmd.Context(), client, appRef,
					optStr(agentRef), optStr(role), optStr(name), optStr(teamAgent), optStr(promptOverride))
				if perr != nil {
					return api.MapError(perr)
				}
				if preview.CastWorkerPreview == nil {
					return exitcode.Newf(exitcode.Error, "server returned no preview")
				}
				p := preview.CastWorkerPreview
				dto := castPreviewDTO{
					Name: p.Name, Role: p.Role, AgentID: p.AgentId, AgentName: p.AgentName,
					TeamAgentID: p.TeamAgentId, TeamAgentName: p.TeamAgentName,
					Prompt: p.Prompt, HasNamePlaceholder: p.HasNamePlaceholder,
				}
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					fmt.Fprintf(w, "would cast %s%s — agent %s (%s)\n", dto.Name, roleSuffix(dto.Role), dto.AgentName, dto.AgentID)
					if dto.TeamAgentName != nil {
						fmt.Fprintf(w, "  name allocated by the cast-list register of %s\n", *dto.TeamAgentName)
					}
					if dto.HasNamePlaceholder != nil && !*dto.HasNamePlaceholder {
						fmt.Fprintf(w, "  ! the agent's prompt template never binds {{name}} — this worker would be nameless in its own briefing\n")
					}
					if dto.Prompt != nil && *dto.Prompt != "" {
						fmt.Fprintf(w, "\n%s\n", *dto.Prompt)
					}
					fmt.Fprintf(w, "\nnothing was created, and %q is NOT reserved — it may be gone at cast time\n", dto.Name)
					return nil
				})
			}
			resp, err := gen.CastWorker(cmd.Context(), client, appRef,
				optStr(agentRef), optStr(role), optStr(name), optStr(teamAgent), optStr(promptOverride))
			if err != nil {
				// Thin by directive: the server's typed errors carry the
				// guidance (WORKER_ROLE_NOT_FOUND lists the available roles,
				// WORKER_AGENT_AMBIGUOUS says to pass --agent) — map to exit
				// codes, surface the message verbatim.
				return api.MapError(err)
			}
			if resp.CastWorker == nil {
				return exitcode.Newf(exitcode.Error, "server returned no worker")
			}
			dto := workerDTOFromFields(resp.CastWorker.WorkerFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ cast %s%s — worker %s\n", dto.Name, roleSuffix(dto.Role), dto.ID); err != nil {
					return err
				}
				if dto.Prompt != nil && *dto.Prompt != "" {
					if _, err := fmt.Fprintf(w, "\n%s\n", *dto.Prompt); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "cast by role: the single installed agent whose persona role matches")
	cmd.Flags().StringVar(&agentRef, "agent", "", "cast this agent (ID or URN; must be installed in the App)")
	cmd.Flags().StringVar(&name, "name", "", "explicit worker name (overrides the register; taken-name refusals are yours to handle)")
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the cast-list roles, when the App installs more than one")
	cmd.Flags().StringVar(&promptOverride, "prompt-override", "", "per-worker individuality appended to the agent's prompt template")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview the cast (name, agent, composed prompt) without creating anything; reserves nothing")
	return cmd
}

func newCmdWorkerList(f *cmdutil.Factory) *cobra.Command {
	var includeRetired bool
	cmd := &cobra.Command{
		Use:     "list [--include-retired]",
		Aliases: []string{"ls"},
		Short:   "The team's staff — this App's workers",
		Long: `List the App's workers (cor:agt:020:01: the staff are the Workers; the
AppAgent join is the install roster, ` + "`hadron app agent list`" + `). The team
App resolves from --app (or the configured App context), falling back to
the worktree binding's team memory.

Retired workers are hidden unless --include-retired — their names stay
bound to them forever, so the retired list is also the reserved-name list.`,
		Example: `  hadron team worker list --app acme.com:eng-team
  hadron team worker list --include-retired --json`,
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
			rows, err := scanWorkers(ctx, client, appRef)
			if err != nil {
				return err
			}
			workers := []workerDTO{}
			for _, w := range rows {
				dto := workerDTOFromFields(w)
				if dto.Retired && !includeRetired {
					continue
				}
				workers = append(workers, dto)
			}
			return output.Write(f.IOStreams, f.JSON, workers, func(w io.Writer) error {
				t := output.NewTable(w, "WORKER", "ROLE", "RETIRED", "AGENT ID", "ID")
				for _, wk := range workers {
					retired := "—"
					if wk.RetiredAt != nil {
						retired = *wk.RetiredAt
					}
					t.Row(wk.Name, dash(wk.Role), retired, wk.AgentID, wk.ID)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&includeRetired, "include-retired", false, "include retired workers (their names stay reserved)")
	return cmd
}

// workerForArg resolves a worker command's positional argument: the App scope
// comes from --app / the App context / the worktree binding when available,
// and a worker id works without any of them.
func workerForArg(cmd *cobra.Command, f *cmdutil.Factory, arg string) (gen.WorkerFields, error) {
	var zero gen.WorkerFields
	ctx := cmd.Context()
	client, err := f.GraphQLClient()
	if err != nil {
		return zero, err
	}
	b, err := readBindingOrNilWithApp(ctx, f)
	if err != nil {
		b = nil
	}
	appRef, err := resolveTeamApp(ctx, f, b)
	if err != nil {
		appRef = "" // no App scope — resolveWorker still handles the id form
	}
	return resolveWorker(ctx, client, appRef, arg)
}

func newCmdWorkerGet(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <name-or-id>",
		Short: "Show a worker (by name within the App, or by id)",
		Example: `  hadron team worker get Iris
  hadron team worker get wkr_123 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := workerForArg(cmd, f, args[0])
			if err != nil {
				return err
			}
			dto := workerDTOFromFields(w)
			return output.Write(f.IOStreams, f.JSON, dto, func(out io.Writer) error {
				fmt.Fprintf(out, "%s%s\n  worker: %s (app %s)\n  agent: %s\n", dto.Name, roleSuffix(dto.Role), dto.ID, dto.AppID, dto.AgentID)
				if dto.URN != nil && *dto.URN != "" {
					fmt.Fprintf(out, "  urn: %s\n", *dto.URN)
				}
				if dto.MemoryID != nil {
					fmt.Fprintf(out, "  memory: %s\n", *dto.MemoryID)
				}
				if dto.Retired {
					fmt.Fprintf(out, "  retired: %s\n", *dto.RetiredAt)
				}
				fmt.Fprintf(out, "  created: %s\n", dto.CreatedAt)
				if dto.Prompt != nil && *dto.Prompt != "" {
					fmt.Fprintf(out, "\n%s\n", *dto.Prompt)
				}
				return nil
			})
		},
	}
	return cmd
}

// retireResultDTO is the stable --json shape of `worker retire`.
type retireResultDTO struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	RetiredAt *string `json:"retiredAt"`
	Status    string  `json:"status"`
}

func newCmdWorkerRetire(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "retire <name-or-id>",
		Short: "Retire a worker — it stops working; its name stays reserved forever",
		Long: `Retire a worker: it stops authoring and takes no new sessions, and its
name stays bound to it forever — PR trailers and chat history reference the
name, so it is never freed for a new casting (cor:agt:020:02). Retiring is
idempotent; there is no un-retire.

This is the normal end of a worker. The only removal is ` + "`worker rm`" + `, the
hard-delete escape for a casting that NEVER did anything (a typo'd cast);
anything with history refuses it and retires instead.`,
		Example: `  hadron team worker retire Iris --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := workerForArg(cmd, f, args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Retire worker %s (%s)? It stops working; the name stays reserved for it forever.", w.Name, w.Id)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RetireWorker(cmd.Context(), client, w.Id)
			if err != nil {
				return api.MapError(err)
			}
			if resp.RetireWorker == nil {
				return exitcode.Newf(exitcode.Error, "server returned no worker")
			}
			dto := workerDTOFromFields(resp.RetireWorker.WorkerFields)
			result := retireResultDTO{ID: dto.ID, Name: dto.Name, RetiredAt: dto.RetiredAt, Status: "retired"}
			return output.Write(f.IOStreams, f.JSON, result, func(out io.Writer) error {
				_, err := fmt.Fprintf(out, "✓ retired worker %s — the name stays bound to it and is never re-cast\n", dto.Name)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// removeResultDTO is the stable --json shape of `worker rm`.
type removeResultDTO struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func newCmdWorkerRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <name-or-id>",
		Aliases: []string{"delete"},
		Short:   "Hard-delete a NEVER-USED casting (frees its name)",
		Long: `Hard-delete a worker that never did anything — the one removal escape
(cor:dmo:050:11), for a miscast: wrong role, typo'd name. The server refuses
with WORKER_IN_USE when any session was ever bound to it or its working
memory holds content; a worker with history retires instead
(` + "`worker retire`" + `), keeping its name reserved. Deleting also removes the
empty working memory, and — unlike retiring — frees the name.`,
		Example: `  hadron team worker rm Irsi --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := workerForArg(cmd, f, args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes,
				fmt.Sprintf("worker %s (%s) — only a never-used casting can be deleted; this frees its name", w.Name, w.Id)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.DeleteWorker(cmd.Context(), client, w.Id)
			if err != nil {
				return api.MapError(err)
			}
			if !resp.DeleteWorker {
				return exitcode.Newf(exitcode.Error, "worker %s was not deleted", w.Name)
			}
			result := removeResultDTO{ID: w.Id, Name: w.Name, Status: "deleted"}
			return output.Write(f.IOStreams, f.JSON, result, func(out io.Writer) error {
				_, err := fmt.Fprintf(out, "✓ deleted never-used worker %s — its name is free again\n", w.Name)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
