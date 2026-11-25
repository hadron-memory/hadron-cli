package team

// `team host settings-check` — is Claude Code's idleCompaction off for every
// worker this host runs (cli#2545, ruled in cli#2544)? Local and read-only:
// no server, no key, no write, and the CLI never edits a settings file.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/settingscheck"
)

type settingsAccountDTO struct {
	Dir    string `json:"dir"`
	File   string `json:"file"`
	Status string `json:"status"`
}

type settingsWorktreeDTO struct {
	Worker   string `json:"worker"`
	Worktree string `json:"worktree"`
	Path     string `json:"path"`
	Status   string `json:"status"`
	// Effective is null when the value is not known; Source is null when no
	// file sets the key.
	Effective *bool    `json:"effective"`
	Source    *string  `json:"source"`
	Reasons   []string `json:"reasons"`
}

type settingsCheckDTO struct {
	Key           string                `json:"key"`
	RequiredValue bool                  `json:"requiredValue"`
	Default       bool                  `json:"default"`
	MinVersion    string                `json:"minVersion"`
	ClaudeBin     string                `json:"claudeBin"`
	ClaudeVersion *string               `json:"claudeVersion"`
	Drift         bool                  `json:"drift"`
	Accounts      []settingsAccountDTO  `json:"accounts"`
	Worktrees     []settingsWorktreeDTO `json:"worktrees"`
}

func newCmdTeamHostSettingsCheck(f *cmdutil.Factory) *cobra.Command {
	var configPath, managedDir, claudeBin string
	cmd := &cobra.Command{
		Use:   "settings-check --config <host.yaml>",
		Short: "Check that idle compaction is off for this host's workers",
		Example: `  hadron team host settings-check --config /etc/hadron/host.yaml
  hadron team host settings-check --config host.yaml --json`,
		Long: `Report, per worker worktree of this host, whether Claude Code's
"idleCompaction" setting is off, and which file decides it. The setting is a
Boolean that is on (true) by default; for team workers it must be false. It
needs Claude Code 2.1.290 or later: an older version ignores it.

Run it on the host, as the user the workers run as. It reads local files only:
no server, no sign-in, no host key. It changes nothing, starts nothing, and
shows nothing of a settings file but this one key. Setting the value is the
host manager's own step, by hand; no hadron command writes these files.

WHICH FILE WINS, highest first:
  1. managed settings: managed-settings.json, then managed-settings.d/*.json
     in name order, a later file replacing the value
  2. .claude/settings.local.json at the repository root; for a linked Git
     worktree that is the MAIN checkout, so one file there decides for all
     of its worktrees
  3. .claude/settings.local.json in the worktree
  4. .claude/settings.json in the worktree
  5. settings.json in the account directory
Levels 2 to 5 were measured in a linked worktree on Claude Code 2.1.328 and
2.1.332. Level 1, a worktree inside a repository subdirectory, and skipping a
root another user owns follow its documentation.

Account directories are ~/.claude and every ~/.claude* directory. A worker with
claude_config_dir in host.yaml is judged by that one. For any other worker the
launcher picks the account, which this command cannot see, so it is off only
when it is off whichever account is used.

REASONS for drift (a worktree may have several):
  UNSET             no file that applies sets the key, so the default (on)
                    applies; with ACCOUNT_UNKNOWN, for one of the accounts
  ON                the deciding file sets true
  MANAGED_ON        managed settings set true; no other file can change that
  INVALID           the deciding file is not valid JSON, or the key in it is
                    not true or false
  UNREADABLE        the deciding file or directory, or the worktree's .git,
                    could not be read
  ACCOUNT_UNKNOWN   the account is not known and the accounts disagree
  VERSION_TOO_OLD   the installed Claude Code is older than 2.1.290
  VERSION_UNKNOWN   --claude-bin is not a link to a versions/<version> file
  WORKTREE_MISSING  the worktree directory is not there
  NO_WORKTREE       the worker has no worktree, so nothing could be checked
GLM and Kimi workers run Claude Code against another model, so they are checked
like Claude workers. A worker whose terminal runs Codex is NOT_APPLICABLE.

It checks this key only. A worker whose idle compaction is off another way
(autoCompactEnabled false, DISABLE_AUTO_COMPACT) is still drift, and it cannot
see a --settings argument, server-managed or MDM settings, or the version of
an agent that is already running.

Exit codes: 0 no drift; 5 drift; 2 --config missing or unreadable, or
--managed-dir empty; 1 the host state file or the home directory is unreadable. With 5 the full report, --json
included, is still printed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if configPath == "" {
				return exitcode.Newf(exitcode.Usage, "--config is required")
			}
			if managedDir == "" {
				return exitcode.Newf(exitcode.Usage, "--managed-dir is empty; leave it out for %s", settingscheck.ManagedDir)
			}
			cfg, err := hostpoller.LoadConfig(configPath)
			if err != nil {
				return exitcode.New(exitcode.Usage, err)
			}
			state, err := hostpoller.LoadState(cfg.StateFile)
			if err != nil {
				return exitcode.Newf(exitcode.Error, "cannot read host state")
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return exitcode.Newf(exitcode.Error, "cannot find the home directory")
			}
			if claudeBin == "" {
				claudeBin = filepath.Join(home, ".local", "bin", "claude")
			}
			accounts, err := settingscheck.AccountDirs(home)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return exitcode.Newf(exitcode.Error, "cannot read the home directory")
			}
			if len(accounts) == 0 {
				accounts = []string{filepath.Join(home, ".claude")}
			}
			dto := settingsCheckDTO{
				Key: settingscheck.Key, RequiredValue: false, Default: settingscheck.Default,
				MinVersion: settingscheck.MinVersion, ClaudeBin: claudeBin,
				Accounts: []settingsAccountDTO{}, Worktrees: []settingsWorktreeDTO{},
			}
			version := ""
			if v := settingscheck.InstalledVersion(claudeBin); v == "" {
				version = "VERSION_UNKNOWN"
			} else {
				dto.ClaudeVersion = &v
				if !settingscheck.AtLeast(v, settingscheck.MinVersion) {
					version = "VERSION_TOO_OLD"
				}
			}
			for _, d := range accounts {
				file := filepath.Join(d, "settings.json")
				dto.Accounts = append(dto.Accounts, settingsAccountDTO{Dir: d, File: file, Status: settingscheck.ReadFile(file)})
			}
			created, _ := state.Snapshot()
			for _, ref := range cfg.WorkerRefs() {
				w := cfg.Workers[ref]
				dirs := accounts
				if w.ClaudeConfigDir != "" {
					dirs = []string{w.ClaudeConfigDir}
				}
				trees := append(append([]string{}, w.Worktrees...), created[ref]...)
				sort.Strings(trees)
				trees = dedupe(trees)
				if len(trees) == 0 {
					trees = []string{""} // one row says the worker was not checked
				}
				for _, tree := range trees {
					row := settingsWorktreeDTO{Worker: ref, Worktree: tree, Reasons: []string{}}
					if tree != "" {
						row.Path = cfg.WorktreePath(tree)
					}
					checkWorktreeSettings(&row, w, managedDir, home, version, dirs)
					dto.Drift = dto.Drift || row.Status == "DRIFT"
					dto.Worktrees = append(dto.Worktrees, row)
				}
			}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return writeSettingsCheck(w, dto) }); err != nil {
				return err
			}
			if dto.Drift {
				return exitcode.Newf(exitcode.Conflict, "%s is not off for every worker of this host", settingscheck.Key)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "host configuration (required)")
	cmd.Flags().StringVar(&managedDir, "managed-dir", settingscheck.ManagedDir, "directory of Claude Code's managed settings")
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Claude Code launcher link the version is read from (default ~/.local/bin/claude)")
	return cmd
}

// checkWorktreeSettings fills one row's verdict.
func checkWorktreeSettings(row *settingsWorktreeDTO, w hostpoller.WorkerConfig, managedDir, home, version string, accounts []string) {
	if !w.RunsClaudeCode() {
		row.Status = "NOT_APPLICABLE"
		return
	}
	row.Status = "DRIFT"
	if row.Worktree == "" {
		row.Reasons = append(row.Reasons, "NO_WORKTREE")
		return
	}
	if st, err := os.Stat(row.Path); err != nil || !st.IsDir() {
		row.Reasons = append(row.Reasons, "WORKTREE_MISSING")
		return
	}
	// The first account that is not off speaks for the row: with the account
	// unknown, off is claimed only when every one is.
	var res settingscheck.Result
	first, differ := settingscheck.Result{}, false
	for i, dir := range accounts {
		r := settingscheck.Effective(managedDir, row.Path, dir, home)
		if i == 0 {
			first, res = r, r
		}
		differ = differ || r.State != first.State
		if res.Off() && !r.Off() {
			res = r
		}
	}
	if res.Source != "" {
		row.Source = &res.Source
	}
	on := true
	off := false
	switch res.State {
	case settingscheck.StateOff:
		row.Effective = &off
	case settingscheck.StateOn:
		row.Effective = &on
		if res.Managed {
			row.Reasons = append(row.Reasons, "MANAGED_ON")
		} else {
			row.Reasons = append(row.Reasons, "ON")
		}
	case settingscheck.StateUnset:
		def := settingscheck.Default
		row.Effective = &def
		row.Reasons = append(row.Reasons, "UNSET")
	default:
		row.Reasons = append(row.Reasons, res.State)
	}
	if differ {
		row.Reasons = append(row.Reasons, "ACCOUNT_UNKNOWN")
	}
	if version != "" {
		row.Reasons = append(row.Reasons, version)
	}
	if len(row.Reasons) == 0 {
		row.Status = "OFF"
	}
}

func writeSettingsCheck(w io.Writer, dto settingsCheckDTO) error {
	v := "unknown"
	if dto.ClaudeVersion != nil {
		v = *dto.ClaudeVersion
	}
	fmt.Fprintf(w, "%s must be false (default %t); Claude Code %s at %s, needs %s or later\n", dto.Key, dto.Default, v, output.SafeCell(dto.ClaudeBin), dto.MinVersion)
	t := output.NewTable(w, "ACCOUNT FILE", "VALUE")
	for _, a := range dto.Accounts {
		t.Row(output.SafeCell(a.File), a.Status)
	}
	if err := t.Flush(); err != nil {
		return err
	}
	t = output.NewTable(w, "WORKER", "WORKTREE", "STATUS", "EFFECTIVE", "REASONS", "SOURCE")
	for _, r := range dto.Worktrees {
		eff, src := "unknown", "-"
		if r.Effective != nil {
			eff = fmt.Sprint(*r.Effective)
		} else if r.Status == "NOT_APPLICABLE" {
			eff = "-"
		}
		if r.Source != nil {
			src = *r.Source
		}
		reasons := "-"
		for i, s := range r.Reasons {
			if i == 0 {
				reasons = s
			} else {
				reasons += "," + s
			}
		}
		t.Row(output.SafeCell(r.Worker), output.SafeCell(orDash(r.Worktree)), r.Status, eff, reasons, output.SafeCell(src))
	}
	return t.Flush()
}
