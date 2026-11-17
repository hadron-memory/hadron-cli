package hostpoller

import (
	"context"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// cleanupRelaunchTerminals runs only after a successful C5 launch, under its
// admission lock. The receipts are local: cleanup never changes the launch's
// outcome, retries a launch, or sends another server event.
func (p *Poller) cleanupRelaunchTerminals(ctx context.Context, agent Agent, target hostrelaunch.Target) {
	p.cleanupRelaunchTerminalsWith(ctx, agent, target, probeRelaunchWorktree, verifyRelaunchProcess)
}

// The seams drive the same closure and terminal client production uses, rather
// than replacing the final safety decision with a boolean in tests.
func (p *Poller) cleanupRelaunchTerminalsWith(ctx context.Context, agent Agent, target hostrelaunch.Target,
	probe func(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error),
	verify func(context.Context, Agent, hostrelaunch.Target, hostrelaunch.Observation) error) {
	a, ok := agent.(*OrcaAgent)
	if !ok || a.l.Run == nil {
		p.cleanupReceipt(target, "", "skipped", "CLEANUP_UNAVAILABLE")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := relaunchTerminalClientFor(a.l.Run)
	cleanupRelaunchTabs(ctx, client, a.handle, target.Worktree, func(ctx context.Context) bool {
		proof, err := probe(ctx, target)
		// Reuse the pinned-process check. A singleton is deliberately stricter
		// than inferring which tab an unassociated child process belongs to.
		return err == nil && proof.Proven && verify(ctx, a, target, proof) == nil
	}, func(handle, outcome, reason string) {
		p.cleanupReceipt(target, handle, outcome, reason)
	})
}

// afterRelaunchCleanup is best-effort housekeeping after the outcome was
// reported. Reacquire C3/C5 admission and binding authority; a notice may have
// taken time, or another action may have changed the target meanwhile.
func (p *Poller) afterRelaunchCleanup(ctx context.Context, d *RelaunchDependencies, token string, target hostrelaunch.Target, owned hostrelaunch.Agent) {
	report := func(reason string) { p.cleanupReceipt(target, "", "skipped", reason) }
	defer func() {
		if recover() != nil {
			report("CLEANUP_PANIC")
		}
	}()
	if d.Cleanup == nil {
		report("CLEANUP_DISABLED")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	a, ok := owned.(*relaunchOwnedAgent)
	if !ok || a.agent == nil || ctx.Err() != nil {
		report("CLEANUP_UNAVAILABLE")
		return
	}
	release, err := p.reserveWorktreeLaunch(token)
	if err != nil {
		report("CLEANUP_ADMISSION_UNAVAILABLE")
		return
	}
	defer release()
	unlock, err := delivery.LockBinding(target.Worktree, time.Second)
	if err != nil {
		report("CLEANUP_BINDING_UNPROVEN")
		return
	}
	defer unlock()
	b, err := d.Binding(target)
	if err != nil || !b.Usable || b.AppID != target.AppID || b.WorkerID != target.WorkerID || b.SessionID != target.SessionID || b.Server != target.Server {
		report("CLEANUP_BINDING_UNPROVEN")
		return
	}
	snap, err := d.Source.RelaunchSnapshot(ctx, target.HostID)
	current := false
	if err == nil && snap.SweepMode == "APPLY" {
		for _, row := range snap.Workers {
			if row.WorkerID != target.WorkerID || row.Desired != "RUNNING" || (row.Reason != "LONG_LIVED" && row.Reason != "HOLDS_TASK") {
				continue
			}
			t, _, _, _, valid := p.relaunchTarget(snap, row)
			current = valid && t == target
		}
	}
	if !current || ctx.Err() != nil {
		report("CLEANUP_TARGET_UNPROVEN")
		return
	}
	d.Cleanup(ctx, a.agent, target)
}

func (p *Poller) cleanupReceipt(target hostrelaunch.Target, handle, outcome, reason string) {
	if p.emit != nil {
		p.emit(Event{Time: p.now().UTC(), Kind: "relaunch-terminal-cleanup", Type: "worker.relaunch", Outcome: outcome, Reason: reason, Detail: reason, WorkerID: target.WorkerID, SessionID: target.SessionID, TerminalHandle: handle})
	}
}

// A failed subprocess can leave a complete-looking JSON reply on stdout.
// The terminal client parses envelopes, so invalidate that reply here first.
func relaunchTerminalClientFor(run OrcaRun) *orcaterm.Client {
	return orcaterm.NewSweepClient(func(ctx context.Context, argv []string) orcaterm.Result {
		code, out, _ := run(ctx, argv)
		if code != 0 || ctx.Err() != nil {
			return orcaterm.Result{Code: 1}
		}
		return orcaterm.Result{Stdout: out}
	})
}

type relaunchTerminalClient interface {
	List(context.Context) ([]orcaterm.Terminal, error)
	CloseTab(context.Context, string) error
}

// Orca has no terminal→PID association. A tab is eligible only when every
// pane is disconnected, unidentified, in this worktree and not tracked.
// A fresh census must prove the pinned relaunched process is the ONLY agent;
// disconnected alone is never sufficient. A connected shell could hold a
// non-agent build, ssh or editor and is never closed (#2193 ruling #110846).
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
		if term.Connected {
			receipt(term.Handle, "skipped", "CONNECTED_TERMINAL")
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
		if runtime.GOOS != "linux" || ctx.Err() != nil || proof == nil || !proof(ctx) || ctx.Err() != nil {
			receipt(term.Handle, "skipped", "CENSUS_UNPROVEN")
			continue
		}
		fresh, err := client.List(ctx)
		if err != nil {
			receipt(term.Handle, "skipped", "TERMINAL_LIST_UNAVAILABLE")
			continue
		}
		if !emptyRelaunchTab(fresh, term.Handle, term.TabID, tracked, worktree) {
			receipt(term.Handle, "skipped", "TAB_UNPROVEN")
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
		if term.Handle == tracked || term.Identity() != "" || term.Connected || filepath.Clean(term.WorktreePath) != filepath.Clean(worktree) {
			return false
		}
	}
	return found && trackedFound && tracked != ""
}
