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
	"strings"
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

// routerOutcomeDTO is the stable --json shape of one poll, for both sources.
// Fields a source does not produce are present and empty.
type routerOutcomeDTO struct {
	At     string   `json:"at"`
	Source string   `json:"source"`
	Shadow bool     `json:"shadow"`
	OK     bool     `json:"ok"`
	Error  *string  `json:"error"`
	Nudged []string `json:"nudged"`
	// Dropped: no longer due (or read) at the re-read just before the send.
	Dropped []string          `json:"dropped"`
	Held    map[string]string `json:"held"`
	// v1 (attention) only.
	Covered         []string `json:"covered"`
	Adopted         bool     `json:"adopted"`
	Liveness        []string `json:"liveness"`
	LivenessDropped []string `json:"livenessDropped"`
	// v2 (wakes) only.
	AckFailed map[string]string `json:"ackFailed"`
	Recovered []string          `json:"recovered"`
	Would     map[string]string `json:"would"`
	Status    *string           `json:"status"`
	Withheld  int               `json:"withheld"`
	Dormant   []string          `json:"dormant"`
}

func emptyOutcomeDTO(at time.Time, source string, shadow bool, err error) routerOutcomeDTO {
	dto := routerOutcomeDTO{At: at.UTC().Format(time.RFC3339), Source: source, Shadow: shadow, OK: err == nil,
		Nudged: []string{}, Dropped: []string{}, Held: map[string]string{}, Covered: []string{}, Liveness: []string{},
		LivenessDropped: []string{}, AckFailed: map[string]string{}, Recovered: []string{}, Would: map[string]string{},
		Dormant: []string{}}
	if err != nil {
		msg := err.Error()
		dto.Error = &msg
	}
	return dto
}

// pollResult is one poll, rendered for both outputs.
type pollResult struct {
	dto   routerOutcomeDTO
	line  string
	noisy bool
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

// routerRun is one poll's wiring: the config, the chosen source's server
// seam and the orca runner.
type routerRun struct {
	cfg    *teamrouter.Config
	source string
	shadow bool
	srv    teamrouter.Server
	wakes  teamrouter.WakeServer
	orca   teamrouter.OrcaRunner
}

func newRouterRun(f *cmdutil.Factory, configFlag, sourceFlag string, shadow bool) (*routerRun, error) {
	cfg, err := loadRouterConfig(configFlag)
	if err != nil {
		return nil, err
	}
	source := cfg.Source
	if sourceFlag != "" {
		if err := teamrouter.ValidSource(sourceFlag); err != nil {
			return nil, exitcode.New(exitcode.Usage, err)
		}
		source = sourceFlag
	}
	if shadow && source != teamrouter.SourceWakes {
		return nil, exitcode.Newf(exitcode.Usage, "--shadow runs the %q source only (pass --source %s)", teamrouter.SourceWakes, teamrouter.SourceWakes)
	}
	run := &routerRun{cfg: cfg, source: source, shadow: shadow, orca: teamrouter.ExecOrca(cfg.OrcaCli)}
	if source == teamrouter.SourceAttention {
		client, err := f.GraphQLClient()
		if err != nil {
			return nil, err
		}
		run.srv = teamrouter.NewGraphQLServer(client, cfg.App, cfg.Channel)
		return run, nil
	}
	// v2 runs on the router's App key, never the operator's login, and with
	// no worker session: a client of its own, outside the factory's
	// session-attributing decorator (an App key bound to a session is
	// refused APP_KEY_SESSION_BOUND).
	if cfg.AppKeyFile == "" {
		return nil, exitcode.Newf(exitcode.Usage, `source %q needs "appKeyFile" in the router config`, teamrouter.SourceWakes)
	}
	key, err := teamrouter.ReadAppKeyFile(cfg.AppKeyFile)
	if err != nil {
		return nil, exitcode.New(exitcode.Usage, err)
	}
	server, err := f.Server()
	if err != nil {
		return nil, err
	}
	client, err := api.NewClient(server, key, f.HTTPClient)
	if err != nil {
		return nil, exitcode.New(exitcode.Usage, err)
	}
	run.wakes = teamrouter.NewGraphQLWakeServer(client, cfg.App)
	return run, nil
}

// pollOnce runs one poll of the chosen source. A RouterError is the poll's
// own verdict; anything else is mapped through the CLI's error contract.
func (r *routerRun) pollOnce(ctx context.Context, logw io.Writer) (pollResult, error) {
	log := func(line string) { fmt.Fprintf(logw, "%s %s\n", time.Now().UTC().Format(time.RFC3339), line) }
	fail := func(err error, unchanged string) (pollResult, error) {
		var rerr *teamrouter.RouterError
		if errors.As(err, &rerr) {
			err = exitcode.New(exitcode.Error, fmt.Errorf("%s (%s)", rerr.Reason, unchanged))
		} else {
			err = api.MapError(err)
		}
		return pollResult{dto: emptyOutcomeDTO(time.Now(), r.source, r.shadow, err)}, err
	}
	if r.source == teamrouter.SourceAttention {
		out, err := teamrouter.PollOnce(ctx, r.cfg, r.srv, r.orca, log, nil)
		if err != nil {
			return fail(err, "token unchanged")
		}
		dto := emptyOutcomeDTO(time.Now(), r.source, false, nil)
		dto.Nudged, dto.Dropped, dto.Covered = out.Nudged, out.Dropped, out.Covered
		dto.Held, dto.Adopted = out.Held, out.Adopted
		dto.Liveness, dto.LivenessDropped = out.Liveness, out.LivenessDropped
		return pollResult{dto: dto, line: teamrouter.DescribeOutcome(out),
			noisy: len(out.Nudged) > 0 || len(out.Held) > 0 || len(out.Liveness) > 0}, nil
	}
	out, err := teamrouter.PollWakes(ctx, r.cfg, r.wakes, r.orca, log, nil, r.shadow)
	if err != nil {
		return fail(err, "nothing sent after the failure")
	}
	dto := emptyOutcomeDTO(time.Now(), r.source, r.shadow, nil)
	dto.Nudged, dto.Dropped, dto.Held = out.Nudged, out.Dropped, out.Held
	dto.AckFailed, dto.Recovered, dto.Would = out.AckFailed, out.Recovered, out.Would
	status := out.Status
	dto.Status, dto.Withheld, dto.Dormant = &status, out.Withheld, out.Dormant
	return pollResult{dto: dto, line: teamrouter.DescribeWakeOutcome(out),
		noisy: len(out.Nudged) > 0 || len(out.Held) > 0 || len(out.AckFailed) > 0 || len(out.Recovered) > 0 ||
			len(out.Would) > 0 || out.Status != "OK"}, nil
}

func addSourceFlags(cmd *cobra.Command, source *string, shadow *bool) {
	cmd.Flags().StringVar(source, "source", "", "delivery `source`: attention (v1, your login) or wakes (v2, the App key); default: the config's, else attention")
	cmd.Flags().BoolVar(shadow, "shadow", false, "with --source wakes: read due wakes and report who WOULD be woken; touch no terminal, send and acknowledge nothing")
}

func newCmdTeamRouterPoll(f *cmdutil.Factory) *cobra.Command {
	var configFlag, sourceFlag string
	var shadow bool
	cmd := &cobra.Command{
		Use:   "poll [--config <path>] [--source attention|wakes] [--shadow]",
		Short: "Run one router poll",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			run, err := newRouterRun(f, configFlag, sourceFlag, shadow)
			if err != nil {
				return err
			}
			// Router log lines go to stderr under --json, keeping stdout one document.
			logw := f.IOStreams.Out
			if f.JSON {
				logw = f.IOStreams.ErrOut
			}
			res, pollErr := run.pollOnce(context.WithoutCancel(cmd.Context()), logw)
			if f.JSON {
				if err := output.WriteJSON(f.IOStreams.Out, res.dto); err != nil {
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
			fmt.Fprintf(f.IOStreams.Out, "%s %s\n", res.dto.At, res.line)
			return nil
		},
	}
	cmd.Flags().StringVar(&configFlag, "config", "", "router config `path` (default: see team router --help)")
	addSourceFlags(cmd, &sourceFlag, &shadow)
	return cmd
}

func newCmdTeamRouterLoop(f *cmdutil.Factory) *cobra.Command {
	var configFlag, sourceFlag string
	var shadow bool
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "loop [--config <path>] [--interval 60s] [--source attention|wakes] [--shadow]",
		Short: "Poll forever in the foreground",
		Long: `Poll every --interval, in the foreground (the default runtime, by design:
the operator sees it run). A failed poll is logged and retried next interval,
with the token unchanged. A line is printed only for a poll that nudged or
held someone, or failed; --json prints one JSON object per such poll.

SIGINT/SIGTERM stop the loop AFTER the poll in flight finishes — a send is
never cut off between Orca's receipt and the ledger write. A second signal
exits at once.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if interval < time.Second {
				return exitcode.Newf(exitcode.Usage, "--interval must be at least 1s (got %s)", interval)
			}
			run, err := newRouterRun(f, configFlag, sourceFlag, shadow)
			if err != nil {
				return err
			}
			logw := f.IOStreams.Out
			if f.JSON {
				logw = f.IOStreams.ErrOut
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			for {
				res, pollErr := run.pollOnce(context.WithoutCancel(ctx), logw)
				noisy := pollErr != nil || res.noisy
				switch {
				case f.JSON && noisy:
					if err := output.WriteJSON(f.IOStreams.Out, res.dto); err != nil {
						return err
					}
				case pollErr != nil:
					fmt.Fprintf(f.IOStreams.ErrOut, "%s orca-attention-router: %v\n", res.dto.At, pollErr)
				case noisy:
					fmt.Fprintf(f.IOStreams.Out, "%s %s\n", res.dto.At, res.line)
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
	addSourceFlags(cmd, &sourceFlag, &shadow)
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
				if len(rep.PendingAcks) > 0 {
					fmt.Fprintf(w, "delivered, acknowledgement pending (v2): %s\n", strings.Join(rep.PendingAcks, ", "))
				}
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
