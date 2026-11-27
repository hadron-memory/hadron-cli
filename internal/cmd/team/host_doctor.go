package team

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostfacts"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// `hadron team host doctor` (hadron-cli#3236, host-file C2 of hadron-server#5209): what this machine has, what
// its accounts file names, and with --host whether it can run every worker assigned to it. Every fact is detected
// now (internal/hostfacts); nothing is read from a host file, and nothing about an account leaves the machine.

// hostDoctorEnv is what the command reads, injected so tests need no Orca and no file system.
type hostDoctorEnv struct {
	Facts        *hostfacts.Facts
	AccountsPath func() (string, error)
	IsDir        func(path string) bool
	// Setup is the one existing assignments read; nil without --host.
	Setup func(hostID string) (hostSetupDTO, error)
}

var newHostDoctorEnv = func(cmd *cobra.Command, f *cmdutil.Factory) hostDoctorEnv {
	return hostDoctorEnv{
		Facts:        hostfacts.New(nil),
		AccountsPath: hostfacts.AccountsPath,
		IsDir: func(p string) bool {
			st, err := os.Stat(p)
			return err == nil && st.IsDir()
		},
		Setup: func(hostID string) (hostSetupDTO, error) {
			d, _, err := fetchHostSetup(cmd, f, hostID)
			return d, err
		},
	}
}

const (
	codeAccountProgram   = "ACCOUNT_PROGRAM_NOT_FOUND"
	codeAccountConfigDir = "ACCOUNT_CONFIGDIR_NOT_FOUND"
	notPlatformSetup     = "App does not take its setup from the platform: its workers come from the host file, so doctor checks no assignments."
	defaultAccountsNote  = "no account named: claude, glm and kimi use ~/.claude; codex uses Orca's active managed Codex account"
)

type doctorProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

type doctorOrca struct {
	Bin     *string `json:"bin"`
	Version *string `json:"version"`
	Status  string  `json:"status"` // FOUND, MISSING (not on this machine) or UNREACHABLE (did not answer)
	Reason  *string `json:"reason"`
	Fix     *string `json:"fix"`
}

type doctorFact struct {
	Fact   string  `json:"fact"`
	Status string  `json:"status"` // FOUND, MISSING or UNKNOWN (Orca could not be asked)
	Value  *string `json:"value"`
	Reason *string `json:"reason"`
	Fix    *string `json:"fix"`
	// Needed is false for a missing program no assigned worker uses: informational, never exit 5.
	Needed bool `json:"needed"`
}

type doctorAccountsFile struct {
	Path    string  `json:"path"`
	Present bool    `json:"present"`
	Error   *string `json:"error"`
}

type doctorAccount struct {
	Name           string          `json:"name"`
	Family         string          `json:"family"`
	Tool           string          `json:"tool"`
	Program        *string         `json:"program"`
	ProgramFound   bool            `json:"programFound"`
	ConfigDir      *string         `json:"configDir"`
	ConfigDirFound *bool           `json:"configDirFound"`
	Problems       []doctorProblem `json:"problems"`
}

type doctorSetup struct {
	HostID  *string `json:"hostId"`
	Source  *string `json:"source"`
	Profile *string `json:"profile"`
	Read    string  `json:"read"` // OK or FAILED
	Error   *string `json:"error"`
	Note    *string `json:"note"`
}

type doctorWorker struct {
	Worker        string          `json:"worker"`
	Workdir       string          `json:"workdir"`
	Family        string          `json:"family"`
	WorkdirPath   *string         `json:"workdirPath"`
	Account       *string         `json:"account"`
	AccountStatus string          `json:"accountStatus"` // default, ok, unknown or missing
	Program       *string         `json:"program"`
	Status        string          `json:"status"` // OK or MISSING
	Problems      []doctorProblem `json:"problems"`
}

type doctorDTO struct {
	Orca         doctorOrca         `json:"orca"`
	Facts        []doctorFact       `json:"facts"`
	AccountsFile doctorAccountsFile `json:"accountsFile"`
	Accounts     []doctorAccount    `json:"accounts"`
	Setup        *doctorSetup       `json:"setup"`
	Workers      []doctorWorker     `json:"workers"`
	OK           bool               `json:"ok"`
}

func doctorStr(s string) *string { return &s }

func missingFields(m *hostfacts.Missing) (reason, fix *string) {
	return doctorStr(m.Reason), doctorStr(m.Fix)
}

// buildHostDoctor runs every check. It returns the report, the exit code (0, 5 or 10; 10 dominates) and the
// error of a failed assignments read, which the caller reports after printing the local sections.
func buildHostDoctor(ctx context.Context, env hostDoctorEnv, hostID string, withHost bool) (doctorDTO, int, error) {
	d := doctorDTO{Facts: []doctorFact{}, Accounts: []doctorAccount{}, Workers: []doctorWorker{}}
	problems, unreachable := 0, false

	// 1. Orca.
	orcaUp := false
	if bin, miss := env.Facts.OrcaBin(); miss != nil {
		d.Orca = doctorOrca{Status: "MISSING"}
		d.Orca.Reason, d.Orca.Fix = missingFields(miss)
		unreachable = true
	} else {
		d.Orca = doctorOrca{Bin: doctorStr(bin)}
		if env.Facts.Run == nil {
			env.Facts.Run = hostfacts.ProductionRunner(bin)
		}
		if ver, vm := env.Facts.OrcaVersion(ctx); vm != nil {
			d.Orca.Status = "UNREACHABLE"
			d.Orca.Reason, d.Orca.Fix = missingFields(vm)
			unreachable = true
		} else {
			d.Orca.Version, d.Orca.Status = doctorStr(ver), "FOUND"
			orcaUp = true
		}
	}

	// 2. What Orca knows.
	var repos []hostfacts.Repo
	var worktrees []hostfacts.Worktree
	reposOK, worktreesOK := false, false
	addUnknown := func(fact string) {
		d.Facts = append(d.Facts, doctorFact{Fact: fact, Status: "UNKNOWN", Reason: doctorStr("Orca could not be asked"), Needed: true})
	}
	if orcaUp {
		if r, m := env.Facts.Repos(ctx); m != nil {
			f := doctorFact{Fact: m.Fact, Status: "MISSING", Needed: true}
			f.Reason, f.Fix = missingFields(m)
			d.Facts = append(d.Facts, f)
			unreachable = true
		} else {
			repos, reposOK = r, true
			d.Facts = append(d.Facts, doctorFact{Fact: "orca repos", Status: "FOUND", Value: doctorStr(fmt.Sprintf("%d registered", len(r))), Needed: true})
		}
		if w, m := env.Facts.Worktrees(ctx); m != nil {
			f := doctorFact{Fact: m.Fact, Status: "MISSING", Needed: true}
			f.Reason, f.Fix = missingFields(m)
			d.Facts = append(d.Facts, f)
			unreachable = true
		} else {
			worktrees, worktreesOK = w, true
			d.Facts = append(d.Facts, doctorFact{Fact: "orca worktrees", Status: "FOUND", Value: doctorStr(fmt.Sprintf("%d listed", len(w))), Needed: true})
		}
	} else {
		addUnknown("orca repos")
		addUnknown("orca worktrees")
	}

	// 3. The accounts file.
	apath, perr := env.AccountsPath()
	var acc hostfacts.Accounts
	var accErr error
	if perr != nil {
		accErr = perr
	} else {
		acc, accErr = hostfacts.LoadAccounts(apath)
	}
	d.AccountsFile = doctorAccountsFile{Path: apath, Present: acc.Present}
	if accErr != nil {
		d.AccountsFile.Error = doctorStr(accErr.Error())
		problems++
	}
	progOf := func(family string) (string, *hostfacts.Missing) { return env.Facts.AgentProgram(family) }
	for _, name := range acc.Names {
		e := acc.Entries[name]
		a := doctorAccount{Name: name, Family: e.Family, Tool: e.Tool(), Problems: []doctorProblem{}}
		if e.Program != "" {
			a.Program = doctorStr(e.Program)
			a.ProgramFound = env.Facts.IsExecutable(e.Program)
			if !a.ProgramFound {
				a.Problems = append(a.Problems, doctorProblem{Code: codeAccountProgram, Message: fmt.Sprintf("program %s is not an executable file", e.Program), Fix: "correct `program` in the accounts file, or install it"})
			}
		} else if p, m := progOf(e.Family); m != nil {
			a.Problems = append(a.Problems, doctorProblem{Code: codeAccountProgram, Message: m.Reason, Fix: m.Fix})
		} else {
			a.Program, a.ProgramFound = doctorStr(p), true
		}
		if e.ConfigDir != "" {
			a.ConfigDir = doctorStr(e.ConfigDir)
			found := env.IsDir(e.ConfigDir)
			a.ConfigDirFound = &found
			if !found {
				a.Problems = append(a.Problems, doctorProblem{Code: codeAccountConfigDir, Message: fmt.Sprintf("configDir %s is not a directory", e.ConfigDir), Fix: "create the directory, or correct `configDir` in the accounts file"})
			}
		}
		problems += len(a.Problems)
		d.Accounts = append(d.Accounts, a)
	}

	// 4. The assignments (--host), which decide which family programs are needed.
	var setupErr error
	needFamily := map[string]bool{}
	if withHost {
		sd, err := env.Setup(hostID)
		d.Setup = &doctorSetup{Read: "OK"}
		if hostID != "" {
			d.Setup.HostID = doctorStr(hostID)
		}
		switch {
		case err != nil:
			d.Setup.Read, d.Setup.Error = "FAILED", doctorStr(err.Error())
			setupErr = err
		case sd.Source != "SERVER":
			d.Setup.Source = doctorStr(sd.Source)
			d.Setup.Note = doctorStr(notPlatformSetup)
		default:
			d.Setup.Source = doctorStr(sd.Source)
			prof := sd.Profile
			d.Setup.Profile = &prof
			for _, a := range sd.Assignments {
				w := evalWorker(env, a, sd.Profile, acc, accErr, apath, repos, worktrees, reposOK && worktreesOK, orcaUp)
				needFamily[hostfacts.CanonicalFamily(a.Family)] = true
				problems += len(w.Problems)
				d.Workers = append(d.Workers, w)
			}
		}
	}

	// 5. Programs per family: a missing one only counts when an assigned worker needs it.
	for _, fam := range []string{"claude", "codex", "glm", "kimi"} {
		f := doctorFact{Fact: "agent program (" + fam + ")", Needed: needFamily[fam]}
		if p, m := progOf(fam); m != nil {
			f.Status = "MISSING"
			f.Reason, f.Fix = missingFields(m)
		} else {
			f.Status, f.Value = "FOUND", doctorStr(p)
		}
		d.Facts = append(d.Facts, f)
	}

	// 6. The state directory.
	sf := doctorFact{Fact: "state directory", Needed: true}
	dir, sm := env.Facts.StateDirOnDisk()
	if sm != nil {
		sf.Status, sf.Value = "MISSING", doctorStr(dir)
		sf.Reason, sf.Fix = missingFields(sm)
		problems++
	} else {
		sf.Status, sf.Value = "FOUND", doctorStr(dir)
	}
	d.Facts = append(d.Facts, sf)

	_ = reposOK
	_ = worktreesOK
	code := 0
	switch {
	case unreachable:
		code = exitcode.OrcaUnreachable
	case problems > 0:
		code = exitcode.Conflict
	}
	d.OK = code == 0
	return d, code, setupErr
}

// evalWorker checks one assignment: its workdir, its account, its program.
func evalWorker(env hostDoctorEnv, a hostAssignmentDTO, profile string, acc hostfacts.Accounts, accErr error, apath string,
	repos []hostfacts.Repo, worktrees []hostfacts.Worktree, orcaKnown, orcaUp bool) doctorWorker {
	name := a.WorkerID
	if a.WorkerName != nil && *a.WorkerName != "" {
		name = *a.WorkerName
	}
	w := doctorWorker{Worker: name, Workdir: a.Workdir, Family: a.Family, Status: "OK", Problems: []doctorProblem{}}
	fam := hostfacts.CanonicalFamily(a.Family)

	// Workdir.
	if orcaUp && orcaKnown {
		path, cands, res := hostfacts.ResolveWorkdir(a.Workdir, repos, worktrees)
		switch res {
		case hostfacts.Found:
			w.WorkdirPath = doctorStr(path)
		case hostfacts.Ambiguous:
			w.Problems = append(w.Problems, doctorProblem{Code: hostfacts.CodeWorkdirAmbiguousOnHost,
				Message: fmt.Sprintf("%s matches %d worktrees on this host: %s", a.Workdir, len(cands), strings.Join(cands, ", ")),
				Fix:     "rename or archive all but one, so the workdir (the repository name and the directory name) names one worktree"})
		default:
			w.Problems = append(w.Problems, doctorProblem{Code: hostfacts.CodeWorkdirNotOnHost,
				Message: fmt.Sprintf("%s is not a worktree Orca knows on this host", a.Workdir),
				Fix:     "create it with `hadron team worker onboard`, or add it to Orca (`orca worktree create`)"})
		}
	}

	// Account.
	progFromEntry := false
	switch {
	case profile != hostProfileAvailable:
		w.AccountStatus = "unknown"
	case a.AccountProfile == nil || *a.AccountProfile == "":
		w.AccountStatus = "default"
	default:
		account := *a.AccountProfile
		w.Account = doctorStr(account)
		switch {
		case accErr != nil:
			w.AccountStatus = "missing"
			w.Problems = append(w.Problems, doctorProblem{Code: hostfacts.CodeAccountNotOnHost,
				Message: fmt.Sprintf("account %q cannot be resolved: the accounts file is refused (%v)", account, accErr),
				Fix:     "fix the accounts file at " + apath})
		case !acc.Present:
			w.AccountStatus = "missing"
			w.Problems = append(w.Problems, doctorProblem{Code: hostfacts.CodeAccountNotOnHost,
				Message: fmt.Sprintf("account %q is named but there is no accounts file at %s", account, apath),
				Fix:     fmt.Sprintf("create the accounts file with an entry named %q (see `hadron team host doctor --help`)", account)})
		default:
			e, ok := acc.Entries[account]
			switch {
			case !ok:
				w.AccountStatus = "missing"
				w.Problems = append(w.Problems, doctorProblem{Code: hostfacts.CodeAccountNotOnHost,
					Message: fmt.Sprintf("the accounts file has no account named %q", account),
					Fix:     fmt.Sprintf("add an entry named %q under `accounts:` in %s", account, apath)})
			case e.Family != fam:
				w.AccountStatus = "missing"
				w.Problems = append(w.Problems, doctorProblem{Code: hostfacts.CodeAccountNotOnHost,
					Message: fmt.Sprintf("account %q is for the family %s, but this worker's family is %s", account, e.Family, fam),
					Fix:     "give the worker an account of its own family, or correct `family` in the entry"})
			default:
				w.AccountStatus = "ok"
				progFromEntry = true
				if e.Program != "" {
					w.Program = doctorStr(e.Program)
					if !env.Facts.IsExecutable(e.Program) {
						w.AccountStatus = "missing"
						w.Problems = append(w.Problems, doctorProblem{Code: hostfacts.CodeAccountNotOnHost,
							Message: fmt.Sprintf("account %q: program %s is not an executable file", account, e.Program),
							Fix:     "correct `program` in the accounts file, or install it"})
					}
				} else {
					progFromEntry = false
				}
				if e.ConfigDir != "" && !env.IsDir(e.ConfigDir) {
					w.AccountStatus = "missing"
					w.Problems = append(w.Problems, doctorProblem{Code: hostfacts.CodeAccountNotOnHost,
						Message: fmt.Sprintf("account %q: configDir %s is not a directory", account, e.ConfigDir),
						Fix:     "create the directory, or correct `configDir` in the accounts file"})
				}
			}
		}
	}

	// Program: the entry's own, else the family's default.
	if !progFromEntry {
		if p, m := env.Facts.AgentProgram(fam); m != nil {
			w.Problems = append(w.Problems, doctorProblem{Code: hostfacts.CodeProgramNotOnHost, Message: m.Reason, Fix: m.Fix})
		} else {
			w.Program = doctorStr(p)
		}
	}
	if len(w.Problems) > 0 {
		w.Status = "MISSING"
	}
	return w
}

func renderHostDoctor(w io.Writer, d doctorDTO) error {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format+"\n", a...) }
	p("host doctor: what this machine has, detected now (nothing is read from a host file or kept)")
	switch d.Orca.Status {
	case "FOUND":
		p("orca: found %s (version %s)", output.SafeCell(*d.Orca.Bin), output.SafeCell(*d.Orca.Version))
	default:
		p("orca: %s: %s", d.Orca.Status, output.SafeCell(*d.Orca.Reason))
		p("  fix: %s", output.SafeCell(*d.Orca.Fix))
	}
	for _, f := range d.Facts {
		switch f.Status {
		case "FOUND":
			p("%s: found, %s", f.Fact, output.SafeCell(*f.Value))
		case "UNKNOWN":
			p("%s: unknown (%s)", f.Fact, output.SafeCell(*f.Reason))
		default:
			note := ""
			if !f.Needed {
				note = " (informational: no assigned worker needs it)"
			}
			p("%s: MISSING: %s%s", f.Fact, output.SafeCell(*f.Reason), note)
			p("  fix: %s", output.SafeCell(*f.Fix))
		}
	}
	p("default accounts: %s", defaultAccountsNote)
	switch {
	case d.AccountsFile.Error != nil:
		p("accounts file %s: REFUSED: %s", output.SafeCell(d.AccountsFile.Path), output.SafeCell(*d.AccountsFile.Error))
	case !d.AccountsFile.Present:
		p("accounts file %s: absent (optional: it names extra accounts)", output.SafeCell(d.AccountsFile.Path))
	default:
		p("accounts file %s: %d account(s)", output.SafeCell(d.AccountsFile.Path), len(d.Accounts))
	}
	for _, a := range d.Accounts {
		if len(a.Problems) == 0 {
			p("  account %s (%s): ok", output.SafeCell(a.Name), a.Family)
			continue
		}
		p("  account %s (%s): MISSING", output.SafeCell(a.Name), a.Family)
		for _, pr := range a.Problems {
			p("    %s: %s; fix: %s", pr.Code, output.SafeCell(pr.Message), output.SafeCell(pr.Fix))
		}
	}
	if d.Setup != nil {
		switch {
		case d.Setup.Read == "FAILED":
			p("assignments: could not be read: %s", output.SafeCell(*d.Setup.Error))
		case d.Setup.Note != nil:
			p("%s", *d.Setup.Note)
		default:
			p("assignments: %d worker(s) assigned (profile %s)", len(d.Workers), *d.Setup.Profile)
		}
		for _, wk := range d.Workers {
			acct := "account " + d.accountLabel(wk)
			if len(wk.Problems) == 0 {
				p("  worker %s %s (%s): OK (%s)", output.SafeCell(wk.Worker), output.SafeCell(wk.Workdir), output.SafeCell(wk.Family), acct)
				continue
			}
			p("  worker %s %s (%s): MISSING", output.SafeCell(wk.Worker), output.SafeCell(wk.Workdir), output.SafeCell(wk.Family))
			for _, pr := range wk.Problems {
				p("    %s: %s; fix: %s", pr.Code, output.SafeCell(pr.Message), output.SafeCell(pr.Fix))
			}
		}
	}
	if d.OK {
		p("result: ok")
	} else {
		p("result: something is missing (see above)")
	}
	return nil
}

func (d doctorDTO) accountLabel(w doctorWorker) string {
	switch w.AccountStatus {
	case "default":
		return "default"
	case "unknown":
		return "unknown (this server does not send the account profile)"
	case "ok":
		return *w.Account
	}
	return "missing"
}

func newCmdTeamHostDoctor(f *cmdutil.Factory) *cobra.Command {
	var hostID string
	cmd := &cobra.Command{
		Use:   "doctor [--host <host-id>] [--json]",
		Short: "Check what this machine has for hosting workers",
		Long: `Check what this machine has, one line per fact, found or missing with the step that fixes it. Every fact
is detected when you run this; nothing is read from a host file and nothing is kept.

It reports: Orca (where it is and its version, the repositories and worktrees it knows), the program each
family runs (claude, codex, claude-glm, claude-kimi), whether the state directory is on disk and not tmpfs, and the
accounts file if there is one. The accounts file is optional: ` + "`~/.config/hadron/accounts.yaml`" + ` (in the Hadron
config directory on Linux and macOS, mode 0600) names extra accounts a worker can run under:

  accounts:
    work:
      family: claude            # claude, codex, glm or kimi
      program: /abs/path/claude # optional
      configDir: /abs/path      # optional

It takes accounts only: a workers, worktrees, repos or host-settings key is refused. The names ` + "`tool`" + ` and ` + "`version`" + `
are reserved for a newer hadron; a file that uses one is answered "needs a newer hadron". With no account named, claude,
glm and kimi use ~/.claude and codex uses Orca's active managed Codex account. Only names ever leave this machine.

--host <host-id> also reads the workers assigned to that host (as you, or through the worktree's binding: the
same read as ` + "`team host setup`" + `) and checks each one: its workdir (<repository name>/<directory name>) is one worktree
Orca knows (WORKDIR_NOT_ON_HOST or WORKDIR_AMBIGUOUS_ON_HOST, which names the candidates), its account is in the
accounts file with the worker's family (ACCOUNT_NOT_ON_HOST), and its program is on PATH (PROGRAM_NOT_ON_HOST). Without
--host the command makes no request. When the App does not take its setup from the platform it says so and checks no
worker.

Exit 0: the host can run every assigned worker. Exit 5: something is missing (a worker's workdir, account or
program, a broken accounts-file entry or a refused file, a state directory on tmpfs); a program no assigned worker
needs is informational. Exit 10: Orca is not on this machine or did not answer, which dominates exit 5. --json prints
the whole report at exit 0, 5 and 10. If the assignments read fails, the local lines are printed and the command
exits with that read's own code (3, 7 or 8).`,
		Example: "  hadron team host doctor\n  hadron team host doctor --host orca1 --app acme.com:eng-team\n  hadron team host doctor --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			withHost := cmd.Flags().Changed("host")
			if withHost && strings.TrimSpace(hostID) == "" {
				return exitcode.Newf(exitcode.Usage, "--host needs a host id; omit the flag to check only this machine")
			}
			env := newHostDoctorEnv(cmd, f)
			d, code, setupErr := buildHostDoctor(cmd.Context(), env, strings.TrimSpace(hostID), withHost)
			if err := output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error { return renderHostDoctor(w, d) }); err != nil {
				return err
			}
			if setupErr != nil {
				_, _ = fmt.Fprintln(f.IOStreams.ErrOut, setupErr.Error())
				if code == 0 || code == exitcode.Conflict {
					code = exitcode.FromError(setupErr)
				}
			}
			if code != 0 {
				return exitcode.Silent(code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&hostID, "host", "", "also check the workers assigned to this host id (reads the assignments as you)")
	return cmd
}
