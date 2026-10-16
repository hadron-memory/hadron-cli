package team

// `team router` — the Orca attention router, ported from hadron-server's
// scripts/orca-attention-router (cli#829, T7). The engine is
// internal/teamrouter; this file is configuration lookup, the loop and the
// output contract.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/teamrouter"
)

// RouterConfigEnv names the router config when --config is absent. The TS
// router read ORCA_ROUTER_CONFIG; both are honoured, this one first.
const RouterConfigEnv = "HADRON_ROUTER_CONFIG"

const legacyRouterConfigEnv = "ORCA_ROUTER_CONFIG"

// routerConfigPath resolves the config: --config, then the env vars, then
// the CLI's own <config dir>/team-router/config.json, then the TS router's
// ~/.config/hadron-orca-attention-router/config.json — so an existing
// install runs unchanged, on the same state directory and the same writer
// lock (a TS and a Go run therefore exclude each other).
func routerConfigPath(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	for _, env := range []string{RouterConfigEnv, legacyRouterConfigEnv} {
		if v := os.Getenv(env); v != "" {
			return v, nil
		}
	}
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	own := filepath.Join(dir, "team-router", "config.json")
	if _, err := os.Stat(own); err == nil {
		return own, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		legacy := filepath.Join(home, ".config", "hadron-orca-attention-router", "config.json")
		if _, err := os.Stat(legacy); err == nil {
			return legacy, nil
		}
	}
	return own, nil
}

func loadRouterConfig(flag string) (*teamrouter.Config, error) {
	path, err := routerConfigPath(flag)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, exitcode.Newf(exitcode.Usage, "no router config at %s (pass --config, or set %s)", path, RouterConfigEnv)
	}
	c, err := teamrouter.LoadConfig(path)
	if err != nil {
		return nil, exitcode.New(exitcode.Usage, err)
	}
	return c, nil
}

// routerOutcomeDTO is the stable --json shape of one poll.
type routerOutcomeDTO struct {
	At              string            `json:"at"`
	OK              bool              `json:"ok"`
	Error           *string           `json:"error"`
	Nudged          []string          `json:"nudged"`
	Dropped         []string          `json:"dropped"`
	Covered         []string          `json:"covered"`
	Held            map[string]string `json:"held"`
	Adopted         bool              `json:"adopted"`
	Liveness        []string          `json:"liveness"`
	LivenessDropped []string          `json:"livenessDropped"`
}

func outcomeDTO(at time.Time, o *teamrouter.Outcome, err error) routerOutcomeDTO {
	dto := routerOutcomeDTO{At: at.UTC().Format(time.RFC3339), OK: err == nil, Nudged: []string{}, Dropped: []string{},
		Covered: []string{}, Held: map[string]string{}, Liveness: []string{}, LivenessDropped: []string{}}
	if err != nil {
		msg := err.Error()
		dto.Error = &msg
		return dto
	}
	dto.Nudged, dto.Dropped, dto.Covered = o.Nudged, o.Dropped, o.Covered
	dto.Held, dto.Adopted = o.Held, o.Adopted
	dto.Liveness, dto.LivenessDropped = o.Liveness, o.LivenessDropped
	return dto
}

func newCmdTeamRouter(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "router <command>",
		Short: "Nudge Orca-hosted workers when they are mentioned (the Orca attention router)",
		Long: `Run the Orca attention router: poll the team App's attention with YOUR
login, and type one short line into each mentioned roster worker's Orca
terminal telling it where to start reading. It never reads, relays or posts a
chat message, never marks anything read, and never hands its token to a
worker. (Ported from hadron-server's scripts/orca-attention-router, cli#829.)

A nudge is typed only on two signals: Orca proves the terminal idle (or, for
a terminal Orca stopped tracking, the rendered screen proves an idle agent),
AND the screen proves the prompt empty — nobody is typing. Counts are
recomputed immediately before the send, and both proofs are re-checked after
that. Anything unproven HOLDS the worker, and a held worker keeps the
attention token unadopted, so it is retried next poll without re-nudging the
others.

CONFIG is router.ts's JSON, unchanged: --config, else $` + RouterConfigEnv + `
(or $` + legacyRouterConfigEnv + `), else ~/.config/hadron/team-router/config.json,
else the TS router's ~/.config/hadron-orca-attention-router/config.json. Its
stateDir holds the adopted token, the delivery ledger and the liveness clocks
(0700/0600), in the TS router's format, behind the same writer lock: point
both at one state dir and they exclude each other.

  {"app": "<app ref>", "channel": "<team Channel id>", "stateDir": "~/.local/state/hadron-team-router",
   "orcaCli": "orca", "busyWaitMs": 60000, "submitWaitS": 15, "coordinatorWatchMinutes": 20,
   "roster": [{"worker": "<id>", "name": "Eli", "worktree": "~/orca/workspaces/x/eli",
               "terminal": "<optional pinned handle>", "coordinator": false, "lifecycle": false}]}

Each roster worker needs its own mention-only register row on the team
Channel, or it is held (a broader row would wake it on every message).`,
	}
	cmd.AddCommand(newCmdTeamRouterPoll(f), newCmdTeamRouterLoop(f), newCmdTeamRouterStatus(f))
	return cmd
}

// routerRun is one poll's wiring: the config, the server reads and the orca runner.
type routerRun struct {
	cfg  *teamrouter.Config
	srv  teamrouter.Server
	orca teamrouter.OrcaRunner
}

func newRouterRun(f *cmdutil.Factory, configFlag string) (*routerRun, error) {
	cfg, err := loadRouterConfig(configFlag)
	if err != nil {
		return nil, err
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return nil, err
	}
	return &routerRun{cfg: cfg, srv: teamrouter.NewGraphQLServer(client, cfg.App, cfg.Channel),
		orca: teamrouter.ExecOrca(cfg.OrcaCli)}, nil
}

// pollOnce runs one poll. A RouterError is the poll's own verdict (token
// unchanged); anything else is mapped through the CLI's error contract.
func (r *routerRun) pollOnce(ctx context.Context, logw io.Writer) (*teamrouter.Outcome, error) {
	log := func(line string) { fmt.Fprintf(logw, "%s %s\n", time.Now().UTC().Format(time.RFC3339), line) }
	out, err := teamrouter.PollOnce(ctx, r.cfg, r.srv, r.orca, log, nil)
	if err != nil {
		var rerr *teamrouter.RouterError
		if errors.As(err, &rerr) {
			return nil, exitcode.New(exitcode.Error, fmt.Errorf("%s (token unchanged)", rerr.Reason))
		}
		return nil, api.MapError(err)
	}
	return out, nil
}

func newCmdTeamRouterPoll(f *cmdutil.Factory) *cobra.Command {
	var configFlag string
	cmd := &cobra.Command{
		Use:   "poll [--config <path>]",
		Short: "Run one router poll",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			run, err := newRouterRun(f, configFlag)
			if err != nil {
				return err
			}
			// Router log lines go to stderr under --json, keeping stdout one document.
			logw := f.IOStreams.Out
			if f.JSON {
				logw = f.IOStreams.ErrOut
			}
			out, pollErr := run.pollOnce(context.WithoutCancel(cmd.Context()), logw)
			at := time.Now()
			if f.JSON {
				if err := output.WriteJSON(f.IOStreams.Out, outcomeDTO(at, out, pollErr)); err != nil {
					return err
				}
				if pollErr != nil {
					return exitcode.Silent(exitcode.FromError(pollErr))
				}
				return nil
			}
			if pollErr != nil {
				return pollErr
			}
			fmt.Fprintf(f.IOStreams.Out, "%s %s\n", at.UTC().Format(time.RFC3339), teamrouter.DescribeOutcome(out))
			return nil
		},
	}
	cmd.Flags().StringVar(&configFlag, "config", "", "router config `path` (default: see team router --help)")
	return cmd
}

func newCmdTeamRouterLoop(f *cmdutil.Factory) *cobra.Command {
	var configFlag string
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "loop [--config <path>] [--interval 60s]",
		Short: "Poll forever in the foreground",
		Long: `Poll every --interval, in the foreground (the default runtime, by design:
the operator sees it run). A failed poll is logged and retried next interval,
with the token unchanged. A line is printed only for a poll that nudged or
held someone, or failed; --json prints one JSON object per such poll.

SIGINT/SIGTERM stop the loop AFTER the poll in flight finishes — a send is
never cut off between Orca's receipt and the ledger write. A second signal
exits at once. Configuration reloads between polls; config-loaded.json records
the loaded config fingerprint and time, without changing the adopted token.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if interval < time.Second {
				return exitcode.Newf(exitcode.Usage, "--interval must be at least 1s (got %s)", interval)
			}
			logw := f.IOStreams.Out
			if f.JSON {
				logw = f.IOStreams.ErrOut
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			for {
				// Reload only between complete deliveries. Invalid config stops the
				// loop rather than continuing to wake an obsolete roster.
				run, err := newRouterRun(f, configFlag)
				if err != nil {
					return err
				}
				if err := teamrouter.RecordReload(run.cfg); err != nil {
					return err
				}
				out, pollErr := run.pollOnce(context.WithoutCancel(ctx), logw)
				at := time.Now()
				noisy := pollErr != nil || len(out.Nudged) > 0 || len(out.Held) > 0 || len(out.Liveness) > 0
				switch {
				case f.JSON && noisy:
					if err := output.WriteJSON(f.IOStreams.Out, outcomeDTO(at, out, pollErr)); err != nil {
						return err
					}
				case pollErr != nil:
					fmt.Fprintf(f.IOStreams.ErrOut, "%s orca-attention-router: %v\n", at.UTC().Format(time.RFC3339), pollErr)
				case noisy:
					fmt.Fprintf(f.IOStreams.Out, "%s %s\n", at.UTC().Format(time.RFC3339), teamrouter.DescribeOutcome(out))
				}
				select {
				case <-ctx.Done():
					stop() // a second signal now takes the default action
					return nil
				case <-time.After(interval):
				}
			}
		},
	}
	cmd.Flags().StringVar(&configFlag, "config", "", "router config `path` (default: see team router --help)")
	cmd.Flags().DurationVar(&interval, "interval", 60*time.Second, "time between polls")
	return cmd
}

func newCmdTeamRouterStatus(f *cmdutil.Factory) *cobra.Command {
	var configFlag string
	cmd := &cobra.Command{
		Use:   "status [--config <path>]",
		Short: "Show the router's local state (no network)",
		Long: `Show the router's local state: whether a token is adopted (never the token
itself), who was nudged under it, each held worker's last logged reason,
whether the writer lock is held, the roster, and each flagged worker's
liveness clock. Reads only the state directory; no network.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadRouterConfig(configFlag)
			if err != nil {
				return err
			}
			rep, err := teamrouter.Status(cfg)
			if err != nil {
				return exitcode.New(exitcode.Error, err)
			}
			return output.Write(f.IOStreams, f.JSON, rep, func(w io.Writer) error {
				fmt.Fprintf(w, "state dir:     %s\n", rep.StateDir)
				fmt.Fprintf(w, "token adopted: %t\n", rep.TokenAdopted)
				fmt.Fprintf(w, "writer lock:   %s\n", map[bool]string{true: "held", false: "free"}[rep.Locked])
				fmt.Fprintf(w, "roster:        %d worker(s)\n", len(rep.Roster))
				if len(rep.NudgedUnderCurrentToken) > 0 {
					fmt.Fprintf(w, "nudged under the current token: %d\n", len(rep.NudgedUnderCurrentToken))
				}
				if len(rep.Held) > 0 {
					t := output.NewTable(w, "HELD", "LAST REPORTED REASON")
					for _, name := range rep.Roster {
						if reason, ok := rep.Held[name]; ok {
							t.Row(name, reason)
						}
					}
					if err := t.Flush(); err != nil {
						return err
					}
				}
				if len(rep.Liveness) > 0 {
					t := output.NewTable(w, "WATCHED", "IDLE MIN", "COVERED THROUGH")
					for _, name := range rep.Roster {
						ls, ok := rep.Liveness[name]
						if !ok {
							continue
						}
						idle, covered := "—", "—"
						if ls != nil && ls.IdleMinutes != nil {
							idle = fmt.Sprint(*ls.IdleMinutes)
						}
						if ls != nil && ls.CoveredFrontier != nil {
							covered = fmt.Sprintf("#%d", *ls.CoveredFrontier)
						}
						t.Row(name, idle, covered)
					}
					return t.Flush()
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&configFlag, "config", "", "router config `path` (default: see team router --help)")
	return cmd
}
