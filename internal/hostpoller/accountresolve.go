package hostpoller

import (
	"errors"
	"fmt"
	"os"

	"github.com/hadron-memory/hadron-cli/internal/hostfacts"
)

// Account resolution for a host that runs from the platform (hadron-cli#3237, C3 of hadron-server#5209; R7 of
// the epic). The platform names an account only by NAME (an assignment's accountProfile, or a start's); which
// program and directory hold it is this host's business, kept in the accounts file (hostfacts.LoadAccounts),
// and nothing about it leaves the machine. It is read at the moment of use, never cached:
//
//   - no name: the launch family's own program (hostfacts.AgentProgram) with no account environment;
//   - a name: the accounts file's entry, whose family must equal the launch family (canonical equality,
//     Ruling Q5 (a)), launched with the entry's program (else the family's) and its configDir as the tool's
//     account directory.
//
// A failure is a reason led by its code (ACCOUNT_NOT_ON_HOST, PROGRAM_NOT_ON_HOST), naming the family and the
// account (cor:agt:070:08), decided before any process or terminal exists. Nothing falls back to another account.

// LaunchRefusal is a start this host refuses before launching anything, led by its code. Error() is the
// reason REPORTED to the server, so it carries names only — never the accounts file's path, a program or a
// directory (the issue's "only names leave the machine"); Local is the detail for this host's own log.
type LaunchRefusal struct {
	Code  string
	Msg   string
	Local string
}

func (e *LaunchRefusal) Error() string { return e.Code + ": " + e.Msg }

// AccountLookup is what resolution reads, as seams; PlatformAccountLookup fills the production values.
type AccountLookup struct {
	// Program finds the family's own program (hostfacts.Facts.AgentProgram).
	Program func(family string) (string, *hostfacts.Missing)
	// Load reads the accounts file now (hostfacts.LoadAccounts at hostfacts.AccountsPath).
	Load func() (hostfacts.Accounts, error)
	// IsExecutable and IsDir judge an entry's program and configDir.
	IsExecutable func(path string) bool
	IsDir        func(path string) bool
}

// PlatformAccountLookup is the production lookup over facts and the CLI's accounts file.
func PlatformAccountLookup(facts *hostfacts.Facts) AccountLookup {
	return AccountLookup{
		Program: facts.AgentProgram,
		Load: func() (hostfacts.Accounts, error) {
			path, err := hostfacts.AccountsPath()
			if err != nil {
				return hostfacts.Accounts{}, err
			}
			return hostfacts.LoadAccounts(path)
		},
		IsExecutable: facts.IsExecutable,
		IsDir: func(p string) bool {
			st, err := os.Stat(p)
			return err == nil && st.IsDir()
		},
	}
}

// Resolve answers the account a launch of family runs under. name "" is the family's own program.
func (l AccountLookup) Resolve(family, name string) (AccountProfile, error) {
	fam := CanonicalFamily(family)
	if name == "" {
		bin, miss := l.Program(fam)
		if miss != nil {
			return AccountProfile{}, &LaunchRefusal{Code: hostfacts.CodeProgramNotOnHost,
				Msg: fmt.Sprintf("family %s, no account: %s; %s; nothing was launched", fam, miss.Reason, miss.Fix)}
		}
		return AccountProfile{Bin: bin, Families: []string{fam}}, nil
	}
	accts, err := l.Load()
	if err != nil {
		why := "it is refused"
		var ae *hostfacts.AccountsError
		if errors.As(err, &ae) && ae.NeedsNewer {
			why = "it needs a newer hadron"
		}
		return AccountProfile{}, &LaunchRefusal{Code: hostfacts.CodeAccountNotOnHost, Local: err.Error(),
			Msg: fmt.Sprintf("family %s, account %q: this host's accounts file cannot be used (%s); run hadron team host doctor on the host, which names the problem; nothing was launched", fam, name, why)}
	}
	e, ok := accts.Entries[name]
	if !ok {
		where := "this host's accounts file"
		if !accts.Present {
			where = "this host's accounts file, which does not exist"
		}
		return AccountProfile{}, &LaunchRefusal{Code: hostfacts.CodeAccountNotOnHost, Local: accts.Path,
			Msg: fmt.Sprintf("family %s, account %q: no such account in %s; add an entry named %q (family %s) to accounts.yaml in the host's Hadron config directory; nothing was launched", fam, name, where, name, fam)}
	}
	if e.Family != fam {
		return AccountProfile{}, &LaunchRefusal{Code: hostfacts.CodeAccountNotOnHost,
			Msg: fmt.Sprintf("family %s, account %q: the entry is for family %s, not %s; use an account of family %s; nothing was launched", fam, name, e.Family, fam, fam)}
	}
	a := AccountProfile{Bin: e.Program, Families: []string{fam}}
	if a.Bin == "" {
		bin, miss := l.Program(fam)
		if miss != nil {
			return AccountProfile{}, &LaunchRefusal{Code: hostfacts.CodeProgramNotOnHost,
				Msg: fmt.Sprintf("family %s, account %q: %s; %s; nothing was launched", fam, name, miss.Reason, miss.Fix)}
		}
		a.Bin = bin
	} else if !l.IsExecutable(a.Bin) {
		return AccountProfile{}, &LaunchRefusal{Code: hostfacts.CodeProgramNotOnHost, Local: a.Bin,
			Msg: fmt.Sprintf("family %s, account %q: the entry's program is not an executable file on this host; install it or correct the entry's program; nothing was launched", fam, name)}
	}
	if e.ConfigDir != "" {
		if !l.IsDir(e.ConfigDir) {
			return AccountProfile{}, &LaunchRefusal{Code: hostfacts.CodeAccountNotOnHost, Local: e.ConfigDir,
				Msg: fmt.Sprintf("family %s, account %q: the entry's configDir is not a directory on this host; create it and log the account in there, or correct configDir; nothing was launched", fam, name)}
		}
		if fam == "codex" {
			a.CodexHome = e.ConfigDir
		} else {
			a.ClaudeConfigDir = e.ConfigDir
		}
	}
	return a, nil
}
