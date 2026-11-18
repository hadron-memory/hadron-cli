package team

import (
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/healthsched"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// healthBoundWorker names the worker a worktree's session binding holds. It is
// a variable only so a test can drive the title pass without real worktrees.
var healthBoundWorker = delivery.BoundWorker

// healthRename is one tab the title pass renamed, or would rename.
type healthRename struct {
	Worker   string  `json:"worker"` // the worker id, as in reports and refused
	Name     string  `json:"name"`
	Terminal string  `json:"terminal"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Applied  bool    `json:"applied"`
	Error    *string `json:"error"`
	// OrcaCode is the error code Orca answered with, passed through as an
	// opaque string (cli#2040); null when the rename succeeded, or Orca gave
	// no code (an unparsable answer, a missing binary). Error keeps its
	// value, so a consumer testing error == "rename_failed" is unaffected.
	OrcaCode *string `json:"orcaCode"`
}

// healthOrcaBin is the orca the title pass runs: the host configuration's
// delivery.orca_bin, else "orca" found on PATH. `team health install` accepts
// either (it refuses only when neither resolves), so a health-only host with
// no delivery section must not fail its sweep on an empty binary name. A
// configured path that no longer resolves (removed, a directory, not
// executable: the same test install applies) is not used either: the sweep
// falls back to "orca" on PATH, so a stale host config costs one rename pass
// nothing instead of failing it every run (cli#2166).
func healthOrcaBin(cfg *hostpoller.Config) string {
	return healthsched.OrcaOrPath(cfg.Delivery.OrcaBin)
}

// healthLookPath pins a bare "orca" to one file for the whole sweep. It is a
// variable only so a test can fix the answer.
var healthLookPath = exec.LookPath

// healthSweepOrcaBin is the binary the sweep's Orca calls run: healthOrcaBin,
// with the bare fallback name resolved on PATH ONCE to an absolute path
// (cli#2166). A bare name is looked up again by every exec, so the renames and
// the version probe could land on different installations if one disappeared
// mid-pass; an absolute path makes every call ask the same file. A lookup that
// fails keeps the bare name, and the first call reports Orca unreachable as
// before.
func healthSweepOrcaBin(cfg *hostpoller.Config) string {
	bin := healthOrcaBin(cfg)
	if bin == "orca" {
		if p, err := healthLookPath(bin); err == nil && filepath.IsAbs(p) {
			return p
		}
	}
	return bin
}

// healthOrcaVersionRE is what a version looks like: a digit-led token of at
// most 64 characters ("1.4.247", "v1.4.247+build.5"). Anything else Orca
// prints (an error body, a banner, control bytes) is not a version.
var healthOrcaVersionRE = regexp.MustCompile(`^v?[0-9][0-9A-Za-z.+_-]*$`)

// healthOrcaVersionMax bounds the whole token, a leading "v" included.
const healthOrcaVersionMax = 64

// healthOrcaVersion is `orca --version` for the receipt (cli#2166): the first
// line when it is version-shaped, else nil (Orca did not answer, or answered
// something that is not a version). It is local output only: the platform's
// host and sweep records have fixed shapes (cor:agt:070:01, :09), so the value
// is never sent anywhere, and a failure here never fails the sweep. Terminal
// list and rename need Orca 1.4.232 or later.
//
// run is the runner the title pass used: the probe asks the very executable
// that did the renames, never a second resolution that could land on another
// installation.
func healthOrcaVersion(ctx context.Context, run orcaterm.Runner) *string {
	res := run(ctx, []string{"--version"})
	if res.Code != 0 {
		return nil
	}
	line, _, _ := strings.Cut(res.Stdout, "\n")
	line = strings.TrimSpace(line)
	if len(line) > healthOrcaVersionMax || !healthOrcaVersionRE.MatchString(line) {
		return nil
	}
	return &line
}

// healthTitles is the sweep's title pass (cli#2017): every tab whose
// worktree is bound to a worker registered on this host gets that worker's
// bare name as its title, when it differs. workers maps worker id to name.
//
// A tab is matched to its worker by the worktree's session binding, never by
// its current title or by name, so two same-named workers in two Apps cannot be
// mixed up and nothing is ever identified by what a tab says about itself. A
// tab with no registered worker (a plain shell, a stranger's worktree) is left
// alone. Panes of one tab are renamed once. With apply false nothing is
// renamed; the entries say what would be. A tab that fails to rename is
// reported in its entry and the others still go ahead; the returned error is
// an exit-1 refusal when the list failed or any rename did.
func healthTitles(ctx context.Context, cfg *hostpoller.Config, workers map[string]string, apply bool) ([]healthRename, error) {
	if len(workers) == 0 {
		return []healthRename{}, nil // nothing registered: no Orca call to fail
	}
	return healthTitlesWithRunner(ctx, orcaRunner(healthSweepOrcaBin(cfg)), workers, apply)
}

// healthTitlesWithRunner is healthTitles on an already-built runner, so the sweep
// resolves Orca once and the version probe shares the title pass's executable
// (cli#2166).
func healthTitlesWithRunner(ctx context.Context, run orcaterm.Runner, workers map[string]string, apply bool) ([]healthRename, error) {
	if len(workers) == 0 {
		return []healthRename{}, nil
	}
	terminals, err := orcaterm.NewClient(run).List(ctx)
	if err != nil {
		return []healthRename{}, exitcode.Newf(exitcode.Error, "tab titles were not checked: the terminal list failed")
	}
	out := []healthRename{}
	// doneTab: the tab is handled (renamed, or listed in a dry run), so its
	// other panes are skipped. A pane that fails leaves the tab open, so a
	// sibling pane with a working handle is still tried; failedTab holds the
	// tabs that no pane managed to rename.
	doneTab := map[string]bool{}
	failedTab := map[string]bool{}
	for _, t := range terminals {
		if t.WorktreePath == "" || t.Handle == "" {
			continue
		}
		id := healthBoundWorker(t.WorktreePath)
		name, ok := workers[id]
		if !ok {
			continue
		}
		want := capTerminalTitle(terminalTitleName(name))
		if t.Title == want {
			continue
		}
		tab := t.TabID
		if tab == "" {
			tab = t.Handle
		}
		if doneTab[tab] {
			continue
		}
		r := healthRename{Worker: id, Name: name, Terminal: t.Handle, From: t.Title, To: want}
		if apply {
			if err := orcaterm.RenameTab(ctx, run, t.Handle, want); err != nil {
				code := "rename_failed"
				r.Error = &code
				if oc := orcaterm.ErrorCode(err); oc != "" {
					r.OrcaCode = &oc
				}
				failedTab[tab] = true
			} else {
				r.Applied = true
				doneTab[tab] = true
				delete(failedTab, tab)
			}
		} else {
			doneTab[tab] = true
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(failedTab) > 0 {
		return out, exitcode.Newf(exitcode.Error, "some tab titles were not renamed")
	}
	return out, nil
}
