package team

import (
	"context"
	"errors"
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

// The reasons a doctor run checked no worker (doctorDTO.WorkersNotChecked).
const (
	notCheckedNoHost    = "no --host"
	notCheckedHostSetup = "host-sourced setup"
	notCheckedReadFail  = "read failed"
)

const (
	// doctorExitMissing is exit 5: something this host needs is missing. It is exitcode.Conflict, the code
	// `team host settings-check` also returns for a finding.
	doctorExitMissing   = exitcode.Conflict
	notPlatformSetup    = "App does not take its setup from the platform: its workers come from the host file, so doctor checks no assignments."
	defaultAccountsNote = "claude, glm and kimi use ~/.claude; codex uses Orca's active managed Codex account"
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
	Fact string `json:"fact"`
	// FOUND; MISSING (something needs it: counts toward exit 5); NOT_FOUND (a program no assigned worker relies
	// on, or that no worker was read to need: informational); UNKNOWN (Orca could not be asked).
	Status string  `json:"status"`
	Value  *string `json:"value"`
	Reason *string `json:"reason"`
	Fix    *string `json:"fix"`
	// Needed is true when something relies on the fact. A program is needed only by an assigned worker that
	// runs the family's default program; without --host nothing was read, so it is false.
	Needed bool `json:"needed"`
}

type doctorAccountsFile struct {
	Path    string  `json:"path"`
	Present bool    `json:"present"`
	Error   *string `json:"error"`
}

type doctorAccount struct {
	Name         string  `json:"name"`
	Family       string  `json:"family"`
	Tool         string  `json:"tool"`
	Program      *string `json:"program"`
	ProgramFound bool    `json:"programFound"`
	ConfigDir    *string `json:"configDir"`
	// ConfigDirFound is a pointer on purpose: null means no configDir is named, so there is nothing to find
	// (programFound has no such case: an entry always resolves a program). Do not flatten it to false.
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
	// WorkersChecked is true once the assignments were read and judged, including a read of zero assignments (then
	// there was nothing to check, and nothing was left unchecked). Exit 0 and ok:true without it mean only that the
	// machine's own facts passed.
	WorkersChecked bool `json:"workersChecked"`
	// WorkersNotChecked says why no worker was checked: "no --host", "host-sourced setup" (the App takes its setup
	// from the host file, so there are no platform assignments) or "read failed". null when WorkersChecked. Only
	// the first is something the caller can fix by passing a flag.
	WorkersNotChecked *string `json:"workersNotChecked"`
	OK                bool    `json:"ok"`
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
		msg := accErr.Error()
		var ae *hostfacts.AccountsError
		if errors.As(accErr, &ae) {
			msg = ae.Msg // the path is already in accountsFile.path
		}
		d.AccountsFile.Error = doctorStr(msg)
		problems++
	}
	progOf := func(family string) (string, *hostfacts.Missing) { return env.Facts.AgentProgram(family) }
	names := acc.Names
	if accErr != nil {
		names = nil // a refused file lists no entry as if it counted
	}
	for _, name := range names {
		e := acc.Entries[name]
		a := doctorAccount{Name: name, Family: e.Family, Tool: e.Tool(), Problems: []doctorProblem{}}
		if e.Program != "" {
			a.Program = doctorStr(e.Program)
			a.ProgramFound = env.Facts.IsExecutable(e.Program)
			if !a.ProgramFound {
				a.Problems = append(a.Problems, doctorProblem{Code: hostfacts.CodeAccountProgramNotFound, Message: fmt.Sprintf("program %s is not an executable file", e.Program), Fix: "correct `program` in the accounts file, or install it"})
			}
		} else if p, m := progOf(e.Family); m != nil {
			a.Problems = append(a.Problems, doctorProblem{Code: hostfacts.CodeAccountProgramNotFound, Message: m.Reason, Fix: m.Fix})
		} else {
			a.Program, a.ProgramFound = doctorStr(p), true
		}
		if e.ConfigDir != "" {
			a.ConfigDir = doctorStr(e.ConfigDir)
			found := env.IsDir(e.ConfigDir)
			a.ConfigDirFound = &found
			if !found {
				a.Problems = append(a.Problems, doctorProblem{Code: hostfacts.CodeAccountConfigDirNotFound, Message: fmt.Sprintf("configDir %s is not a directory", e.ConfigDir), Fix: "create the directory, or correct `configDir` in the accounts file"})
			}
		}
		problems += len(a.Problems)
		d.Accounts = append(d.Accounts, a)
	}

	// 4. The assignments (--host), which decide which family programs are needed.
	var setupErr error
	needFamily := map[string]bool{}
	d.WorkersNotChecked = doctorStr(notCheckedNoHost)
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
			d.WorkersNotChecked = doctorStr(notCheckedReadFail)
		case sd.Source != "SERVER":
			d.Setup.Source = doctorStr(sd.Source)
			d.WorkersNotChecked = doctorStr(notCheckedHostSetup)
			d.Setup.Note = doctorStr(notPlatformSetup)
		default:
			d.Setup.Source = doctorStr(sd.Source)
			prof := sd.Profile
			d.Setup.Profile = &prof
			for _, a := range sd.Assignments {
				w, usesDefault := evalWorker(env, a, sd.Profile, acc, accErr, apath, repos, worktrees, reposOK && worktreesOK, orcaUp)
				if usesDefault {
					needFamily[hostfacts.CanonicalFamily(a.Family)] = true
				}
				problems += len(w.Problems)
				d.Workers = append(d.Workers, w)
			}
			d.WorkersChecked = true
			d.WorkersNotChecked = nil
		}
	}

	// 5. Programs per family: a missing one only counts when an assigned worker needs it.
	for _, fam := range []string{"claude", "codex", "glm", "kimi"} {
		f := doctorFact{Fact: "agent program (" + fam + ")", Needed: needFamily[fam]}
		if p, m := progOf(fam); m != nil {
			f.Status = "NOT_FOUND"
			if f.Needed {
				f.Status = "MISSING"
			}
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

	code := 0
	switch {
	case unreachable:
		code = exitcode.OrcaUnreachable
	case problems > 0:
		code = doctorExitMissing
	}
	d.OK = code == 0 && setupErr == nil
	return d, code, setupErr
}

// evalWorker checks one assignment: its workdir, its account, its program.
func evalWorker(env hostDoctorEnv, a hostAssignmentDTO, profile string, acc hostfacts.Accounts, accErr error, apath string,
	repos []hostfacts.Repo, worktrees []hostfacts.Worktree, orcaKnown, orcaUp bool) (doctorWorker, bool) {
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
	// usesDefault: the worker runs the family's default program, so that program is needed on this host.
	return w, !progFromEntry
}

func renderHostDoctor(w io.Writer, d doctorDTO) error {
	var werr error
	p := func(format string, a ...any) {
		if werr == nil {
			_, werr = fmt.Fprintf(w, format+"\n", a...)
		}
	}
	fix := func(indent, text string) { p("%sfix: %s", indent, output.SafeCell(text)) }
	p("host doctor: what this machine has, detected now (nothing is read from a host file or kept)")
	switch d.Orca.Status {
	case "FOUND":
		p("orca: found %s (version %s)", output.SafeCell(*d.Orca.Bin), output.SafeCell(*d.Orca.Version))
	default:
		p("orca: %s: %s", d.Orca.Status, output.SafeCell(*d.Orca.Reason))
		fix("  ", *d.Orca.Fix)
	}
	for _, f := range d.Facts {
		switch f.Status {
		case "FOUND":
			p("%s: found, %s", f.Fact, output.SafeCell(*f.Value))
		case "UNKNOWN":
			p("%s: unknown (%s)", f.Fact, output.SafeCell(*f.Reason))
		case "NOT_FOUND":
			why := "no assigned worker runs it by default"
			if !d.WorkersChecked {
				why = "only needed if a worker runs this family; " + d.notCheckedWhy(true)
			}
			p("%s: not found: %s (%s)", f.Fact, output.SafeCell(*f.Reason), why)
			fix("  ", *f.Fix)
		default:
			p("%s: MISSING: %s", f.Fact, output.SafeCell(*f.Reason))
			fix("  ", *f.Fix)
		}
	}
	p("default accounts (when a worker names none): %s", defaultAccountsNote)
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
			p("    %s: %s", pr.Code, output.SafeCell(pr.Message))
			fix("      ", pr.Fix)
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
			acct := "account " + output.SafeCell(d.accountLabel(wk))
			// A workdir is judged only when Orca answered both reads; otherwise the row must not read as if it had been.
			const uncheckedNote = "workdir not checked: Orca did not answer"
			okNote, missingNote := "", ""
			if wk.WorkdirPath == nil && !wk.hasWorkdirProblem() {
				okNote, missingNote = "; "+uncheckedNote, " ("+uncheckedNote+")"
			}
			if len(wk.Problems) == 0 {
				p("  worker %s %s (%s): OK (%s%s)", output.SafeCell(wk.Worker), output.SafeCell(wk.Workdir), output.SafeCell(wk.Family), acct, okNote)
				continue
			}
			p("  worker %s %s (%s): MISSING%s", output.SafeCell(wk.Worker), output.SafeCell(wk.Workdir), output.SafeCell(wk.Family), missingNote)
			for _, pr := range wk.Problems {
				p("    %s: %s", pr.Code, output.SafeCell(pr.Message))
				fix("      ", pr.Fix)
			}
		}
	}
	switch {
	case d.Orca.Status != "FOUND" || orcaFactMissing(d):
		p("result: Orca is missing or did not answer (exit 10)")
	case !d.OK:
		p("result: something is missing (see above)")
	case !d.WorkersChecked:
		p("result: ok for this machine; no workers were checked (%s)", d.notCheckedWhy(false))
	default:
		p("result: ok")
	}
	return werr
}

// notCheckedWhy says why no worker was read, with the advice only when --host was not given. short is the
// clause that follows "only needed if a worker runs this family".
func (d doctorDTO) notCheckedWhy(short bool) string {
	reason := ""
	if d.WorkersNotChecked != nil {
		reason = *d.WorkersNotChecked
	}
	switch reason {
	case notCheckedHostSetup:
		return "the App takes its setup from the host file, so no assignments were read"
	case notCheckedReadFail:
		return "the assignments could not be read"
	}
	if short {
		return "pass --host to check"
	}
	return "pass --host <id> to check the workers assigned to a host"
}

// orcaFactMissing is true when Orca was found but a read of it failed, the other way exit 10 happens.
func orcaFactMissing(d doctorDTO) bool {
	for _, f := range d.Facts {
		if (f.Fact == "orca repos" || f.Fact == "orca worktrees") && f.Status == "MISSING" {
			return true
		}
	}
	return false
}

func (w doctorWorker) hasWorkdirProblem() bool {
	for _, pr := range w.Problems {
		if pr.Code == hostfacts.CodeWorkdirNotOnHost || pr.Code == hostfacts.CodeWorkdirAmbiguousOnHost {
			return true
		}
	}
	return false
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
		Long: `Check what this machine has, one line per fact: found, or missing with the step that fixes it. Every fact
is detected when you run this; nothing is read from a host file and nothing is kept.

It reports:
  Orca                where its command is, its version, and the repositories and worktrees it lists.
  agent programs      the program each family runs, found on PATH: claude runs claude, codex runs codex,
                      glm runs claude-glm, kimi runs claude-kimi.
  state directory     where hadron keeps this host's own state: $XDG_STATE_HOME/hadron, else ~/.local/state/hadron.
                      It must be on disk; on tmpfs (memory that is emptied at reboot) it is reported missing.
  accounts file       optional, below.

The accounts file is ` + "`~/.config/hadron/accounts.yaml`" + ` (in the Hadron config directory on Linux and macOS, mode 0600). It
names extra accounts a worker can run under:

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
Orca knows (WORKDIR_NOT_ON_HOST, or WORKDIR_AMBIGUOUS_ON_HOST, which names the candidates), its account is in the
accounts file with the worker's family (ACCOUNT_NOT_ON_HOST, naming the cause), and its program is on PATH
(PROGRAM_NOT_ON_HOST). An accounts-file entry whose own program or configDir is missing is reported on the entry as
ACCOUNT_PROGRAM_NOT_FOUND or ACCOUNT_CONFIGDIR_NOT_FOUND. Without --host the command makes no request and checks
no worker: a program not found is only informational, because nothing says a worker needs it. When the App does
not take its setup from the platform it says so and checks no worker.

Exit 0: everything checked is fine; without --host that is the machine's own facts only, and the last line says no
workers were checked. Exit 5: something is missing (a worker's workdir, account or program, a broken accounts-file
entry or a refused file, a state directory on tmpfs); a program no assigned worker runs by default is informational.
Exit 10: Orca is not on this machine or did not answer, which dominates exit 5. --json prints the whole report at
exit 0, 5 and 10, with workersChecked saying whether any worker was checked and, when none was, workersNotChecked
saying why ("no --host", "host-sourced setup" or "read failed"). If the assignments read fails, the local lines are
printed and the command exits with that read's own code (3, 7 or 8); the report's ok is then false. Exit 10 wins over
a failed assignments read: when Orca is also missing or did not answer, the exit is 10 and the read's error is still
printed on stderr.`,
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
				if code == 0 || code == doctorExitMissing {
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
