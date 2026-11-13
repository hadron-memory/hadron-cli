package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"time"
)

// worktree.create: a new worktree for a worker, from a configured repo. Its
// name token is <repo's last segment>/<label>. A re-delivered operation whose
// tree is already there on the requested branch succeeds again.
func (p *Poller) worktreeCreate(ctx context.Context, op Operation) {
	ref, _, ok := p.workerRef(op)
	if !ok {
		p.finish(ctx, op, Failed, "", "the worker is not configured on this host")
		return
	}
	repo, base, branch, label := argString(op, "repo"), argString(op, "baseBranch"), argString(op, "branchName"), argString(op, "label")
	repoPath, ok := p.cfg.Repos[repo]
	if !ok {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("repo %q is not configured on this host", repo))
		return
	}
	token := path.Base(repo) + "/" + label
	if !ValidNameToken(token) {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("worktree name %q is not a valid name token", token))
		return
	}
	if owner, ok := p.state.ownerOf(token, p.cfg); ok && owner != ref {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("%s is registered for another worker here", token))
		return
	}
	dir := p.cfg.WorktreePath(token)
	st, err := p.host.Inspect(ctx, dir)
	if err != nil {
		p.finish(ctx, op, Failed, "", "reading the worktree path: "+err.Error())
		return
	}
	if st.Exists {
		if !st.IsWorktree || st.Branch != branch {
			p.finish(ctx, op, Failed, "", fmt.Sprintf("%s already exists and is not a worktree on %s", token, branch))
			return
		}
	} else if err := p.host.CreateWorktree(ctx, repoPath, dir, branch, base); err != nil {
		p.finish(ctx, op, Failed, "", "git worktree add failed: "+err.Error())
		return
	}
	if err := p.state.addCreated(ref, token); err != nil {
		p.log("state-failed", op.ID, op.Type, "", err.Error())
	}
	p.finish(ctx, op, Succeeded, token, "")
}

// worktree.remove: only a tree with nothing to lose. Uncommitted changes, a
// branch with no upstream, commits not pushed, a running agent, or an
// earlier failed worker.end all refuse, with the reason reported.
func (p *Poller) worktreeRemove(ctx context.Context, op Operation) {
	token := argString(op, "worktree")
	if !ValidNameToken(token) {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("worktree %q is not a valid name token", token))
		return
	}
	dir := p.cfg.WorktreePath(token)
	st, err := p.host.Inspect(ctx, dir)
	if err != nil {
		p.finish(ctx, op, Failed, "", "reading the worktree: "+err.Error())
		return
	}
	// An absent tree is already removed, and removing nothing has no side
	// effect: SUCCEEDED, before any check that needs the tree or its
	// registration. A remove whose success report was lost comes back after
	// the tree and its registration are gone, and must not turn into FAILED.
	if !st.Exists {
		_ = p.state.removeCreated(token)
		p.finish(ctx, op, Succeeded, "", "")
		return
	}
	// Only the operation's own worker's tree: never another worker's, and
	// never a tree under the root this host doesn't register for it.
	ref, w, ok := p.workerRef(op)
	if !ok {
		p.finish(ctx, op, Failed, "", "the worker is not configured on this host")
		return
	}
	if !contains(p.state.worktreesFor(ref, w.Worktrees), token) {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("worktree %q is not registered for this worker here", token))
		return
	}
	if why, kept := p.state.keepReason(token); kept {
		p.finish(ctx, op, Failed, "", "kept: "+why)
		return
	}
	if p.agentRunningIn(token) {
		p.finish(ctx, op, Failed, "", "an agent this host launched is still running in it")
		return
	}
	// Any process working inside the tree refuses too: an agent launched
	// before this poller existed, a human's shell. Fails closed on a read
	// error.
	if who, err := p.host.Occupied(ctx, dir); err != nil {
		p.finish(ctx, op, Failed, "", "could not tell whether a process is working in it: "+err.Error())
		return
	} else if who != "" {
		p.finish(ctx, op, Failed, "", "a process is working in it ("+who+")")
		return
	}
	switch {
	case !st.IsWorktree:
		p.finish(ctx, op, Failed, "", "not a git worktree")
		return
	case st.Dirty:
		p.finish(ctx, op, Failed, "", "it has uncommitted or untracked changes")
		return
	case st.Upstream == "":
		p.finish(ctx, op, Failed, "", fmt.Sprintf("branch %s has no upstream, so its commits are not pushed", st.Branch))
		return
	case st.Ahead > 0:
		p.finish(ctx, op, Failed, "", fmt.Sprintf("%d commit(s) on %s are not pushed to %s", st.Ahead, st.Branch, st.Upstream))
		return
	}
	if err := p.host.RemoveWorktree(ctx, dir); err != nil {
		p.finish(ctx, op, Failed, "", "git worktree remove failed: "+err.Error())
		return
	}
	_ = p.state.removeCreated(token)
	p.finish(ctx, op, Succeeded, "", "")
}

// postDeadlineUnclearBudget is how many unclear Report answers a start still
// tolerates once the launch grant's deadline has passed (#1474 delta review
// P2-2): an unclear answer still decides nothing — nothing is stopped,
// forgotten or reported on it — but the asking itself ends, so a server that
// never answers clearly cannot hold the worker's dispatch goroutine forever.
const postDeadlineUnclearBudget = 3

// worker.start: launch the configured agent in a registered worktree and
// hand it the grant, then report SUCCEEDED once the server sees its session
// bound, within the grant's lifetime.
func (p *Poller) workerStart(ctx context.Context, op Operation) {
	ref, w, ok := p.workerRef(op)
	if !ok {
		p.finish(ctx, op, Failed, "", "the worker is not configured on this host")
		return
	}
	// A start collected before a restart may already be bound: ask first,
	// so it is never launched twice.
	// Only NOT_DONE is evidence the start is unbound; launch on nothing
	// else. A finished operation stops here, and any other failure leaves
	// it collected for the next collect to retry.
	switch _, err := p.server.Report(ctx, op.ID, Succeeded, "", ""); {
	case err == nil:
		p.log("reported", op.ID, op.Type, Succeeded, "its session was already bound")
		return
	case errors.Is(err, ErrFinal):
		p.log("reported", op.ID, op.Type, "", "the server already finished it: "+err.Error())
		return
	case !errors.Is(err, ErrNotDone):
		p.log("report-failed", op.ID, op.Type, Succeeded, err.Error()+"; not launching on an unclear answer")
		return
	}
	// Compared as the server's canonical identity on BOTH sides (cli#971): a
	// provider alias and its family are one family; a genuine mismatch, an
	// empty family or a malformed one still launches nothing.
	if fam := argString(op, "family"); !SameFamily(fam, w.Family) {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("this host runs the worker as %q, not %q", w.Family, fam))
		return
	}
	token := argString(op, "worktree")
	if !contains(p.state.worktreesFor(ref, w.Worktrees), token) {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("worktree %q is not registered for this worker here", token))
		return
	}
	dir := p.cfg.WorktreePath(token)
	if st, err := p.host.Inspect(ctx, dir); err != nil || !st.Exists {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("worktree %q does not exist on this host", token))
		return
	}
	if op.LaunchGrant == "" {
		p.finish(ctx, op, Failed, "", "the collected start carried no launch grant")
		return
	}
	// One agent per worktree (#472). This sees only agents THIS poller
	// launched: after a restart, an agent still booting from an earlier
	// launch is invisible here, and its revoked grant fails its own bind.
	if p.agentRunningIn(token) {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("an agent this host launched is still running in %s", token))
		return
	}
	// Any process working in the tree refuses the start too — an agent an
	// older wrapper runs in a terminal there, a human's shell (#1474). The
	// same /proc scan worktree.remove uses, which is what sees a process
	// this poller has no record of; it names the pid and a bounded, redacted
	// argv, and fails closed when it cannot tell. Best-effort by nature: a
	// process can start between this scan and the launch, and the grant's
	// lifetime plus the recorded-agent guard bound what that window costs.
	if who, err := p.host.Occupied(ctx, dir); err != nil {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("could not tell whether a process is working in %s: %s; nothing was launched", token, err.Error()))
		return
	} else if who != "" {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("a process is already working in %s (%s); nothing was launched", token, who))
		return
	}
	deadline := p.now().Add(LaunchGrantTTL)
	agent, err := p.host.Launch(ctx, LaunchSpec{
		Argv: w.Launch,
		Dir:  dir,
		Env:  agentEnv(os.Environ(), p.key, launchGrantEnvVar+"="+op.LaunchGrant),
	})
	if err != nil {
		p.finish(ctx, op, Failed, "", "launching the agent failed: "+err.Error())
		return
	}
	p.trackAgent(op.WorkerID, token, agent)
	p.log("launched", op.ID, op.Type, "", fmt.Sprintf("worker %s in %s", ref, token))
	// Unclear answers past the grant's deadline are tolerated only a bounded
	// number of times (#1474 delta review P2-2): postDeadlineUnclearBudget.
	postDeadline := 0
	for {
		select {
		case <-agent.Done():
			// The agent is gone; the ask below still decides what happened
			// — an agent that bound and then exited is a start that
			// succeeded, and one that never bound still leaves a group to
			// stop.
		default:
			// Still running: wait out the bind poll before the next ask.
			if err := p.sleep(ctx, p.BindPoll); err != nil {
				return
			}
		}
		// Both decision predicates are sampled BEFORE the ask (Jane F2,
		// #1474 delta review): a NOT_DONE answer only decides an event —
		// the agent's exit, the grant's deadline — that had already
		// happened when the ask was issued, because the server's answer is
		// never older than the ask. An event landing while the answer is
		// in flight leaves that answer stale (the server may not have seen
		// the event yet); the loop asks again, and the next answer —
		// issued after the event — is authoritative.
		askExpired := exited(agent)
		askPastDeadline := !p.now().Before(deadline)
		_, err := p.server.Report(ctx, op.ID, Succeeded, "", "")
		switch {
		case err == nil:
			// The server recorded the bind: the agent IS the start, and
			// nothing about it is stopped or forgotten.
			if exited(agent) {
				p.log("reported", op.ID, op.Type, Succeeded, "its session bound before the agent exited")
			} else {
				p.log("reported", op.ID, op.Type, Succeeded, "its session bound")
			}
			return
		case errors.Is(err, ErrFinal):
			p.alreadyFinished(op, agent, err)
			return
		case errors.Is(err, ErrNotDone):
			if askExpired {
				// The agent was already gone when the ask was issued, and
				// the server — answering at or after that moment — says
				// nothing bound: an authoritative failure. Nothing is left
				// to signal, but the group it leaves behind is stopped and
				// the record of it must not outlive the report.
				p.stopLaunchedAgent(op, agent)
				p.finish(ctx, op, Failed, "", "the agent exited before binding a session ("+agent.ExitDetail()+"); the launch command must stay in the foreground for the agent's life")
				return
			}
			if askPastDeadline {
				// The grant was already spent when the ask was issued, and
				// the server — answering at or after that moment, never on
				// a NOT_DONE sampled before the deadline — says nothing
				// bound: an authoritative failure. Stop what was launched
				// (a wrapper that keeps relaunching its agent must not
				// outlive the failure) and forget it, then report.
				p.stopLaunchedAgent(op, agent)
				p.finish(ctx, op, Failed, "", fmt.Sprintf("no session was bound within %s, the launch grant's lifetime", LaunchGrantTTL))
				return
			}
		default:
			// An unclear answer stops and forgets nothing: a bind may have
			// landed while the answer was in flight. Ask again next pass,
			// paced when the agent is gone (the loop's own sleep only runs
			// while it lives) — but only a bounded number of times once the
			// grant's deadline has passed: the asking itself then ends,
			// with every record kept and nothing signalled, so a server
			// that never answers clearly cannot hold this worker's
			// dispatch goroutine forever (#1474 delta review P2-2). The
			// operation stays collected server-side and is re-delivered;
			// the still-recorded agent refuses a re-launch until it is
			// resolved.
			p.log("report-failed", op.ID, op.Type, Succeeded, err.Error())
			if askPastDeadline {
				postDeadline++
				if postDeadline > postDeadlineUnclearBudget {
					p.log("start-stuck", op.ID, op.Type, "", "giving up asking for this start: unclear answers persisted past the launch grant's lifetime; the agent is left running and every record is kept")
					return
				}
			}
			if askExpired {
				if err := p.sleep(ctx, p.BindPoll); err != nil {
					return
				}
			}
		}
	}
}

// exited reports whether a launched agent's process has already exited.
func exited(agent Agent) bool {
	select {
	case <-agent.Done():
		return true
	default:
		return false
	}
}

// alreadyFinished acts on an ALREADY_REPORTED answer after the launch: the
// refusal says what the server recorded, and only a recorded FAILED stops
// what was launched — a recorded SUCCEEDED means the agent IS the bound
// start, and a refusal naming nothing stops nothing either: never a kill on
// an inference (#1474 review P2-3). The server handler pins the contract:
// the same outcome again is answered with the operation itself (so a lost
// SUCCEEDED response retried is not a refusal), a different one refused
// carrying what it recorded (reportHostOperation).
func (p *Poller) alreadyFinished(op Operation, agent Agent, err error) {
	recorded := ""
	var fin *FinalError
	if errors.As(err, &fin) {
		recorded = fin.Recorded
	}
	switch recorded {
	case Failed:
		p.stopLaunchedAgent(op, agent)
		p.log("reported", op.ID, op.Type, "", "the server recorded it failed: "+err.Error())
	case Succeeded:
		p.log("reported", op.ID, op.Type, "", "the server recorded it succeeded; what was launched was left running: "+err.Error())
	default:
		// Unnamed outcome: what was launched is left running, kept on every
		// record. Nothing bounds it automatically after this return — the
		// grant's deadline path governs only the ask loop that already
		// ended — so this log line is the operator's notice: the agent runs
		// until someone ends it (#1474 delta review P3).
		p.log("reported", op.ID, op.Type, "", "the server already finished it without naming the outcome; what was launched was left running and stays recorded until ended by hand: "+err.Error())
	}
}

// stopLaunchedAgent cleans up after a start that failed: stop the process
// group the poller launched, and — only once the stop is CONFIRMED (the
// whole group observed gone) — forget the agent, in memory and in the state
// file, so nothing of the failed launch is left running or recorded (#1474).
// An unconfirmed stop keeps every record: the "an agent this host launched
// is still running" guard must not lose a process it could not prove dead.
// The stop happens on the worker's own dispatch goroutine (dispatch, behind
// its tail), so its bounded waits never stall the poller loop or another
// worker's operations.
func (p *Poller) stopLaunchedAgent(op Operation, agent Agent) {
	stopped, detail := agent.Terminate()
	if detail != "" {
		p.log("stopped", op.ID, op.Type, "", detail)
	}
	if !stopped {
		p.log("stop-unconfirmed", op.ID, op.Type, "", "the launched agent could not be confirmed stopped; its records are kept")
		return
	}
	p.mu.Lock()
	delete(p.agents, op.WorkerID)
	delete(p.agentTrees, op.WorkerID)
	delete(p.agentIDs, op.WorkerID)
	p.mu.Unlock()
	// The confirmed stop ends the launch's hold on its group number too
	// (it reaps the leader it had kept unreaped); an unconfirmed one
	// returned above and keeps both the records and the hold (#1474 delta
	// round 3, Ada #102351).
	if err := p.state.dropAgent(op.WorkerID); err != nil {
		// Logged, not fatal: the entry can then survive a restart in the
		// state file — a start is refused while its pid reads Alive on it,
		// which an operator resolves from this line (a dead pid stops
		// reading Alive on its own).
		p.log("state-failed", op.ID, op.Type, "", err.Error())
	}
}

const launchGrantEnvVar = "HADRON_LAUNCH_GRANT"

// worker.end: prompt the worker to end its own session with a handoff, wait
// a bounded time, and report. It reports FAILED, never SUCCEEDED: the server
// records a FAILED on a session that did end as the success it is, with
// whether a handoff was written, while a SUCCEEDED would end a still-running
// session itself — the kill this operation must never be (cor:agt:020:10).
func (p *Poller) workerEnd(ctx context.Context, op Operation) {
	ref, w, ok := p.workerRef(op)
	if !ok {
		p.finish(ctx, op, Failed, "", "the worker is not configured on this host")
		return
	}
	session := argString(op, "sessionId")
	p.mu.Lock()
	agent := p.agents[op.WorkerID]
	p.mu.Unlock()
	if agent != nil {
		select {
		case <-agent.Done():
			// Exited long before: it says nothing about the session this
			// operation names, which may be a later, manual bind.
			agent = nil
		default:
		}
	}
	note := ""
	if len(w.End) > 0 {
		dir := ""
		if t, ok := p.agentWorktree(op.WorkerID); ok {
			dir = p.cfg.WorktreePath(t)
		}
		env := agentEnv(os.Environ(), p.key, "HADRON_SESSION_ID="+session, "HADRON_WORKER_ID="+op.WorkerID)
		if err := p.host.Prompt(ctx, w.End, dir, env); err != nil {
			note = "; the end prompt failed: " + err.Error()
		}
	} else {
		note = "; no end prompt is configured for this worker"
	}
	wait := w.endWait()
	reason := fmt.Sprintf("the session did not end within %s%s", wait, note)
	var done <-chan struct{}
	if agent != nil {
		done = agent.Done()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-done:
		reason = "the agent process exited (" + agent.ExitDetail() + ") without its session ending" + note
	case <-timer.C:
	}
	got, err := p.report(ctx, op, Failed, "", reason)
	if err != nil {
		return
	}
	if got.Status != Succeeded {
		// Keep the worktree the agent ran in; when this poller did not
		// launch it (a restart), keep every worktree the worker has here.
		keep := p.state.worktreesFor(ref, w.Worktrees)
		if t, ok := p.agentWorktree(op.WorkerID); ok {
			keep = []string{t}
		}
		for _, t := range keep {
			if err := p.state.keep(t, "a worker.end for its worker failed ("+reason+")"); err != nil {
				p.log("state-failed", op.ID, op.Type, "", err.Error())
			}
		}
		return
	}
	if h, ok := got.Result["handoff"].(bool); ok {
		p.log("ended", op.ID, op.Type, Succeeded, fmt.Sprintf("handoff written: %t", h))
	}
}

// trackAgent remembers a launched agent and the worktree it runs in, in
// memory and in the state file (it outlives a poller restart).
func (p *Poller) trackAgent(workerID, token string, a Agent) {
	id, hasID := p.launchIdentity(a)
	p.mu.Lock()
	p.agents[workerID] = a
	p.agentTrees[workerID] = token
	if hasID {
		p.agentIDs[workerID] = id
	} else {
		delete(p.agentIDs, workerID)
	}
	p.mu.Unlock()
	start := ""
	if hasID {
		start = id.StartTime
	}
	if err := p.state.recordAgent(workerID, token, a.PID(), start); err != nil {
		p.log("state-failed", "", "", "", err.Error())
	}
}

// launchIdentity observes a just-launched agent's process identity, when
// resets are enabled. An unreaped child keeps its pid, so an identity read
// while Done is still open, checked after the read, is the agent's own.
func (p *Poller) launchIdentity(a Agent) (ProcessIdentity, bool) {
	if p.resetDeps.Proc == nil || a.PID() <= 0 {
		return ProcessIdentity{}, false
	}
	h, err := p.resetDeps.Proc.Observe(a.PID())
	if err != nil {
		return ProcessIdentity{}, false
	}
	defer h.Close()
	select {
	case <-a.Done():
		return ProcessIdentity{}, false
	default:
		return h.Identity(), true
	}
}

// agentWorktree is the worktree this worker's launched agent runs in: this
// process's launch, else the one recorded before a restart.
func (p *Poller) agentWorktree(workerID string) (string, bool) {
	p.mu.Lock()
	t, ok := p.agentTrees[workerID]
	p.mu.Unlock()
	if ok {
		return t, true
	}
	return p.state.recordedWorktree(workerID)
}

// agentRunningIn reports whether an agent this host launched — in this
// process or recorded before a restart — is still running in token.
func (p *Poller) agentRunningIn(token string) bool {
	p.mu.Lock()
	for id, t := range p.agentTrees {
		if t != token {
			continue
		}
		select {
		case <-p.agents[id].Done():
		default:
			p.mu.Unlock()
			return true
		}
	}
	p.mu.Unlock()
	for _, pid := range p.state.agentPIDsIn(token) {
		if p.host.Alive(pid) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
