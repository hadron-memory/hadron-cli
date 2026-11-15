package team

import (
	"context"
	"sort"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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
}

// healthOrcaBin is the orca the title pass runs: the host configuration's
// delivery.orca_bin, else "orca" found on PATH. `team health install` accepts
// either (it refuses only when neither resolves), so a health-only host with
// no delivery section must not fail its sweep on an empty binary name.
func healthOrcaBin(cfg *hostpoller.Config) string {
	if cfg.Delivery.OrcaBin != "" {
		return cfg.Delivery.OrcaBin
	}
	return "orca"
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
	run := orcaRunner(healthOrcaBin(cfg))
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
			if orcaterm.RenameTab(ctx, run, t.Handle, want) != nil {
				code := "rename_failed"
				r.Error = &code
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
