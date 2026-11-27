package hostpoller

import (
	"context"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// cli#2161 (C12): a context reset relaunches at once. `team session
// reset-context` leaves a stamp beside the binding and ends its own agent.
// The poller is told by the kernel (the agent's Done, a pidfd for an Orca
// agent) and runs C5's own Runner for that one worker with an exit proof in
// place of the two-sweep absence rule. There is no IPC and no second launch
// policy: the guards, the lock, the reservation, the cap, the go-gate and the
// /proc proof are C5's, unchanged. Anything that does not prove the exit
// leaves the worker to the ordinary sweep.

// relaunchStampTTL bounds how old a stamp may be when its agent's exit is
// seen: the command ends the agent right after writing it.
const relaunchStampTTL = 2 * time.Minute

// watchForExit records the exit of an agent this poller launched and wakes
// the run loop. Only while relaunch is enabled.
func (p *Poller) watchForExit(workerID string, a Agent) {
	if p.exitWake == nil {
		return
	}
	go func() {
		<-a.Done()
		p.mu.Lock()
		if p.exited == nil {
			p.exited = map[string]Agent{}
		}
		p.exited[workerID] = a
		p.mu.Unlock()
		p.wakeForExit()
	}()
}

func (p *Poller) wakeForExit() {
	select {
	case p.exitWake <- struct{}{}:
	default: // a wake is already pending
	}
}

func (p *Poller) exitsPending() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.exited) > 0
}

// relaunchExits runs one exit pass per recorded exit.
func (p *Poller) relaunchExits(ctx context.Context) {
	p.mu.Lock()
	pending := p.exited
	p.exited = nil
	p.mu.Unlock()
	for id, a := range pending {
		if ctx.Err() != nil {
			return
		}
		p.relaunchExit(ctx, id, a)
	}
}

func (p *Poller) relaunchExit(ctx context.Context, workerID string, old Agent) {
	if p.afterExitPass != nil {
		defer p.afterExitPass(workerID)
	}
	d := p.relaunch
	p.mu.Lock()
	current, token, ref := p.agents[workerID], p.agentTrees[workerID], p.workerRefs[workerID]
	p.mu.Unlock()
	w, configured := p.config().Worker(ref)
	// Only the exit of the agent this poller tracks now, of an Orca worker,
	// beside a stamp. An ordinary exit costs no network call.
	if current != old || token == "" || !configured || !w.launchesViaOrca() || p.config().WorktreePath(token) == "" || !delivery.HasRelaunchStamp(p.config().WorktreePath(token)) {
		return
	}
	hostID := p.state.RegisteredHostID()
	// The pass reads only the exited worker (cli#2816): the host-wide list is
	// the sweep's discovery read, not this pass's. A read that fails is
	// unproven and leaves the worker to the sweep; a read that names no row is
	// the selector's own answer — the platform does not show the worker as
	// desired here — and ends the pass silently.
	snap, err := d.Source.RelaunchWorker(ctx, hostID, workerID)
	if err != nil || snap.HostID != hostID || snap.AppID == "" || snap.Server == "" {
		p.log("relaunch-unproven", "", "worker.relaunch", "skipped", hostrelaunch.Unproven)
		return
	}
	if snap.SweepMode != "APPLY" {
		return
	}
	var row *RelaunchWorker
	for i := range snap.Workers {
		if snap.Workers[i].WorkerID == workerID {
			row = &snap.Workers[i]
		}
	}
	if row == nil {
		return // not a worker the platform wants running
	}
	journal, generation, closeJournal, err := d.OpenExitJournal()
	if err != nil || journal == nil || generation == 0 || closeJournal == nil {
		p.log("relaunch-unproven", "", "worker.relaunch", "skipped", hostrelaunch.Unproven)
		return
	}
	defer closeJournal()
	if err := d.Ready(ctx); err != nil {
		p.log("relaunch-unproven", "", "worker.relaunch", "skipped", hostrelaunch.Unproven)
		return
	}
	if p.relaunchEpoch == "" {
		epoch, e := newRelaunchEpoch()
		if e != nil {
			p.log("relaunch-unproven", "", "worker.relaunch", "skipped", hostrelaunch.Unproven)
			return
		}
		p.relaunchEpoch = epoch
	}
	var checkpoint string
	runner, target, _, ok := p.relaunchRunner(d, snap, *row, journal, generation, &checkpoint)
	if !ok {
		return
	}
	// Asked by the Runner under the binding lock, after every guard and the
	// cap: the agent this poller launched is gone and is still the one it
	// tracks, and the stamp names this exact worker and session, fresh. The
	// stamp is used up by the reading.
	runner.ExitProof = func(_ context.Context, t hostrelaunch.Target) (bool, error) {
		select {
		case <-old.Done():
		default:
			return false, nil
		}
		p.mu.Lock()
		same := p.agents[t.WorkerID] == old
		p.mu.Unlock()
		if !same {
			return false, nil
		}
		cp, proven := delivery.ConsumeRelaunchStamp(t.Worktree, t.WorkerID, t.SessionID, p.now(), relaunchStampTTL)
		checkpoint = cp
		return proven, nil
	}
	started := p.now()
	result, _ := runner.Execute(ctx, target) // closed result/reason is emitted by Log
	// C1's S10 rule (#2056, #109567): a recovery pass is recorded only when it
	// applied or failed. An exit pass is one worker's pass: a relaunch (or a
	// failed one) is never left unrecorded, and a refused or unproven exit,
	// which the sweep still owns, records no empty row.
	if d.ReportSweep != nil {
		report := &SweepReport{AppID: snap.AppID, HostID: hostID, Mode: snap.SweepMode, StartedAt: started, Lines: []SweepReportLine{}}
		report.add(workerID, strings.ToUpper(result.Status), result.Reason)
		if report.Applied+report.Failed > 0 {
			if id, e := newRelaunchEpoch(); e == nil {
				report.RunID = "recovery:" + id
				p.sendRecoveryReport(ctx, d, report)
			} else {
				p.log("sweep-record-failed", "", "worker.relaunch", "", "RECORD_FAILED")
			}
		}
	}
}
