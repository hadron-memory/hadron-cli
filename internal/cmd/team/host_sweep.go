package team

import (
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/healthsched"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/spf13/cobra"
)

func newCmdTeamHostSweep(f *cmdutil.Factory) *cobra.Command {
	var configPath string
	c := &cobra.Command{Use: "sweep --config <host.yaml>", Short: "Run one host reconcile and record its outcome", Long: `Run one host up pass using the registered host id in the poller's state and
worktrees_root from host YAML. The existing manager credential reconciles;
a separate key_file-only client records S10. The App's sweepMode decides
whether actions are allowed (OFF or OBSERVE acts on nothing). No worker
session is sent. C1 posts no chat message; the server owns summaries/alarms.

A second concurrent pass exits immediately with another sweep holds the lock.
Open-session recovery belongs to the running poller's C5 loop; this command
never starts a second agent directly. The 15-minute health timer and 5-minute
report-stops timer remain explicitly separate: they still handle health and
unreported stop notices. Install creates only the new one-minute sweep timer.
No daemon is started, killed or restarted by this command.`, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
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
		orca := cfg.Delivery.OrcaBin
		if orca == "" {
			orca = "orca"
		}
		return runHostUp(cmd, f, hostUpOptions{host: host, root: cfg.WorktreesRoot, orcaBin: orca, configPath: configPath, apply: true, timeout: 2 * time.Minute})
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
Repeated install is idempotent; another config requires uninstall first.`
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
	status.Long = "Inspect only the installed host sweep timer and its last run. Does not read server history; use host sweep-runs for S10 records."
	c.AddCommand(install, uninstall, status)
	return c
}
