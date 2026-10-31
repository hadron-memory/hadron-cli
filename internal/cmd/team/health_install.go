package team

// `team health install|uninstall|status` (hadron-cli#1374, slice C2b of the
// team health sweep): put the sweep on a schedule on the Orca host without a
// separate app. The entry is exactly
//   <abs hadron> team health sweep --config <abs host.yaml>
// in a systemd user timer, a crontab line or a launchd agent. The host key is
// never read, written or passed on argv: it stays in the 0600 key file the
// sweep reads. Scheduler calls go through healthsched's injected Runner.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/healthsched"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// healthEnv builds the machine seam; tests replace it so nothing execs a real
// scheduler.
var healthEnv = func(f *cmdutil.Factory) healthsched.Env {
	home, _ := os.UserHomeDir()
	return healthsched.Env{
		Home: home, GOOS: goosName(), UID: os.Getuid(),
		Which: exec.LookPath,
		Run: func(argv []string, stdin string) ([]byte, error) {
			c := exec.Command(argv[0], argv[1:]...)
			if stdin != "" {
				c.Stdin = strings.NewReader(stdin)
			}
			return c.CombinedOutput()
		},
	}
}

func goosName() string { return runtime.GOOS }

// healthInstallCmds are the three verbs, registered under `team health`.
func healthInstallCmds(f *cmdutil.Factory) []*cobra.Command {
	return []*cobra.Command{newHealthInstall(f), newHealthUninstall(f), newHealthStatus(f)}
}

// mapSched turns a scheduler-package refusal into exit 2 and anything else into exit 1.
func mapSched(err error) error {
	var ue *healthsched.UsageError
	if errors.As(err, &ue) {
		return exitcode.New(exitcode.Usage, err)
	}
	return exitcode.New(exitcode.Error, err)
}

type installDTO struct {
	Scheduler string   `json:"scheduler"`
	Changed   bool     `json:"changed"`
	Interval  string   `json:"interval"`
	Binary    string   `json:"binary"`
	Config    string   `json:"config"`
	Orca      string   `json:"orca"`
	Files     []string `json:"files"`
}

func newHealthInstall(f *cmdutil.Factory) *cobra.Command {
	var config, interval, bin string
	c := &cobra.Command{
		Use:   "install --config <host.yaml> --interval <duration> [--bin <path>]",
		Short: "Run the team health sweep on a schedule on this host",
		Long: `Install a user-level scheduler entry that runs
"<hadron> team health sweep --config <host.yaml>" every --interval: a systemd
user timer on Linux (a crontab line where systemd user units are not
available) or a launchd agent on macOS. There is no default interval; give
whole minutes, at least 1m (cron needs one that divides an hour or a day
evenly, such as 5m, 15m, 1h or 6h).

The entry carries your current PATH, because schedulers start jobs with a
minimal one, and install refuses (exit 2) when neither delivery.orca_bin in the
host config nor that PATH resolves orca: the sweep could only fail unattended.
The host key is never written into the entry; it stays in the 0600 key file the
sweep reads.

Installing again changes nothing; a new --interval updates the entry. One entry
exists per user: another --config is refused until "team health uninstall".
Exit 2 for a bad flag, path or interval, or when no scheduler is supported
(nothing is written); exit 1 when a scheduler call fails, naming the files
written (uninstall removes them).`,
		Example: "  hadron team health install --config /etc/hadron/host.yaml --interval 5m\n  hadron team health install --config ./host.yaml --interval 1h --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(interval) == "" {
				return exitcode.Newf(exitcode.Usage, "--interval is required (no default): whole minutes, at least 1m, for example --interval 5m")
			}
			d, err := time.ParseDuration(strings.TrimSpace(interval))
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "--interval %q is not a duration like 5m or 1h", interval)
			}
			if config == "" {
				return exitcode.Newf(exitcode.Usage, "--config is required: the host configuration the sweep reads")
			}
			abs, err := filepath.Abs(config)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "--config: %v", err)
			}
			cfg, err := hostpoller.LoadConfig(abs)
			if err != nil {
				// A parse error can quote the file (the key file, if it was
				// passed by mistake): name the path and the cause class only.
				if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
					return exitcode.Newf(exitcode.Usage, "--config %s cannot be read: %v", abs, errors.Unwrap(err))
				}
				return exitcode.Newf(exitcode.Usage, "--config %s is not a valid host configuration; its contents are not echoed (check it with `hadron team host run --config`)", abs)
			}
			if bin != "" && !filepath.IsAbs(bin) {
				return exitcode.Newf(exitcode.Usage, "--bin must be an absolute path, got %q", bin)
			}
			if bin == "" {
				if p, lerr := exec.LookPath("hadron"); lerr == nil {
					bin = p
				} else if p, xerr := os.Executable(); xerr == nil {
					bin = p
				}
			}
			if bin, err = filepath.Abs(bin); err != nil || bin == "" {
				return exitcode.Newf(exitcode.Usage, "could not find hadron: pass --bin <absolute path>")
			}
			// The entry carries the cleaned PATH, so that is what orca must resolve on.
			path, err := healthsched.CleanPath(os.Getenv("PATH"))
			if err != nil {
				return mapSched(err)
			}
			orca, err := healthsched.ResolveOrca(cfg.Delivery.OrcaBin, path)
			if err != nil {
				return mapSched(err)
			}
			spec := healthsched.Spec{Bin: bin, Config: abs, Interval: d, Path: path}
			res, err := healthEnv(f).Install(spec)
			if err != nil {
				return mapSched(err)
			}
			for _, n := range res.Notes {
				fmt.Fprintln(f.IOStreams.ErrOut, "note:", n)
			}
			dto := installDTO{Scheduler: string(res.Kind), Changed: res.Changed, Interval: d.String(), Binary: bin, Config: abs, Orca: orca, Files: append([]string{}, res.Files...)}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				verb := "installed"
				if !res.Changed {
					verb = "already installed, unchanged"
				}
				_, e := fmt.Fprintf(w, "%s (%s): every %s, runs %s team health sweep --config %s, orca %s\n", verb, dto.Scheduler, dto.Interval, bin, abs, orca)
				return e
			})
		},
	}
	c.Flags().StringVar(&config, "config", "", "host configuration the sweep reads (required)")
	c.Flags().StringVar(&interval, "interval", "", "how often to run, whole minutes at least 1m, e.g. 5m (required, no default)")
	c.Flags().StringVar(&bin, "bin", "", "absolute path of hadron to run (default: the one on PATH)")
	return c
}

type uninstallDTO struct {
	Removed   bool     `json:"removed"`
	Scheduler string   `json:"scheduler"`
	Files     []string `json:"files"`
}

func newHealthUninstall(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the scheduled team health sweep from this host",
		Long: `Remove exactly what "team health install" wrote (the timer and service, the
crontab line, or the launchd agent), including after an install that failed
midway. Other crontab lines are left alone. Nothing installed is success.`,
		Example: "  hadron team health uninstall",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := healthEnv(f).Uninstall()
			if err != nil {
				return mapSched(err)
			}
			dto := uninstallDTO{Removed: res.Changed, Scheduler: string(res.Kind), Files: append([]string{}, res.Files...)}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if !res.Changed {
					_, e := fmt.Fprintln(w, "nothing installed")
					return e
				}
				_, e := fmt.Fprintf(w, "uninstalled (%s)\n", dto.Scheduler)
				return e
			})
		},
	}
}

type lastRunDTO struct {
	At       *string `json:"at"`
	ExitCode *int    `json:"exitCode"`
}

type healthStatusDTO struct {
	Installed bool        `json:"installed"`
	Scheduler *string     `json:"scheduler"`
	Active    bool        `json:"active"`
	Interval  *string     `json:"interval"`
	Binary    *string     `json:"binary"`
	Config    *string     `json:"config"`
	Orca      *string     `json:"orca"`
	Files     []string    `json:"files"`
	LastRun   *lastRunDTO `json:"lastRun"`
	Notes     []string    `json:"notes"`
}

func nz(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func newHealthStatus(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the team health sweep is scheduled on this host",
		Long: `Show whether the sweep is installed, which scheduler holds it, the interval,
the hadron it runs, the orca it resolves, and the last run's time and exit code
as the scheduler recorded them (systemd, launchd). Under cron the last run is
"unknown": cron does not record it, and nothing is guessed. A partial install
(files present, scheduler not running them) shows as installed but not active.
Read-only; exit 0 whether or not anything is installed.

Details:
  --json: installed, scheduler, active, interval, binary, config, orca, files,
  lastRun {at, exitCode} (null fields when unknown, null when nothing is
  installed) and notes.`,
		Example: "  hadron team health status\n  hadron team health status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e := healthEnv(f)
			st := e.ReadStatus()
			dto := healthStatusDTO{Installed: st.Installed, Active: st.Active, Files: append([]string{}, st.Files...), Notes: append([]string{}, st.Notes...)}
			if st.Installed {
				dto.Scheduler, dto.Interval, dto.Binary, dto.Config = nz(st.Kind), nz(st.Interval), nz(st.Bin), nz(st.Config)
				dto.Orca = nz(st.Orca)
				if cfg, err := hostpoller.LoadConfig(st.Config); err == nil && cfg.Delivery.OrcaBin != "" {
					dto.Orca = nz(cfg.Delivery.OrcaBin)
				}
				lr := &lastRunDTO{}
				if st.LastRun != nil {
					if st.LastRun.At != nil {
						s := st.LastRun.At.UTC().Format(time.RFC3339)
						lr.At = &s
					}
					lr.ExitCode = st.LastRun.ExitCode
				}
				dto.LastRun = lr
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if !dto.Installed {
					_, err := fmt.Fprintln(w, "not installed")
					return err
				}
				val := func(s *string) string {
					if s == nil {
						return "unknown"
					}
					return *s
				}
				last := "unknown"
				if dto.LastRun != nil && dto.LastRun.At != nil {
					last = *dto.LastRun.At
					if dto.LastRun.ExitCode != nil {
						last += fmt.Sprintf(" (exit %d)", *dto.LastRun.ExitCode)
					}
				} else if dto.LastRun != nil && dto.LastRun.ExitCode != nil {
					last = fmt.Sprintf("exit %d", *dto.LastRun.ExitCode)
				}
				active := "yes"
				if !dto.Active {
					active = "NO"
				}
				for _, kv := range [][2]string{{"installed", "yes"}, {"scheduler", val(dto.Scheduler)}, {"active", active}, {"interval", val(dto.Interval)}, {"binary", val(dto.Binary)}, {"config", val(dto.Config)}, {"orca", val(dto.Orca)}, {"last run", last}} {
					if _, err := fmt.Fprintf(w, "%-10s %s\n", kv[0]+":", output.SafeCell(kv[1])); err != nil {
						return err
					}
				}
				for _, n := range dto.Notes {
					if _, err := fmt.Fprintf(w, "note: %s\n", n); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}
