package hostpoller

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Launcher profiles (hadron-cli#2058 C3, decision G1): a worker's launch is
// described by typed fields, never a free-form command, and the CLI builds
// the argv from fixed templates per family and per mode. Today the profile
// comes from the host config (ConfigProfiles); the typed server fields of
// S1b (hadron-server#4230) replace that source behind the same seam.

// The two run modes (G7): a long-lived worker runs its interactive TUI in an
// Orca terminal; a short-lived one runs its task and exits.
const (
	ModeLong  = "long"
	ModeShort = "short"
)

// PromptArg stands in an argv for the first prompt. The Orca launcher puts
// the prompt in the launch's environment and this slot expands to it in
// double quotes, so arbitrary text is never typed into a shell.
const PromptArg = "\x00prompt"

// LauncherProfile is the typed description of how a worker starts (G1).
type LauncherProfile struct {
	Family  string `yaml:"family"`
	Mode    string `yaml:"mode"`
	Model   string `yaml:"model"`
	Effort  string `yaml:"effort"`
	Account string `yaml:"account"`
}

// AccountProfile is the host-local half of an account profile name: the
// launcher that selects the account (claude-holger, claude-glm, codex, …)
// and, for Codex, the home its login lives in. Host-local by design: which
// binary and directory hold an account is this host's business, and the
// server never names a command (cor:agt:070:03).
type AccountProfile struct {
	// Bin is a bare command name found on PATH, or an absolute path.
	Bin string `yaml:"bin"`
	// CodexHome is CODEX_HOME for a Codex account (Orca keeps one per
	// account); empty for the Claude family, whose launcher scripts select
	// their own CLAUDE_CONFIG_DIR.
	CodexHome string `yaml:"codex_home"`
	// Families are the families this account explicitly has a template for
	// (#2199): the only ones a start may name other than the worker's own
	// profile family. Unset, the account launches just that family.
	Families []string `yaml:"families"`
}

// ProfileSource answers a worker's launcher profile. ok is false for a
// worker that has none (it launches the configured exec argv); an error is a
// profile that exists but cannot be used, and the start is refused.
type ProfileSource interface {
	Profile(workerRef string) (LauncherProfile, AccountProfile, bool, error)
}

// ConfigProfiles is the host-config source (TRANSITIONAL: S1b's typed server
// fields replace it).
type ConfigProfiles struct{ Cfg *Config }

func (s ConfigProfiles) Profile(ref string) (LauncherProfile, AccountProfile, bool, error) {
	w, ok := s.Cfg.Workers[ref]
	if !ok || w.Profile == nil {
		return LauncherProfile{}, AccountProfile{}, false, nil
	}
	a, ok := s.Cfg.Accounts[w.Profile.Account]
	if !ok {
		return LauncherProfile{}, AccountProfile{}, false, fmt.Errorf("account profile %q is not configured on this host", w.Profile.Account)
	}
	return *w.Profile, a, true, nil
}

var (
	// A model or effort is a plain token: it can never become a flag of its
	// own, so a profile cannot smuggle --resume or anything else into argv.
	profileTokenRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	binNameRE      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// validLauncherBin: a bare command name, or an absolute path with no shell or
// whitespace characters.
func validLauncherBin(bin string) bool {
	return binNameRE.MatchString(bin) || filepath.IsAbs(bin) && !strings.ContainsAny(bin, " \t\n'\"\\$`")
}

// claudeFamily are the families whose launcher is Claude Code (GLM and Kimi
// run Claude Code against another backend through their launcher scripts).
var claudeFamily = map[string]bool{"claude": true, "glm": true, "kimi": true}

// LaunchArgv builds a worker's argv from its profile and account. It is the
// only place an argv is made for a profile, and it never resumes (G9): no
// template has --resume, --continue or a session id, and the fields it
// interpolates are plain tokens. Anything a template cannot express refuses.
func LaunchArgv(p LauncherProfile, a AccountProfile) ([]string, error) {
	fam := CanonicalFamily(p.Family)
	if p.Mode != ModeLong && p.Mode != ModeShort {
		return nil, fmt.Errorf("launcher profile mode %q must be %s or %s", p.Mode, ModeLong, ModeShort)
	}
	for name, v := range map[string]string{"model": p.Model, "effort": p.Effort} {
		if v != "" && !profileTokenRE.MatchString(v) {
			return nil, fmt.Errorf("launcher profile %s %q is not a plain token", name, v)
		}
	}
	if !validLauncherBin(a.Bin) {
		return nil, fmt.Errorf("account launcher %q must be a bare command name or an absolute path", a.Bin)
	}
	switch {
	case claudeFamily[fam]:
		// No fixed Claude Code flag carries an effort: it launches without one
		// and the bind reports NOT_APPLICABLE (#2199), never a refusal.
		argv := []string{a.Bin, "--dangerously-skip-permissions"}
		if p.Model != "" {
			argv = append(argv, "--model", p.Model)
		}
		if p.Mode == ModeShort {
			argv = append(argv, "-p") // runs the task to completion, then exits
		}
		return append(argv, PromptArg), nil
	case fam == "codex":
		argv := []string{a.Bin}
		if p.Mode == ModeShort {
			argv = append(argv, "exec") // the TUI needs a terminal and stays; exec runs and exits
		} else {
			argv = append(argv, "--no-daemon")
		}
		argv = append(argv, "--dangerously-bypass-approvals-and-sandbox")
		if p.Model != "" {
			argv = append(argv, "-m", p.Model)
		}
		if p.Effort != "" {
			argv = append(argv, "-c", "model_reasoning_effort="+p.Effort)
		}
		return append(argv, PromptArg), nil
	}
	return nil, fmt.Errorf("no launch template for family %q", p.Family)
}
