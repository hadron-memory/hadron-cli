package team

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"

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

const teamPauseHelp = `App scope comes from --app, the active context or this worktree's binding.
This is the signed-in person's write: no worker session is attached; the
server checks permission.

Which pause to use:
  team pause: hold regular wakes across this App, without interrupting sessions.
  team wake-settings --paused-until: the worker's own pause for its schedule.
  team stop: stop signals for chosen workers at a level and scope; see
    team stop --help for availability restrictions before setting a signal.

While the App is paused, workers show PENDING with reason TEAM_PAUSED on the
roster. Pausing again keeps the original timestamp. Resuming clears only the
App's team pause, not worker self-pauses or stop signals; lifting it restarts
every worker's overdue timer (the roster's OVERDUE clock). Resuming an unpaused
team stamps nothing. The result is the server's stored state, not proof that
any worker stopped or resumed.`

func newCmdTeamPause(f *cmdutil.Factory, paused bool) *cobra.Command {
	verb := "resume"
	short := "Clear the App's team pause"
	opening := "Lift the App's team pause as its manager. This permits regular wakes again."
	if paused {
		verb = "pause"
		short = "Pause the App's regular worker wakes"
		opening = "Pause the App's regular worker wakes as its manager, without interrupting open sessions."
	}
	return &cobra.Command{
		Use: verb, Short: short, Long: opening + "\n\n" + teamPauseHelp,
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
	var agent, instruction string
	var yes bool
	c := &cobra.Command{
		Use:   "change-agent <name|id|URN> --agent <id|URN> [--instruction <kind>:<ref>] [--yes]",
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

Who may change it: an App manager, as themselves. Without --instruction this
is always the signed-in person's write: it attaches no bound worker session,
even from a bound worktree. With --instruction it goes out through this
worktree's worker session when that binding is for this server and the
worker's own App, and is unchanged since the command started; the change is
then admitted when the worker's holder manages the App. Otherwise it goes out
as the signed-in person (a note on stderr says why), with no worker named;
the server still checks the person's reference, and refuses a bad one.

` + instructionHelp + `

The CLI never retries the mutation; after a lost response, inspect the worker
before deciding what to do next.`,
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
			var instr *gen.ActInstructionInput
			if cmd.Flags().Changed("instruction") {
				var err error
				if instr, err = parseInstruction(instruction); err != nil {
					return err
				}
			}
			// Direct refs identify their own App; ambient context must never be
			// presented as their destination. No network read precedes confirmation.
			direct := cmdutil.IsEntityID(workerRef) || strings.HasPrefix(strings.ToLower(workerRef), "hrn:worker:") || strings.HasPrefix(strings.ToLower(workerRef), "urn:worker:")
			source := "from the worker record"
			location := "in the worker's own App"
			if !direct {
				b, err := readBindingOrNilWithApp(cmd.Context(), f)
				if err != nil {
					return err
				}
				app, err := f.App()
				if err != nil {
					return err
				}
				source = "from the App context"
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
				if app == "" {
					return exitcode.Newf(exitcode.Usage, "a worker name needs App scope: pass --app <app>, set an App context or bind a worktree")
				}
				location = fmt.Sprintf("in App %s (%s)", app, source)
			}
			prompt := fmt.Sprintf("Change worker %s %s to agent %s, replacing its current casting? This clears its boot-prompt pointer (a custom boot-prompt selection) and replaces agent-generated tickers (wake schedules, including edits to those schedules). Hand-created tickers stay. An open session keeps its old agent and role; the change takes effect at the next bind.", workerRef, location, agent)
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			// An instructed write may go out only through the binding as it
			// stood before the first lookup (cli#1592).
			var snap instructionSnapshot
			if instr != nil {
				snap = snapshotForInstruction(cmd.Context())
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var worker gen.WorkerFields
			if direct {
				// An id/URN is App-independent: do not scan ambient staff first.
				result, err := gen.GetWorker(cmd.Context(), client, workerRef)
				if err != nil {
					return api.MapErrorKeepingCode(err)
				}
				if result == nil || result.Worker == nil {
					return exitcode.Newf(exitcode.NotFound, "no worker %q", workerRef)
				}
				worker = result.Worker.WorkerFields
			} else {
				worker, err = workerForArg(cmd, f, workerRef)
				if err != nil {
					return err
				}
			}
			changed, err := sendChangeWorkerAgent(cmd.Context(), f, client, worker, agent, instr, snap)
			if err != nil {
				return err
			}
			dto := workerDTOFromFields(changed)
			if direct {
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
	c.Flags().StringVar(&instruction, "instruction", "", instructionFlagUsage)
	c.Flags().BoolVar(&yes, "yes", false, "confirm clearing the custom boot-prompt selection and replacing agent-generated wake schedules")
	return c
}

// sendChangeWorkerAgent makes the one change-agent write. Without an
// instruction it is the plain document, headerless: the person's write, as
// before cli#1592. With one it is the instruction document, through the
// snapshot's session only when that binding is verified for the target
// worker's App; otherwise headerless, borrowing no worker. Never retried.
func sendChangeWorkerAgent(ctx context.Context, f *cmdutil.Factory, client graphql.Client, worker gen.WorkerFields, agent string, instr *gen.ActInstructionInput, snap instructionSnapshot) (gen.WorkerFields, error) {
	var zero gen.WorkerFields
	if instr == nil {
		resp, err := gen.ChangeWorkerAgent(ctx, client, worker.Id, agent)
		if err != nil {
			return zero, api.MapErrorKeepingCode(err)
		}
		if resp == nil || resp.ChangeWorkerAgent == nil {
			return zero, exitcode.Newf(exitcode.Error, "server returned no changed worker; outcome unknown, inspect the worker before retrying")
		}
		return resp.ChangeWorkerAgent.WorkerFields, nil
	}
	session, why, err := instructedSession(ctx, f, snap, worker.AppId)
	if err != nil {
		return zero, err
	}
	if why != "" {
		fmt.Fprintf(f.IOStreams.ErrOut, "note: this worktree's worker session is not used (%s); the change goes out as the signed-in person, with the instruction checked but not recorded\n", why)
	}
	if session != "" {
		ctx = api.WithSession(ctx, session)
	}
	resp, err := gen.ChangeWorkerAgentInstructed(ctx, client, worker.Id, agent, instr)
	if err != nil {
		return zero, api.MapErrorKeepingCode(err)
	}
	if resp == nil || resp.ChangeWorkerAgent == nil {
		return zero, exitcode.Newf(exitcode.Error, "server returned no changed worker; outcome unknown, inspect the worker before retrying")
	}
	return resp.ChangeWorkerAgent.WorkerFields, nil
}
