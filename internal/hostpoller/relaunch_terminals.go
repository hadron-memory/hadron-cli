package hostpoller

import (
	"context"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// cleanupRelaunchTerminals runs only after a successful C5 launch, under its
// admission lock. The receipts are local: cleanup never changes the launch's
// outcome, retries a launch, or sends another server event.
func (p *Poller) cleanupRelaunchTerminals(ctx context.Context, agent Agent, target hostrelaunch.Target) {
	a, ok := agent.(*OrcaAgent)
	if !ok || a.l.Run == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := orcaterm.NewSweepClient(func(ctx context.Context, argv []string) orcaterm.Result {
		code, out, errOut := a.l.Run(ctx, argv)
		return orcaterm.Result{Code: code, Stdout: out, Stderr: errOut}
	})
	cleanupRelaunchTabs(ctx, client, a.handle, target.Worktree, func(ctx context.Context) bool {
		proof, err := probeRelaunchWorktree(ctx, target)
		// Reuse the pinned-process check. A singleton is deliberately stricter
		// than inferring which tab an unassociated child process belongs to.
		return err == nil && proof.Proven && verifyRelaunchProcess(ctx, a, target, proof) == nil
	}, func(handle, outcome, reason string) {
		if p.emit != nil {
			p.emit(Event{Time: p.now().UTC(), Kind: "relaunch-terminal-cleanup", Type: "worker.relaunch", Outcome: outcome, Reason: reason, Detail: reason, WorkerID: target.WorkerID, SessionID: target.SessionID, TerminalHandle: handle})
		}
	})
}

type relaunchTerminalClient interface {
	List(context.Context) ([]orcaterm.Terminal, error)
	CloseTab(context.Context, string) error
}

// Orca has no terminal→PID association. A tab is eligible only when every
// pane is unidentified, belongs to this worktree and is not tracked. Re-list
// immediately before each close, then require a fresh complete census proving
// that the pinned relaunched process is the ONLY agent. Connected bare shells
// are allowed by that stronger proof; disconnected alone is never sufficient.
func cleanupRelaunchTabs(ctx context.Context, client relaunchTerminalClient, tracked, worktree string, proof func(context.Context) bool, receipt func(string, string, string)) {
	terms, err := client.List(ctx)
	if err != nil {
		receipt("", "skipped", "TERMINAL_LIST_UNAVAILABLE")
		return
	}
	seen := map[string]bool{}
	for _, term := range terms {
		if filepath.Clean(term.WorktreePath) != filepath.Clean(worktree) {
			continue
		}
		if term.Handle == tracked {
			receipt(term.Handle, "skipped", "TRACKED_TERMINAL")
			continue
		}
		if term.Identity() != "" {
			receipt(term.Handle, "skipped", "IDENTIFIED_TERMINAL")
			continue
		}
		if term.TabID == "" || term.Handle == "" {
			receipt(term.Handle, "skipped", "TAB_UNPROVEN")
			continue
		}
		if seen[term.TabID] {
			continue
		}
		seen[term.TabID] = true
		fresh, err := client.List(ctx)
		if err != nil {
			receipt(term.Handle, "skipped", "TERMINAL_LIST_UNAVAILABLE")
			continue
		}
		if !emptyRelaunchTab(fresh, term.Handle, term.TabID, tracked, worktree) {
			receipt(term.Handle, "skipped", "TAB_UNPROVEN")
			continue
		}
		if runtime.GOOS != "linux" || ctx.Err() != nil || proof == nil || !proof(ctx) || ctx.Err() != nil {
			receipt(term.Handle, "skipped", "CENSUS_UNPROVEN")
			continue
		}
		if err := client.CloseTab(ctx, term.Handle); err != nil {
			receipt(term.Handle, "failed", "TERMINAL_CLOSE_FAILED")
			continue
		}
		receipt(term.Handle, "applied", "DEAD_TAB_CLOSED")
	}
}

func emptyRelaunchTab(terms []orcaterm.Terminal, handle, tab, tracked, worktree string) bool {
	found := false
	trackedFound := false
	handles := map[string]bool{}
	for _, term := range terms {
		if term.Handle == "" || handles[term.Handle] {
			return false
		}
		handles[term.Handle] = true
		if term.Handle == tracked {
			if term.TabID == "" || term.TabID == tab || filepath.Clean(term.WorktreePath) != filepath.Clean(worktree) {
				return false
			}
			trackedFound = true
		}
		if term.TabID != tab {
			continue
		}
		if term.Handle == handle {
			found = true
		}
		if term.Handle == tracked || term.Identity() != "" || filepath.Clean(term.WorktreePath) != filepath.Clean(worktree) {
			return false
		}
	}
	return found && trackedFound && tracked != ""
}
