package team

// `team usage collect|show|install|uninstall|status` (hadron-cli#1472): record
// each model subscription account's 5-hour and 7-day allowance windows over
// time as Hadron objects (collection account-usage), and show which account
// runs out first. Not `hadron usage`, which reads server-recorded AI-call
// events. The CLI reads two files per account (the status line's snapshot and
// an operator-produced ccusage JSON), runs no subprocess, and writes nothing
// outside the paths it was given.

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Khan/genqlient/graphql"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/accountusage"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/healthsched"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// usageNow is the clock; tests replace it.
var usageNow = time.Now

// usageEnv is the scheduler seam for the usage job; tests replace it so
// nothing execs a real scheduler.
var usageEnv = func(f *cmdutil.Factory) healthsched.Env {
	e := healthEnv(f)
	e.Job = healthsched.Usage
	return e
}

func newCmdTeamUsage(f *cmdutil.Factory) *cobra.Command {
	group := &cobra.Command{
		Use:   "usage <command>",
		Short: "Record and show each model account's 5h/7d allowance use",
		Long: `Track how much of each model subscription account's allowance is used. These
are the rolling 5-hour and 7-day windows that every session on an account
shares, read on the worker host: "collect" records one snapshot per account as a
Hadron object (collection account-usage), "show" tells you which account runs
out first, and "install" runs collect on a schedule.

This is not "hadron usage" (AI-call events the server recorded for your
memories); team usage never reads or writes those.

Setup, per account, by the operator:
 1. A status line script that writes the harness's rate_limits object, so the
    CLI can read it. The CLI never edits a harness settings.json. Put this in
    the account's status line script (CLAUDE_CONFIG_DIR is set for the
    session):
      in=$(cat); d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/hadron-usage"
      mkdir -p "$d"; printf '%s' "$in" | jq -c '{observedAt: now|floor} + (.rate_limits // {} | {five_hour, seven_day} | with_entries(select(.value != null)))' > "$d/rate-limits.json.tmp" && mv "$d/rate-limits.json.tmp" "$d/rate-limits.json"
    The file is {"observedAt":N,"five_hour":{"used_percentage":N,"resets_at":N}?,
    "seven_day":{...}?} (Unix seconds; a window the harness leaves out stays out).
 2. Optionally a ccusage file, produced by you (the CLI never runs ccusage):
      CLAUDE_CONFIG_DIR=<dir> npx ccusage@<pinned version> blocks --json --offline > <file>
 3. A host YAML (--config): memory (where records go), optional host, and one
    entry per account: label (your own name, never an email; an @ is refused),
    configDir (absolute), family (claude), optional ccusageFile, optional
    workers (names that run on the account; absent means "not configured").

Record shape (schemaVersion 1, collection account-usage; absent values are left
out, never 0): label, family, host, observedAt (RFC 3339); fiveHourPct,
fiveHourResetsAt, sevenDayPct, sevenDayResetsAt; tokensSinceLast,
estCostUsdSinceLast (ccusage blocks started after this account's previous
record); workers, workersConfigured, schemaVersion.`,
		Example: "  hadron team usage collect --config ./usage.yaml\n  hadron team usage show --config ./usage.yaml --json\n  hadron team usage install --config ./usage.yaml --interval 15m",
	}
	group.AddCommand(newUsageCollect(f), newUsageShow(f), newUsageInstall(f), newUsageUninstall(f), newUsageStatus(f))
	return group
}

// usageMemory resolves where records live: -m wins, else the config's memory.
func usageMemory(flag string, cfgMemory string) (string, error) {
	m := strings.TrimSpace(flag)
	if m == "" {
		m = strings.TrimSpace(cfgMemory)
	}
	if m == "" {
		return "", exitcode.Newf(exitcode.Usage, "no memory to record in: give -m/--memory or set memory: in the config")
	}
	return cmdutil.CanonicalMemoryRef(m), nil
}

// mapUsageInput maps a refused input file to exit 2 and anything else to 1.
func mapUsageInput(err error) error {
	var ve *accountusage.ValidationError
	if errors.As(err, &ve) {
		return exitcode.New(exitcode.Usage, err)
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return exitcode.Newf(exitcode.Usage, "%v", err)
	}
	return exitcode.New(exitcode.Error, err)
}

type collectedDTO struct {
	Label      string              `json:"label"`
	ObjectID   string              `json:"objectId"`
	Record     accountusage.Record `json:"record"`
	HasWindows bool                `json:"hasWindows"`
}

type collectDTO struct {
	Memory  string         `json:"memory"`
	Records []collectedDTO `json:"records"`
}

func newUsageCollect(f *cmdutil.Factory) *cobra.Command {
	var config, memory string
	c := &cobra.Command{
		Use:   "collect --config <usage.yaml> [-m <memory>]",
		Short: "Record one allowance snapshot per configured account",
		Long: `Read each configured account's rate-limits snapshot (and its ccusage file, when
configured) and record one account-usage object per account. Every account is
validated before anything is written: any problem exits 2 and records nothing
for any account. That covers a missing or malformed config, a malformed or
unreadable snapshot or ccusage file, a used_percentage outside 0-100, and a
label containing @. A snapshot that does not exist yet means no data: that
account is recorded with its windows absent.

Reads only <configDir>/hadron-usage/rate-limits.json and the named ccusage
file (a symlink is refused); runs no subprocess; writes only to the server.
Nothing from a file is echoed in an error.

Exit 0 after recording, 2 for refused input, otherwise as any server write.`,
		Example: "  hadron team usage collect --config ./usage.yaml\n  hadron team usage collect --config ./usage.yaml -m hrn:mem:acme.com:team --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if config == "" {
				return exitcode.Newf(exitcode.Usage, "--config is required")
			}
			cfg, err := accountusage.LoadConfig(config)
			if err != nil {
				return mapUsageInput(err)
			}
			prepared, err := accountusage.Prepare(cfg)
			if err != nil {
				return mapUsageInput(err)
			}
			mem, err := usageMemory(memory, cfg.Memory)
			if err != nil {
				return err
			}
			host := cfg.Host
			if host == "" {
				host, _ = os.Hostname()
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			now := usageNow().UTC()
			dto := collectDTO{Memory: mem, Records: []collectedDTO{}}
			for _, p := range prepared {
				var since time.Time
				if p.Blocks != nil {
					since, err = previousObservedAt(cmd, client, mem, p.Account.Label, host)
					if err != nil {
						return err
					}
				}
				rec := accountusage.BuildRecord(p, host, now, since)
				fields, _ := json.Marshal(rec)
				label := p.Account.Label
				resp, err := gen.CreateObject(cmd.Context(), client, mem, accountusage.Collection, fields, nil, &label)
				if err != nil {
					return api.MapError(err)
				}
				id := objectID(resp.CreateObject)
				dto.Records = append(dto.Records, collectedDTO{Label: label, ObjectID: id, Record: rec, HasWindows: rec.FiveHourPct != nil || rec.SevenDayPct != nil})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w, "ACCOUNT", "5H", "7D", "OBSERVED")
				for _, r := range dto.Records {
					t.Row(output.SafeCell(r.Label), pctCell(r.Record.FiveHourPct), pctCell(r.Record.SevenDayPct), r.Record.ObservedAt)
				}
				return t.Flush()
			})
		},
	}
	c.Flags().StringVar(&config, "config", "", "host usage YAML (required)")
	c.Flags().StringVarP(&memory, "memory", "m", "", "memory to record in (default: memory: in the config)")
	return c
}

func objectID(raw json.RawMessage) string {
	var o struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &o)
	return o.ID
}

func pctCell(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", *p)
}

// previousObservedAt is this account's newest recorded observedAt on this
// host, zero when there is none (the first record has no "since last").
func previousObservedAt(cmd *cobra.Command, client graphql.Client, mem, label, host string) (time.Time, error) {
	mb, _ := json.Marshal(map[string]string{"label": label, "host": host})
	sb, _ := json.Marshal(map[string]string{"observedAt": "desc"})
	match, sort := json.RawMessage(mb), json.RawMessage(sb)
	one := 1
	resp, err := gen.FindObjects(cmd.Context(), client, mem, accountusage.Collection, &match, nil, &sort, &one, nil)
	if err != nil {
		return time.Time{}, api.MapError(err)
	}
	for _, o := range resp.FindObjects.Objects {
		var r struct {
			ObservedAt string `json:"observedAt"`
		}
		if json.Unmarshal(o, &r) == nil {
			if t, perr := time.Parse(time.RFC3339, r.ObservedAt); perr == nil {
				return t, nil
			}
		}
	}
	return time.Time{}, nil
}

type showDTO struct {
	Memory   string                     `json:"memory"`
	Accounts []accountusage.AccountView `json:"accounts"`
}

func newUsageShow(f *cmdutil.Factory) *cobra.Command {
	var config, memory string
	var limit int
	c := &cobra.Command{
		Use:   "show [--config <usage.yaml> | -m <memory>]",
		Short: "Show each account's current 5h/7d use, resets and burn rate",
		Long: `Read the recorded account-usage objects and show, per account, the current 5-hour
and 7-day use, when each resets, the recent burn rate (percent per hour across
the points recorded in the current window) and, when the rate is positive, the
time the window would reach 100% and whether that is before it resets. An
account that runs out first is the earliest exhaustsAt before its reset.

Unknown is null, never 0. A window whose reset time has passed is shown with
expired true: its percentage describes a window that is over. Reads at most
--limit newest records (default and maximum 200) across all accounts; records
that do not parse are skipped.

--json: {memory, accounts:[{label, family, host, observedAt, fiveHour, sevenDay,
workers, workersConfigured, points}]}, each window {usedPct, resetsAt, expired,
burnPctPerHour, exhaustsAt, exhaustsBeforeReset}; accounts is [] when nothing
is recorded.`,
		Example: "  hadron team usage show --config ./usage.yaml\n  hadron team usage show -m hrn:mem:acme.com:team --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 || limit > 200 {
				return exitcode.Newf(exitcode.Usage, "--limit must be 1 to 200")
			}
			var cfgMemory string
			if config != "" {
				cfg, err := accountusage.LoadConfig(config)
				if err != nil {
					return mapUsageInput(err)
				}
				cfgMemory = cfg.Memory
			}
			mem, err := usageMemory(memory, cfgMemory)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			sb, _ := json.Marshal(map[string]string{"observedAt": "desc"})
			sort := json.RawMessage(sb)
			resp, err := gen.FindObjects(cmd.Context(), client, mem, accountusage.Collection, nil, nil, &sort, &limit, nil)
			if err != nil {
				return api.MapError(err)
			}
			dto := showDTO{Memory: mem, Accounts: accountusage.View(resp.FindObjects.Objects, usageNow().UTC())}
			if dto.Accounts == nil {
				dto.Accounts = []accountusage.AccountView{}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w, "ACCOUNT", "5H", "5H RESETS", "5H %/H", "7D", "7D RESETS", "7D %/H", "RUNS OUT")
				for _, a := range dto.Accounts {
					t.Row(output.SafeCell(a.Label), winPct(a.FiveHour), strDash(a.FiveHour.ResetsAt), rateCell(a.FiveHour), winPct(a.SevenDay), strDash(a.SevenDay.ResetsAt), rateCell(a.SevenDay), runsOut(a))
				}
				return t.Flush()
			})
		},
	}
	c.Flags().StringVar(&config, "config", "", "host usage YAML (only to take its memory)")
	c.Flags().StringVarP(&memory, "memory", "m", "", "memory the records are in (default: memory: in --config)")
	c.Flags().IntVar(&limit, "limit", 200, "newest records to read, 1 to 200")
	return c
}

func strDash(s *string) string { return output.Dash(s) }

func winPct(w accountusage.WindowView) string {
	if w.UsedPct == nil {
		return "—"
	}
	s := fmt.Sprintf("%.0f%%", *w.UsedPct)
	if w.Expired {
		s += " (expired)"
	}
	return s
}

func rateCell(w accountusage.WindowView) string {
	if w.BurnPctPerHour == nil {
		return "—"
	}
	return fmt.Sprintf("%.1f", *w.BurnPctPerHour)
}

func runsOut(a accountusage.AccountView) string {
	for _, w := range []accountusage.WindowView{a.FiveHour, a.SevenDay} {
		if w.ExhaustsAt != nil && w.ExhaustsBeforeReset != nil && *w.ExhaustsBeforeReset {
			return *w.ExhaustsAt
		}
	}
	return "—"
}

// ---- schedule: mirrors `team health install|uninstall|status` ----

type usageInstallDTO struct {
	Scheduler string   `json:"scheduler"`
	Changed   bool     `json:"changed"`
	Interval  string   `json:"interval"`
	Binary    string   `json:"binary"`
	Config    string   `json:"config"`
	Files     []string `json:"files"`
}

func newUsageInstall(f *cmdutil.Factory) *cobra.Command {
	var config, interval, bin string
	c := &cobra.Command{
		Use:   "install --config <usage.yaml> --interval <duration> [--bin <path>]",
		Short: "Run team usage collect on a schedule on this host",
		Long: `Install a user-level scheduler entry that runs
"<hadron> team usage collect --config <usage.yaml>" every --interval: a systemd
user timer on Linux (a crontab line where systemd user units are not available)
or a launchd agent on macOS, the same mechanism as "team health install", as a
separate entry. There is no default interval; give whole minutes, at least 1m
(cron needs one that divides an hour or a day evenly, such as 5m, 15m or 1h).

The config is validated first (exit 2 and nothing written if it is not valid).
The scheduled run uses this user's own hadron login; no token is written into
the entry. Installing again changes nothing; a new --interval updates it;
another --config is refused until "team usage uninstall". Exit 1 when a
scheduler call fails, naming the files written.`,
		Example: "  hadron team usage install --config /etc/hadron/usage.yaml --interval 15m",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(interval) == "" {
				return exitcode.Newf(exitcode.Usage, "--interval is required (no default): whole minutes, at least 1m, for example --interval 15m")
			}
			d, err := time.ParseDuration(strings.TrimSpace(interval))
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "--interval %q is not a duration like 15m or 1h", interval)
			}
			if config == "" {
				return exitcode.Newf(exitcode.Usage, "--config is required")
			}
			abs, err := filepath.Abs(config)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "--config: %v", err)
			}
			if _, err := accountusage.LoadConfig(abs); err != nil {
				return mapUsageInput(err)
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
			path, err := healthsched.CleanPath(os.Getenv("PATH"))
			if err != nil {
				return mapSched(err)
			}
			res, err := usageEnv(f).Install(healthsched.Spec{Bin: bin, Config: abs, Interval: d, Path: path})
			if err != nil {
				return mapSched(err)
			}
			for _, n := range res.Notes {
				fmt.Fprintln(f.IOStreams.ErrOut, "note:", n)
			}
			dto := usageInstallDTO{Scheduler: string(res.Kind), Changed: res.Changed, Interval: d.String(), Binary: bin, Config: abs, Files: append([]string{}, res.Files...)}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				verb := "installed"
				if !res.Changed {
					verb = "already installed, unchanged"
				}
				_, e := fmt.Fprintf(w, "%s (%s): every %s, runs %s team usage collect --config %s\n", verb, dto.Scheduler, dto.Interval, bin, abs)
				return e
			})
		},
	}
	c.Flags().StringVar(&config, "config", "", "host usage YAML collect reads (required)")
	c.Flags().StringVar(&interval, "interval", "", "how often to run, whole minutes at least 1m, e.g. 15m (required, no default)")
	c.Flags().StringVar(&bin, "bin", "", "absolute path of hadron to run (default: the one on PATH)")
	return c
}

func newUsageUninstall(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the scheduled team usage collection from this host",
		Long: `Remove exactly what "team usage install" wrote (the timer and service, the
crontab line, or the launchd agent), including after an install that failed
midway. Other crontab lines, and the team health sweep's entry, are left alone.
Nothing installed is success.`,
		Example: "  hadron team usage uninstall",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := usageEnv(f).Uninstall()
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

type usageStatusDTO struct {
	Installed bool        `json:"installed"`
	Scheduler *string     `json:"scheduler"`
	Active    bool        `json:"active"`
	Interval  *string     `json:"interval"`
	Binary    *string     `json:"binary"`
	Config    *string     `json:"config"`
	Files     []string    `json:"files"`
	LastRun   *lastRunDTO `json:"lastRun"`
	Notes     []string    `json:"notes"`
}

func newUsageStatus(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether team usage collection is scheduled on this host",
		Long: `Show whether the collector is installed, which scheduler holds it, the interval,
the hadron it runs and the last run's time and exit code as the scheduler
recorded them (systemd, launchd; under cron the last run is unknown and nothing
is guessed). Read-only; exit 0 whether or not anything is installed.

--json: installed, scheduler, active, interval, binary, config, files, lastRun
{at, exitCode} (null when unknown or not installed) and notes.`,
		Example: "  hadron team usage status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st := usageEnv(f).ReadStatus()
			dto := usageStatusDTO{Installed: st.Installed, Active: st.Active, Files: append([]string{}, st.Files...), Notes: append([]string{}, st.Notes...)}
			if st.Installed {
				dto.Scheduler, dto.Interval, dto.Binary, dto.Config = nz(st.Kind), nz(st.Interval), nz(st.Bin), nz(st.Config)
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
				active := "yes"
				if !dto.Active {
					active = "NO"
				}
				for _, kv := range [][2]string{{"installed", "yes"}, {"scheduler", val(dto.Scheduler)}, {"active", active}, {"interval", val(dto.Interval)}, {"binary", val(dto.Binary)}, {"config", val(dto.Config)}} {
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
