package hostpoller

import (
	"context"
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

func (e *AgentlessRefusal) Error() string { return "the server refused the agentless end: " + e.Code }
