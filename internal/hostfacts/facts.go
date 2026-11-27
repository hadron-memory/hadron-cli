// Package hostfacts detects the facts about the machine a host runs on: where Orca is, which
// repositories and worktrees it knows, which agent program a family runs, whether the state
// directory is on disk. Nothing is read from a hand-written host file and nothing is cached: a
// fact is detected when it is needed, so it cannot go stale (hadron-cli#3236, the "Host file
// removal" epic hadron-server#5209).
//
// Every function takes an injected runner or lookup, so tests need no Orca. Each returns the value,
// or a *Missing that carries the fix text. The accounts-file loader is in accounts.go.
package hostfacts

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// Problem codes shared with `team host doctor` and (hadron-cli#3237) `host run`: the same words
// name the same trouble on every surface.
const (
	CodeWorkdirNotOnHost       = "WORKDIR_NOT_ON_HOST"
	CodeWorkdirAmbiguousOnHost = "WORKDIR_AMBIGUOUS_ON_HOST"
	CodeAccountNotOnHost       = "ACCOUNT_NOT_ON_HOST"
	CodeProgramNotOnHost       = "PROGRAM_NOT_ON_HOST"
	// An accounts-file ENTRY whose own program or configDir is missing. A worker that uses such an entry gets
	// CodeAccountNotOnHost naming this cause, so the two layers read as one story.
	CodeAccountProgramNotFound   = "ACCOUNT_PROGRAM_NOT_FOUND"
	CodeAccountConfigDirNotFound = "ACCOUNT_CONFIGDIR_NOT_FOUND"
)

// worktreeLimit is the row cap asked of `orca worktree list`: Orca returns 200 rows by default and
// answers truncated:true past that, so a bare list would report a worktree beyond the 200th as
// missing. 5000 is far above any real host; a still-truncated answer is refused (see Worktrees).
const worktreeLimit = "5000"

var limitRE = regexp.MustCompile(`^[0-9]{1,6}$`)

const macOrcaBundle = "/Applications/Orca.app/Contents/Resources/bin/orca"

// Runner runs one `orca` call; argv starts at the verb. It is orcaterm's shape, with the binary bound by
// the caller, so a fake never sees it.
type Runner = orcaterm.Runner

// Missing is a fact that could not be found, with the command or step that fixes it. Unreachable is true
// when the trouble is Orca itself (not found, not answering, an answer that cannot be trusted): the
// distinction `team host doctor` turns into exit 10.
type Missing struct {
	Fact        string
	Reason      string
	Fix         string
	Unreachable bool
}

func (m *Missing) Error() string { return m.Fact + ": " + m.Reason + " (" + m.Fix + ")" }

// Repo is one repository Orca knows.
type Repo struct {
	ID, Path, DisplayName, Kind string
}

// Worktree is one worktree (or folder workspace) Orca lists.
type Worktree struct {
	ID, RepoID, Path, Branch string
	Archived                 bool
}

// Facts is the detector. All fields are seams; New fills the production values.
type Facts struct {
	Run          Runner
	LookPath     func(string) (string, error)
	IsExecutable func(path string) bool
	Getenv       func(string) string
	Home         string
	GOOS         string
	// FSType names the filesystem kind under path ("tmpfs", "ramfs", another name, or "" when unknown).
	FSType func(path string) (string, error)
}

// New returns a Facts over run, with the production lookups. run may be nil until Orca is found.
func New(run Runner) *Facts {
	home, _ := os.UserHomeDir()
	return &Facts{
		Run:          run,
		LookPath:     exec.LookPath,
		IsExecutable: isExecutableFile,
		Getenv:       os.Getenv,
		Home:         home,
		GOOS:         runtime.GOOS,
		FSType:       defaultFSType,
	}
}

func isExecutableFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// OrcaBin finds the `orca` command: on PATH first, then Orca's own CLI directory on Linux
// (~/.config/orca/cli/bin/orca) or its app bundle on macOS.
func (f *Facts) OrcaBin() (string, *Missing) {
	if p, err := f.LookPath("orca"); err == nil && p != "" {
		return p, nil
	}
	var fallback string
	if f.GOOS == "darwin" {
		fallback = macOrcaBundle
	} else if f.Home != "" && f.GOOS != "windows" {
		// Orca's own CLI directory on Linux; its Windows path is not known, so no fallback is guessed there.
		fallback = filepath.Join(f.Home, ".config", "orca", "cli", "bin", "orca")
	}
	if fallback != "" && f.IsExecutable(fallback) {
		return fallback, nil
	}
	return "", &Missing{
		Fact:        "orca",
		Reason:      "the Orca command was not found on PATH or in Orca's own CLI directory",
		Fix:         "install Orca and make its `orca` command available on PATH",
		Unreachable: true,
	}
}

func (f *Facts) orcaDown(fact, what string) *Missing {
	return &Missing{
		Fact:        fact,
		Reason:      what,
		Fix:         "start Orca (the app, or `orca serve` on a headless host) and check it with `orca status`",
		Unreachable: true,
	}
}

func (f *Facts) run(ctx context.Context, argv ...string) (orcaterm.Result, bool) {
	if f.Run == nil {
		return orcaterm.Result{}, false
	}
	return f.Run(ctx, argv), true
}

// OrcaVersion is what `orca --version` prints.
func (f *Facts) OrcaVersion(ctx context.Context) (string, *Missing) {
	res, ran := f.run(ctx, "--version")
	v := strings.TrimSpace(res.Stdout)
	if !ran || res.Code != 0 || v == "" {
		return "", f.orcaDown("orca version", "Orca did not answer `orca --version`")
	}
	return v, nil
}

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
}

func (f *Facts) read(ctx context.Context, fact string, argv ...string) (json.RawMessage, *Missing) {
	res, ran := f.run(ctx, argv...)
	if !ran || res.Code != 0 {
		return nil, f.orcaDown(fact, "Orca did not answer `orca "+strings.Join(argv, " ")+"`")
	}
	var env envelope
	if json.Unmarshal([]byte(res.Stdout), &env) != nil || !env.OK || len(env.Result) == 0 {
		return nil, f.orcaDown(fact, "Orca's answer to `orca "+strings.Join(argv, " ")+"` is not one we can trust")
	}
	return env.Result, nil
}

// Repos lists the repositories Orca has registered.
func (f *Facts) Repos(ctx context.Context) ([]Repo, *Missing) {
	raw, miss := f.read(ctx, "orca repos", "repo", "list", "--json")
	if miss != nil {
		return nil, miss
	}
	var r struct {
		Repos *[]struct {
			ID          string `json:"id"`
			Path        string `json:"path"`
			DisplayName string `json:"displayName"`
			Kind        string `json:"kind"`
		} `json:"repos"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Repos == nil {
		return nil, f.orcaDown("orca repos", "Orca's repository list has no `repos` list")
	}
	out := make([]Repo, 0, len(*r.Repos))
	for _, x := range *r.Repos {
		out = append(out, Repo{ID: x.ID, Path: x.Path, DisplayName: x.DisplayName, Kind: x.Kind})
	}
	return out, nil
}

// Worktrees lists every worktree Orca knows, archived ones included (marked). A list Orca says is truncated
// is refused: resolving a workdir against a partial list would report a real worktree as missing.
func (f *Facts) Worktrees(ctx context.Context) ([]Worktree, *Missing) {
	raw, miss := f.read(ctx, "orca worktrees", "worktree", "list", "--json", "--limit", worktreeLimit)
	if miss != nil {
		return nil, miss
	}
	var r struct {
		Worktrees *[]struct {
			ID         string `json:"id"`
			RepoID     string `json:"repoId"`
			Path       string `json:"path"`
			Branch     string `json:"branch"`
			IsArchived bool   `json:"isArchived"`
		} `json:"worktrees"`
		Truncated bool `json:"truncated"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Worktrees == nil {
		return nil, f.orcaDown("orca worktrees", "Orca's worktree list has no `worktrees` list")
	}
	if r.Truncated {
		return nil, f.orcaDown("orca worktrees", "Orca cut its worktree list short (truncated), so a missing worktree could not be told from an unlisted one")
	}
	out := make([]Worktree, 0, len(*r.Worktrees))
	for _, x := range *r.Worktrees {
		out = append(out, Worktree{ID: x.ID, RepoID: x.RepoID, Path: x.Path, Branch: x.Branch, Archived: x.IsArchived})
	}
	return out, nil
}

// Resolution is how a workdir token resolved.
type Resolution int

const (
	Found Resolution = iota
	NotOnHost
	Ambiguous
)

// ResolveWorkdir resolves a workdir token to a worktree path. The token is `<repo displayName>/<basename of
// the worktree path>`, joined on the worktree's repo (hadron-server#5210: the worktree's own displayName does
// not match). An archived worktree is not a place to launch and never matches. An ambiguous token returns
// every candidate path, sorted, and picks none.
func ResolveWorkdir(token string, repos []Repo, worktrees []Worktree) (string, []string, Resolution) {
	names := map[string]string{}
	for _, r := range repos {
		names[r.ID] = r.DisplayName
	}
	var cands []string
	for _, w := range worktrees {
		if w.Archived {
			continue
		}
		name, ok := names[w.RepoID]
		if !ok || name+"/"+filepath.Base(w.Path) != token {
			continue
		}
		cands = append(cands, w.Path)
	}
	sort.Strings(cands)
	switch len(cands) {
	case 0:
		return "", nil, NotOnHost
	case 1:
		return cands[0], nil, Found
	}
	return "", cands, Ambiguous
}

// CanonicalFamily folds a provider name to the family a host runs (anthropic is claude, openai is codex).
func CanonicalFamily(s string) string {
	f := strings.ToLower(strings.TrimSpace(s))
	switch f {
	case "anthropic":
		return "claude"
	case "openai":
		return "codex"
	}
	return f
}

// DefaultProgramName is the command a family runs when an accounts-file entry names no program
// (hadron-server#5210): claude, codex, claude-glm and claude-kimi. "" for any other family.
func DefaultProgramName(family string) string {
	switch CanonicalFamily(family) {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	case "glm":
		return "claude-glm"
	case "kimi":
		return "claude-kimi"
	}
	return ""
}

// AgentProgram finds the program a family runs on PATH.
func (f *Facts) AgentProgram(family string) (string, *Missing) {
	fam := CanonicalFamily(family)
	name := DefaultProgramName(fam)
	fact := "agent program (" + fam + ")"
	if name == "" {
		return "", &Missing{Fact: fact, Reason: "there is no default program for the family " + fam,
			Fix: "name its `program` in an entry of the accounts file"}
	}
	if p, err := f.LookPath(name); err == nil && p != "" {
		return p, nil
	}
	return "", &Missing{Fact: fact, Reason: "`" + name + "` is not on PATH",
		Fix: "install `" + name + "`, or name the program's absolute path as `program` in an entry of the accounts file"}
}

// StateDir is where a host keeps its own durable state: $XDG_STATE_HOME/hadron, else ~/.local/state/hadron.
// The rule is config.StateDirFrom, the one the host credential store uses (hadron-cli#3239), read through
// this package's Getenv and Home seams.
func (f *Facts) StateDir() string {
	return config.StateDirFrom(f.Getenv, f.Home)
}

// StateDirOnDisk reports the state directory, and a Missing when it sits on tmpfs or ramfs: such a directory
// is emptied at reboot, so a host's state would not survive it. The path is returned either way.
func (f *Facts) StateDirOnDisk() (string, *Missing) {
	dir := f.StateDir()
	kind, err := f.FSType(dir)
	if err == nil && (kind == "tmpfs" || kind == "ramfs") {
		return dir, &Missing{Fact: "state directory", Reason: dir + " is on " + kind + ", which is emptied at reboot",
			Fix: "set XDG_STATE_HOME to a directory on disk"}
	}
	return dir, nil
}

// allowedRead reports whether argv is one of the three reads this package makes.
func allowedRead(argv []string) bool {
	switch {
	case len(argv) == 1 && argv[0] == "--version":
		return true
	case len(argv) == 3 && argv[0] == "repo" && argv[1] == "list" && argv[2] == "--json":
		return true
	case len(argv) == 5 && argv[0] == "worktree" && argv[1] == "list" && argv[2] == "--json" && argv[3] == "--limit":
		return limitRE.MatchString(argv[4])
	}
	return false
}

// Guard closes a runner over the three reads. Nothing else of Orca's runs through it: never an account,
// terminal or send call.
func Guard(run Runner) Runner {
	return func(ctx context.Context, argv []string) orcaterm.Result {
		if !allowedRead(argv) {
			return orcaterm.Result{Code: 1, Stderr: "refused: not a host-facts read"}
		}
		return run(ctx, argv)
	}
}

// ScrubbedEnv keeps what Orca's CLI needs to find its runtime and nothing of Hadron's or any provider's: no
// server token, host key or API key reaches the child.
func ScrubbedEnv(environ []string) []string {
	keep := map[string]bool{"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "LANG": true, "TMPDIR": true,
		"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true, "XDG_RUNTIME_DIR": true}
	var out []string
	for _, kv := range environ {
		if i := strings.IndexByte(kv, '='); i > 0 && keep[kv[:i]] {
			out = append(out, kv)
		}
	}
	return out
}

// ProductionRunner execs the found `orca` with the guard and the scrubbed environment, one call bounded at 15 s.
func ProductionRunner(bin string) Runner {
	return Guard(orcaterm.ExecEnv(bin, 15*time.Second, ScrubbedEnv(os.Environ())))
}
