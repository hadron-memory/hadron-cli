package team

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type teamPauseDTO struct {
	App          string  `json:"app"`
	Paused       bool    `json:"paused"`
	TeamPausedAt *string `json:"teamPausedAt"`
}

const teamPauseHelp = `Set the App's team pause as its manager. App scope comes from --app, the
active context or this worktree's binding. This is the signed-in person's
write: no worker session is attached; the server checks permission.

While paused, regular worker wakes are held and workers report PENDING
(TEAM_PAUSED). This does not interrupt an open session. Stop notices are a
separate lane. This App setting is distinct from team stop --level pause and
from a worker's own team wake-settings --paused-until.

Pausing again preserves the original timestamp. Resuming clears only the
App's team pause, not worker self-pauses or stop signals; lifting it restarts
every worker's OVERDUE clock. Resuming an unpaused team stamps nothing.
The result is the
server's stored state, not proof that any worker stopped or resumed.`

func newCmdTeamPause(f *cmdutil.Factory, paused bool) *cobra.Command {
	verb := "resume"
	short := "Clear the App's team pause"
	if paused {
		verb = "pause"
		short = "Pause the App's regular worker wakes"
	}
	return &cobra.Command{
		Use: verb, Short: short, Long: teamPauseHelp,
		Example: "  hadron team " + verb + " --app <app> --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetTeamPause(ctx, client, scope.Ref, paused)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || resp.SetTeamPause == nil {
				return exitcode.Newf(exitcode.Error, "server returned no team pause result; outcome unknown, verify the App state before retrying")
			}
			at := resp.SetTeamPause.TeamPausedAt
			dto := teamPauseDTO{App: scope.Ref, Paused: at != nil, TeamPausedAt: at}
			label := lazyAppLabel(ctx, f, scope)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", label()); err != nil {
					return err
				}
				if at != nil {
					_, err := fmt.Fprintf(w, "team paused since %s\n", output.SafeCell(*at))
					return err
				}
				_, err := fmt.Fprintln(w, "team not paused")
				return err
			})
		},
	}
}

func newCmdWorkerChangeAgent(f *cmdutil.Factory) *cobra.Command {
	var agent string
	var yes bool
	c := &cobra.Command{
		Use:   "change-agent <name|id|URN> --agent <id|URN> [--yes]",
		Short: "Change a worker's casting agent as the App manager",
		Long: `Change a worker's agent and role. --agent takes an agent id or full agent
URN, not a name; the server checks that it is installed and has a role and
that the required grants are active. The worker may be named by name, id or
URN (a name resolves in --app, the App context or the worktree binding).
The result is the worker record in --json, or its identity, new agent and
role in text; the text receipt names the App and the source of its scope.

This clears the worker's boot-prompt pointer and replaces its generated
tickers. Confirm on a TTY, or pass --yes non-interactively. It takes effect at
the worker's next bind; an open session finishes under its old agent and role.
The name, slug, URN, signature, trailer and reporting line stay unchanged.
A server containing hadron-server#2504 is required to change an open worker.
Choosing the current agent is a refusal (WORKER_AGENT_UNCHANGED, exit 5).

This is the signed-in App manager's write: worker-session credentials are
refused (exit 8), so no bound worker session is attached. The CLI never retries the mutation; after a lost response, inspect
the worker before deciding what to do next.`,
		Example: "  hadron team worker change-agent Iris --agent hrn:agent:example.com:qa --yes --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workerRef := strings.TrimSpace(args[0])
			agent = strings.TrimSpace(agent)
			if workerRef == "" {
				return exitcode.Newf(exitcode.Usage, "worker reference is empty")
			}
			agentShape := cmdutil.IsEntityID(agent) || strings.HasPrefix(agent, "hrn:agent:") || strings.HasPrefix(agent, "urn:agent:")
			if !agentShape {
				return exitcode.Newf(exitcode.Usage, "--agent requires an agent id or full agent URN, not a name")
			}
			// Resolve only local context before confirmation: declining makes no requests.
			b, err := readBindingOrNilWithApp(cmd.Context(), f)
			if err != nil {
				return err
			}
			app, err := f.App()
			if err != nil {
				return err
			}
			source := "from the App context"
			if f.AppFlag != "" {
				source = "from --app"
			}
			if app == "" && b != nil {
				app = b.AppID
				source = "from the worktree binding"
				if app == "" {
					app = b.TeamMemory
					source = "from the worktree binding's team memory"
				}
			}
			scopeHint := app
			if scopeHint == "" {
				scopeHint = "identified by the worker reference"
			}
			prompt := fmt.Sprintf("Change worker %s in App %s (%s) to agent %s, clearing its boot-prompt pointer and replacing generated tickers?", workerRef, scopeHint, source, agent)
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			worker, err := workerForArg(cmd, f, workerRef)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.ChangeWorkerAgent(cmd.Context(), client, worker.Id, agent)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || resp.ChangeWorkerAgent == nil {
				return exitcode.Newf(exitcode.Error, "server returned no changed worker; outcome unknown, inspect the worker before retrying")
			}
			dto := workerDTOFromFields(resp.ChangeWorkerAgent.WorkerFields)
			if cmdutil.IsEntityID(workerRef) || strings.HasPrefix(workerRef, "hrn:worker:") || strings.HasPrefix(workerRef, "urn:worker:") {
				source = "from the worker record"
			}
			label := lazyAppLabel(cmd.Context(), f, appScope{Ref: dto.AppID, Source: source})
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "app: %s\nworker: %s (%s)\nagent: %s\nrole: %s\nTakes effect at the worker's next bind; an open session finishes under its old agent and role.\n", label(), output.SafeCell(dto.Name), output.SafeCell(dto.ID), output.SafeCell(dto.AgentID), output.SafeCell(stopValue(dto.Role)))
				return err
			})
		},
	}
	c.Flags().StringVar(&agent, "agent", "", "installed agent's id or full URN (required)")
	c.Flags().BoolVar(&yes, "yes", false, "confirm clearing the boot-prompt pointer and replacing generated tickers")
	return c
}
