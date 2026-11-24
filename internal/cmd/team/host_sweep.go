package team

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/healthsched"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/hostup"
	"github.com/spf13/cobra"
)

var sweepDaemonReady = func(ctx context.Context, cfg *hostpoller.Config) error {
	if !cfg.UsesOrcaLaunch() {
		return nil
	}
	return managedDaemonProbe(cfg)(ctx)
}

func newCmdTeamHostSweep(f *cmdutil.Factory) *cobra.Command {
	var configPath string
	c := &cobra.Command{Use: "sweep --config <host.yaml>", Short: "Run one host reconcile and record its outcome", Long: `Run one reconcile pass (the same pass as ` + "`team host up`" + `) using the
registered host id in the poller's state and worktrees_root from host YAML.
` + "`team host up`" + ` is the on-demand form with a desired state you name; this
one is keyed to the host's own registration — the form its installed
one-minute timer (` + "`sweep install`" + `) runs. The subcommands ` + "`sweep install|status|uninstall`" + `
manage that timer. The existing manager credential reconciles; a separate
key_file-only client records the run on the server (read back with
` + "`team host sweep-runs`" + `). No worker session is sent, and no chat message
is posted: the server owns summaries and alarms.

The App's sweep mode decides whether actions are allowed, and it is set with
` + "`hadron team app sweep-mode`" + `: OFF (the default) acts on nothing and records
nothing — a quiet result on an OFF App is the mode, not a fault; OBSERVE
compares and reports without repairs; APPLY allows repairs. Nothing is
planned unless the mode is APPLY.

A second concurrent pass exits immediately with another sweep holds the lock.
Open-session recovery belongs to the running poller's relaunch loop; this
command never starts a second agent directly. The 15-minute health timer and
5-minute report-stops timer remain explicitly separate: they still handle
health and unreported stop notices. Install creates only the new one-minute
sweep timer. Managed-daemon preflight is shared with the poller's
relaunch loop (every worker launches through Orca). A missing daemon scope or
an unreachable terminal list records ORCA_DOWN skips and queues nothing; unreadable or ambiguous readiness stays
RELAUNCH_UNPROVEN. The poller reports the first outage once and replays its
token until confirmed, backing off while the server's sweep-run records are
unavailable; it records only applied/failed passes, so this one-minute
command records the quiet passes. A failed record does not change the
reconciliation result. Default recovery target assumes 30s poll cadence and
two proven absence observations; a slow custom cadence can miss two minutes.
This command requests no daemon start, kill or restart; the service manager
owns it.

Exit codes: 0 the pass ran — including an expected unavailable observation,
which waits for the next timer tick; 2 --config missing or unreadable,
--app or --server disagreeing with the config (the config supplies the
sweep's App and server), or an exec-host preflight refusal (records
nothing, waits for the next tick, no service restart); 1 host state
unreadable or an unexpected error; 5 lock contention (another sweep holds
the lock) or no saved registration (start the host poller first). No
refusal restarts a service.`, Example: "  hadron team host sweep --config /etc/hadron/host.yaml",
		Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			started := time.Now()
			if configPath == "" {
				return exitcode.Newf(exitcode.Usage, "--config is required")
			}
			cfg, err := hostpoller.LoadConfig(configPath)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "cannot read host configuration")
			}
			state, err := hostpoller.LoadState(cfg.StateFile)
			if err != nil {
				return exitcode.Newf(exitcode.Error, "cannot read host state")
			}
			host := state.RegisteredHostID()
			if host == "" {
				return exitcode.Newf(exitcode.Conflict, "host has no saved registration; start the host poller first")
			}
			// The host config owns this sweep, regardless of ambient worktree scope.
			if f.AppFlag != "" && f.AppFlag != cfg.App {
				return exitcode.Newf(exitcode.Usage, "sweep App comes from --config; omit --app")
			}
			f.AppFlag = cfg.App
			if cfg.Server != "" {
				if f.ServerFlag != "" && f.ServerFlag != cfg.Server {
					return exitcode.Newf(exitcode.Usage, "sweep server comes from --config; omit --server")
				}
				f.ServerFlag = cfg.Server
			}
			orca := configuredOrcaBin(cfg) // never empty: it falls back to "orca"
			dir, err := config.Dir()
			if err != nil {
				return exitcode.New(exitcode.Error, err)
			}
			unlock, err := hostup.Lock(dir, host)
			if errors.Is(err, hostup.ErrLocked) {
				return exitcode.Newf(exitcode.Conflict, "another sweep holds the lock; nothing issued")
			}
			if err != nil {
				return exitcode.Newf(exitcode.Error, "sweep lock unavailable")
			}
			defer unlock()
			if err := sweepDaemonReady(cmd.Context(), cfg); err != nil {
				reason := "RELAUNCH_UNPROVEN"
				if errors.Is(err, hostpoller.ErrOrcaDown) {
					reason = "ORCA_DOWN"
				}
				return reportUnavailableSweep(cmd, f, cfg, configPath, host, reason, started)
			}
			return runHostUp(cmd, f, sweepHostUpOptions(host, cfg.WorktreesRoot, orca, configPath, cfg.UsesOrcaLaunch(), started))
		}}
	c.Flags().StringVar(&configPath, "config", "", "host configuration (required)")
	install := newScheduledInstall(f, healthsched.Sweep)
	install.Use = "install --config <host.yaml> [--bin <path>]"
	install.Example = "  hadron team host sweep install --config /etc/hadron/host.yaml"
	install.Short = "Install the one-minute host sweep systemd user timer"
	install.Long = `Install hadron-team-sweep.service and .timer (every minute) plus a fixed
OnFailure journal logger, with StartLimitBurst=3 in 60 seconds. Systemd user
units are required; cron and launchd are refused. No schedule hides in a script.
The host key stays in key_file; unit argv contains only the config path.
Existing health/report-stops timers are retained. Installing does not change
the App's sweepMode: only an App set to APPLY permits reconciliation actions.
Repeated install is idempotent; another config requires uninstall first.

Requires systemd 244 or newer: the service pairs Type=oneshot with
Restart=on-failure, which systemd 243 and older refuse to load. Exit codes 2 and 5
are not restarted at once (RestartPreventExitStatus); an unhandled Go panic also
exits 2, so it waits for the next one-minute tick.

The unit embeds no server or token. The scheduled user's normal login and server
configuration performs reconciliation; the 0600 key_file is used only to record
the sweep run on the server. Test drop-ins that pin a non-default server are not production setup.`
	if err := install.Flags().Set("interval", "1m"); err != nil {
		panic(err)
	}
	_ = install.Flags().MarkHidden("interval")
	run := install.RunE
	install.RunE = func(cmd *cobra.Command, args []string) error {
		if v, _ := cmd.Flags().GetString("interval"); v != "1m" {
			return exitcode.Newf(exitcode.Usage, "sweep interval is fixed at 1m")
		}
		return run(cmd, args)
	}
	uninstall := newScheduledUninstall(f, healthsched.Sweep)
	uninstall.Example = "  hadron team host sweep uninstall"
	uninstall.Short = "Remove only the host sweep timer and service"
	uninstall.Long = "Remove the host sweep systemd units, including its failure logger. Health and report-stops timers are retained."
	status := newScheduledStatus(f, healthsched.Sweep)
	status.Example = "  hadron team host sweep status --json"
	status.Short = "Inspect the host sweep scheduler"
	status.Long = "Inspect only the installed host sweep timer and its last run. Does not read server history;\nuse host sweep-runs for the server's recorded runs."
	c.AddCommand(install, uninstall, status)
	return c
}

// An unavailable daemon permits source reads and S10 metadata only. C5 owns
// the one failed outage announcement; this timer records quiet skipped rows.
func reportUnavailableSweep(cmd *cobra.Command, f *cmdutil.Factory, cfg *hostpoller.Config, path, host, reason string, started time.Time) error {
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	snap, err := (hostUpServerSource{client: client, app: cfg.App}).DesiredState(api.WithoutSession(cmd.Context()), host)
	if err != nil {
		return err
	}
	result := hostup.Result{HostID: host, SweepMode: snap.SweepMode, SetupSource: snap.SetupSource}
	for _, w := range snap.Workers {
		code := w.Reason
		if w.Desired == "RUNNING" && (w.Reason == "LONG_LIVED" || w.Reason == "HOLDS_TASK") {
			code = reason
		}
		result.Skipped = append(result.Skipped, hostup.Item{WorkerID: w.WorkerID, Worker: w.Name, Reason: code})
	}
	id := uuid.NewString()
	record := recordSweepRun(cmd.Context(), f, path, cfg.App, host, id, started, true, result)
	dto := hostUpDTO{AppRef: cfg.App, HostID: host, DryRun: false, Source: "server", SweepMode: nz(snap.SweepMode), SetupSource: nz(snap.SetupSource), SweepRun: record, Planned: []hostUpItemDTO{}, Applied: []hostUpItemDTO{}, Failed: []hostUpItemDTO{}, Skipped: hostUpItems(result.Skipped)}
	if snap.SweepMode != "OFF" {
		dto.Error = &reason
	}
	if err := writeHostUp(f, dto, func() string { return cfg.App + " (from --config)" }); err != nil {
		return err
	}
	// An unavailable observation (OFF included) is a completed sweep, not a crash.
	return nil
}

// sweepHostUpOptions is the timer pass's host-up run: applying, under the lock
// the caller already holds, and exempt from the --apply sweep-mode refusal
// (cli#2652) because its mode comes from the server's desired state and an OFF
// App is a quiet pass the sweep records, not an error.
func sweepHostUpOptions(host, root, orca, configPath string, readyChecked bool, started time.Time) hostUpOptions {
	return hostUpOptions{host: host, root: root, orcaBin: orca, configPath: configPath, apply: true, skipSweepModeGate: true, timeout: 2 * time.Minute, lockHeld: true, readyChecked: readyChecked, startedAt: started}
}
