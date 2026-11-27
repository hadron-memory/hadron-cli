package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// AgentAbsent is the closed reason hadron-server#4292 (S15) defines for the
// host's proof that no agent process runs in a short-lived worker's worktree.
// A worker.end FAILED report whose reason LEADS with it asks the server to end
// the session itself; endAgentlessSession is the same proof by mutation
// (hrn:node:hadronmemory.com:specs:cor:agt:050:10, Proposed).
const AgentAbsent = "AGENT_ABSENT"

// absenceProof is what the strict census says about one worker's worktrees.
// Absent is true only when EVERY worktree was censused completely and held no
// agent: an error, an unproven observation, an agent, or no worktree to look
// in never reads as absence (unknown is not zero, cli#2060).
type absenceProof struct {
	Absent bool
	// Found is true when the census itself saw an agent (as opposed to being
	// unable to say).
	Found bool
	// Detail is the census result, for the report's reason and the log.
	Detail string
}

// proveAgentAbsent runs the strict census over each worktree dir. Every dir
// must be proven empty; the first that is not decides, and says why.
func (p *Poller) proveAgentAbsent(ctx context.Context, probe func(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error), dirs []string) absenceProof {
	if len(dirs) == 0 {
		return absenceProof{Detail: "the strict census was not run: no worktree is registered for this worker on this host"}
	}
	if probe == nil {
		return absenceProof{Detail: "the strict census is not available on this host"}
	}
	skipped := 0
	for _, dir := range dirs {
		if dir == "" {
			// A platform host's token that did not resolve at this poll (hadron-cli#3237): a census of "" proves nothing.
			return absenceProof{Detail: "the strict census was not run: a worktree of this worker did not resolve to a directory on this host at this poll"}
		}
		obs, err := retryCensusProbe(ctx, probe, hostrelaunch.Target{Worktree: dir})
		switch {
		case err != nil:
			return absenceProof{Detail: fmt.Sprintf("the strict census could not prove no agent runs in %s: %v", dir, err)}
		case !obs.Proven:
			return absenceProof{Detail: fmt.Sprintf("the strict census did not prove no agent runs in %s", dir)}
		case len(obs.PIDs) > 0:
			pids := make([]string, len(obs.PIDs))
			for i, pid := range obs.PIDs {
				pids[i] = strconv.Itoa(pid)
			}
			return absenceProof{Found: true, Detail: fmt.Sprintf("the strict census found an agent process in %s (pid %s)", dir, strings.Join(pids, ", "))}
		}
		skipped += obs.ForeignUIDSkipped
	}
	detail := fmt.Sprintf("the strict census proved no agent process runs in %s", strings.Join(dirs, ", "))
	if skipped > 0 {
		detail += " (" + strconv.Itoa(skipped) + " foreign-uid process(es) excluded)"
	}
	return absenceProof{Absent: true, Detail: detail}
}

// AgentlessEnd names the session a host asks the server to end on its own proof
// that no agent runs (endAgentlessSession, hadron-server#4292).
type AgentlessEnd struct{ AppID, WorkerID, SessionID string }

// AgentlessEndResult is the server's answer: Ended is false when the session
// had already ended and nothing was written.
type AgentlessEndResult struct{ Ended bool }

// AgentlessRefusal is a server refusal of the end, by its closed code
// (HOST_DOES_NOT_RUN_WORKER, SESSION_NOT_WORKERS, WORKER_NOT_SHORT_LIVED,
// WORKER_HAS_OPEN_TASK).
type AgentlessRefusal struct{ Code string }

// AgentlessUnsupported is the code of a server that has no endAgentlessSession
// (it predates hadron-server#4292): a standing no, not a failure.
const AgentlessUnsupported = "END_UNSUPPORTED"

func (e *AgentlessRefusal) Error() string { return "the server refused the agentless end: " + e.Code }

// agentlessCandidate is the one row shape the sweep may end a session for: a
// worker of a SHORT_LIVED Agent with no open task (desired STOPPED, reason
// NO_OPEN_TASK) whose session is still open. Leave-alone and every stop
// reason carry another reason (or no desired state) and never match.
func agentlessCandidate(row RelaunchWorker) bool {
	return row.Desired == "STOPPED" && row.Reason == "NO_OPEN_TASK" && row.SessionID != ""
}

// The sweep's closed line codes for an agentless end, beside AgentAbsent.
const (
	agentlessPresent        = "AGENT_PRESENT"
	agentlessCensusUnproven = "CENSUS_UNPROVEN"
	agentlessStateChanged   = "STATE_CHANGED"
	agentlessAlreadyEnded   = "SESSION_ALREADY_ENDED"
	agentlessEndFailed      = "END_FAILED"
)

// sweepAgentlessEnd is Ask 3: the backstop that does not wait for a fresh
// worker.end. It returns the S10 line outcome and its closed reason code. It
// ends a session only on all of: the row is an agentless candidate; this
// host knows no live agent of its own launch; the strict census, over the
// worker's one worktree, is complete and empty; and a fresh desired state
// still reads the same worker, session, reason and APPLY. The server checks
// the rest again (a task opened meanwhile refuses WORKER_HAS_OPEN_TASK).
func (p *Poller) sweepAgentlessEnd(ctx context.Context, d *RelaunchDependencies, initial RelaunchSnapshot, row RelaunchWorker) (string, string) {
	finish := func(outcome, code, detail string) (string, string) {
		if p.emit != nil {
			p.emit(Event{Time: p.now().UTC(), Kind: "agentless-end", Type: "session.end", Outcome: outcome, Detail: detail, WorkerID: row.WorkerID, SessionID: row.SessionID, Reason: code})
		}
		return outcome, code
	}
	target, _, _, token, ok := p.relaunchTarget(initial, row)
	if !ok {
		return finish("SKIPPED", hostrelaunch.Unproven, "the worker's worktree could not be resolved on this host")
	}
	release, err := p.reserveWorktreeLaunch(token)
	if err != nil {
		return finish("SKIPPED", agentlessStateChanged, err.Error())
	}
	defer release()
	p.mu.Lock()
	launched := p.agents[row.WorkerID]
	p.mu.Unlock()
	if launched != nil {
		select {
		case <-launched.Done():
		default:
			return finish("SKIPPED", agentlessPresent, "an agent this host launched for the worker is still running")
		}
	}
	proof := p.proveAgentAbsent(ctx, d.Probe, []string{target.Worktree})
	if !proof.Absent {
		if proof.Found {
			return finish("SKIPPED", agentlessPresent, proof.Detail)
		}
		return finish("SKIPPED", agentlessCensusUnproven, proof.Detail)
	}
	// The end's fresh read is about the one worker it ends (cli#2816): a read
	// that fails or names no row is a changed state, never an end.
	fresh, err := d.Source.RelaunchWorker(ctx, initial.HostID, row.WorkerID)
	if err != nil || fresh.AppID != initial.AppID || fresh.HostID != initial.HostID || fresh.SweepMode != "APPLY" {
		return finish("SKIPPED", agentlessStateChanged, "the desired state could not be re-read as APPLY for this App and host")
	}
	// The proof is for ONE worktree. The fresh row must still be the same
	// candidate and must still resolve to that same worktree (a changed, added
	// or dropped registration voids it), so the end rests on the tree censused.
	same := false
	for _, w := range fresh.Workers {
		if w.WorkerID != row.WorkerID {
			continue
		}
		next, _, _, _, valid := p.relaunchTarget(fresh, w)
		same = agentlessCandidate(w) && w.SessionID == row.SessionID && valid && next.Worktree == target.Worktree
	}
	if !same {
		return finish("SKIPPED", agentlessStateChanged, "the worker's desired state or session changed since the census")
	}
	res, err := d.EndAgentless(ctx, AgentlessEnd{AppID: initial.AppID, WorkerID: row.WorkerID, SessionID: row.SessionID})
	if err != nil {
		var refused *AgentlessRefusal
		if errors.As(err, &refused) {
			// The server's closed no is a state it can see, not a failure of
			// this host, and it recurs every pass: skipped, so a standing
			// refusal writes no S10 record.
			return finish("SKIPPED", boundedSweepCode(refused.Code), err.Error())
		}
		return finish("FAILED", agentlessEndFailed, err.Error())
	}
	if !res.Ended {
		return finish("SKIPPED", agentlessAlreadyEnded, "the session had already ended; nothing was written")
	}
	return finish("APPLIED", AgentAbsent, "asked the server to end the session on its census proof: "+proof.Detail)
}
