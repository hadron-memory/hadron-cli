package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
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
	c := p.config() // one snapshot for the repo and the worktree owner check
	repoPath, ok := c.Repo(repo)
	if !ok {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("repo %q is not configured on this host", repo))
		return
	}
	token := path.Base(repo) + "/" + label
	if !ValidNameToken(token) {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("worktree name %q is not a valid name token", token))
		return
	}
	if owner, ok := p.state.ownerOf(token, c); ok && owner != ref {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("%s is registered for another worker here", token))
		return
	}
	dir := p.config().WorktreePath(token)
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
	dir := p.config().WorktreePath(token)
	if dir == "" {
		// A host that runs from the platform, and the token names no single Orca worktree here: nothing is
		// inspected or removed (hadron-cli#3237).
		p.finish(ctx, op, Failed, "", p.config().WorkdirRefusal(token))
		return
	}
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
	// The start's run settings (#2199). args.family is the family to LAUNCH,
	// not an echo of the registration: a worker launched through Orca may run
	// as another family when this host has a template and the worker's
	// account can launch it (checked below, before any process). Every worker
	// launches through Orca (the exec path was removed, cli#2914).
	rs := RunSettingsFrom(op)
	if err := rs.Validate(); err != nil {
		p.finish(ctx, op, Failed, "", err.Error()+"; nothing was launched")
		return
	}
	run, ok := p.resolveOrcaRun(ctx, op, ref, rs)
	if !ok {
		return
	}
	token := argString(op, "worktree")
	if why := p.config().WorkdirRefusal(token); why != "" {
		// A host that runs from the platform: the token did not resolve to one Orca worktree (hadron-cli#3237).
		p.finish(ctx, op, Failed, "", why)
		return
	}
	if !contains(p.state.worktreesFor(ref, w.Worktrees), token) {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("worktree %q is not registered for this worker here", token))
		return
	}
	dir := p.config().WorktreePath(token)
	if st, err := p.host.Inspect(ctx, dir); err != nil {
		// An error is not "does not exist": an untrusted repo config (nothing was
		// run on the tree) and an unreadable one say what they are.
		reason := fmt.Sprintf("could not inspect worktree %q: %s; nothing was launched", token, err.Error())
		if w.resetsOnStart() {
			// A worker that resets on start is refused like every other reset
			// refusal: led by its code, so the report is greppable and the task
			// waits with the not-started alarm.
			reason = CodeResetFailed + ": " + reason
		}
		p.finish(ctx, op, Failed, "", reason)
		return
	} else if !st.Exists {
		p.finish(ctx, op, Failed, "", fmt.Sprintf("worktree %q does not exist on this host", token))
		return
	}
	if op.LaunchGrant == "" {
		p.finish(ctx, op, Failed, "", "the collected start carried no launch grant")
		return
	}
	release, err := p.reserveWorktreeLaunch(token)
	if err != nil {
		p.finish(ctx, op, Failed, "", "another poller launch owns this worktree; nothing launched")
		return
	}
	defer release()
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
	// C2b (G6): the workspace is reset as part of starting, after every check
	// that no process works in the tree (so nothing is moved under a running
	// agent) and before the launch. A refusal reports the code, launches
	// nothing and leaves the task waiting with the not-started alarm.
	if w.resetsOnStart() {
		out, err := p.host.ResetWorkspace(ctx, dir)
		switch {
		case err != nil:
			p.finish(ctx, op, Failed, "", fmt.Sprintf("%s: could not reset %s: %s; nothing was launched", CodeResetFailed, token, err.Error()))
			return
		case out.Refused():
			p.log("reset-refused", op.ID, op.Type, "", fmt.Sprintf("%s in %s: %s", out.Code, token, out.Detail))
			reason := fmt.Sprintf("%s: %s; nothing was launched", out.Code, out.Detail)
			if len(out.Rescued) > 0 {
				reason += " (rescue refs: " + strings.Join(out.Rescued, ", ") + ")"
			}
			p.finish(ctx, op, Failed, "", reason)
			return
		case out.Reset:
			detail := fmt.Sprintf("reset %s to %s", token, out.Base)
			if len(out.Rescued) > 0 {
				detail += "; rescue refs: " + strings.Join(out.Rescued, ", ")
			}
			p.log("reset", op.ID, op.Type, "", detail)
		default:
			p.log("reset-skipped", op.ID, op.Type, "", fmt.Sprintf("%s has no Git work tree: no reset", token))
		}
	}
	deadline := p.now().Add(LaunchGrantTTL)
	// C3 (#2058): through an Orca terminal, from the launcher profile. A worker that cannot launch
	// there launches nothing (the exec path it once fell back to was removed, cli#2914).
	agent, err := p.orcaStart(ctx, op, ref, w, dir, run)
	if err != nil {
		if agent != nil {
			// An unproven launch whose terminal could not be closed: it
			// stays recorded, so the one-agent-per-worktree guard refuses
			// a second launch there and a stop retries the close.
			p.trackAgent(op.WorkerID, token, agent)
		}
		p.finish(ctx, op, Failed, "", "launching the agent through Orca failed: "+err.Error())
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
		bound, err := p.server.Report(ctx, op.ID, Succeeded, "", "")
		switch {
		case err == nil:
			if sid, ok := bound.Result["sessionId"].(string); ok && sid != "" {
				p.mu.Lock()
				p.agentSessions[op.WorkerID] = sid
				p.mu.Unlock()
			}
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
	p.forgetAgent(op)
}

// forgetAgent drops every record of a worker's launched agent, in memory and
// in the state file. Only ever after a CONFIRMED stop.
func (p *Poller) forgetAgent(op Operation) {
	p.mu.Lock()
	delete(p.agents, op.WorkerID)
	delete(p.agentTrees, op.WorkerID)
	delete(p.agentIDs, op.WorkerID)
	delete(p.agentSessions, op.WorkerID)
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
// session itself — the host never ends a session (cor:agt:020:10).
//
// With stop_on_end (hadron-cli#2057 C2a) the end also stops the worker's
// agent process: for a short-lived worker (always), or when whoever manages
// the App issued it (manager). The wait is then stop_wait (60 s), given only
// to an end prompt that was delivered — the host cannot read whether the
// session ended, so the bound is what it has — and the process is stopped
// with its group: SIGTERM, 5 s, SIGKILL. Only a process identified by the
// worker's worktree and its binding is touched, and the report says which
// process was stopped or why none was. The report stays FAILED: the server
// records the end the worker made, and the host still ends no session.
func (p *Poller) workerEnd(ctx context.Context, op Operation) {
	ref, w, ok := p.workerRef(op)
	if !ok {
		// A worker a reload took out of the file may still have a live agent,
		// and ending a session only de-escalates: carry it out with the entry
		// the worker last had, and say so (cli#2623).
		rm, found := p.removedWorkerFor(op)
		if !found {
			p.finish(ctx, op, Failed, "", "the worker is not configured on this host")
			return
		}
		ref, w = rm.ref, rm.cfg
		p.log("removed-worker-end", op.ID, op.Type, "", ref+": no longer in the configuration file; ending its session with the entry it last had")
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
	stop := w.stopsOnEnd(op.IssuedBy)
	note := ""
	prompted := false
	if len(w.End) > 0 {
		dir := ""
		if t, ok := p.agentWorktree(op.WorkerID); ok {
			dir = p.config().WorktreePath(t)
		}
		env := agentEnvKeys(os.Environ(), p.scrubKeys(), "HADRON_SESSION_ID="+session, "HADRON_WORKER_ID="+op.WorkerID)
		if err := p.host.Prompt(ctx, w.End, dir, env); err != nil {
			note = "; the end prompt failed: " + err.Error()
		} else {
			prompted = true
		}
	} else {
		note = "; no end prompt is configured for this worker"
	}
	wait := w.endWait()
	if stop {
		wait = w.stopWait()
	}
	reason := fmt.Sprintf("the session did not end within %s%s", wait, note)
	if stop && !prompted {
		// Nobody was asked to end the session, so there is nothing to wait
		// for: the stop is immediate.
		wait = 0
		reason = "the worker was not asked to end its session" + note
	}
	var done <-chan struct{}
	if agent != nil {
		done = agent.Done()
	}
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-done:
			reason = "the agent process exited (" + agent.ExitDetail() + ") without its session ending" + note
		case <-timer.C:
		}
	} else if ctx.Err() != nil {
		return
	}
	// cli#2201 C10: a short-lived worker whose end finds no agent. Only the
	// strict census can say so; its answer is the reason's first word, which
	// is what the server reads (hadron-server#4292). Nothing runs, so nothing
	// is scanned for or stopped. Anything short of a proof reports as before,
	// with the census result appended, and never leads with AGENT_ABSENT.
	absent := false
	if agent == nil && session != "" && w.StopOnEnd == StopOnEndAlways {
		proof := p.proveAgentAbsent(ctx, p.agentCensus, p.worktreeDirs(op, ref, w))
		if proof.Absent {
			absent = true
			reason = AgentAbsent + ": " + proof.Detail + "; " + reason
			p.log("agent-absent", op.ID, op.Type, "", proof.Detail)
		} else {
			reason += "; " + proof.Detail
			p.log("agent-absent-unproven", op.ID, op.Type, "", proof.Detail)
		}
	}
	if stop && !absent {
		reason += "; " + p.stopWorkerProcess(ctx, op, ref, w, session, agent)
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

// stopWorkerProcess stops the agent process of the worker a stopping
// worker.end names and says, for the report and the operator's log, which
// process was stopped or why none was (#2057 C2a). The agent this poller
// launched is stopped by its own launch group (Terminate). Any other — an
// agent started from a terminal, or launched before a restart — is found by
// the /proc scan of the worker's own worktrees, and only where the tree's
// session binding does not say otherwise: a tree bound to another worker, to
// another session of this worker, or carrying a binding that cannot be read
// is left alone. No binding at all is the worker having ended its session,
// which clears it: the tree is then the worker's by this host's own
// configuration, where a worktree belongs to one worker.
func (p *Poller) stopWorkerProcess(ctx context.Context, op Operation, ref string, w WorkerConfig, session string, agent Agent) string {
	// allowed answers whether the tree's session binding still leaves its
	// processes to this end: not another worker's, not another session of
	// this worker than the one being ended, readable. It is asked before any
	// process is looked for AND again right before a signal goes out, so a
	// replacement session that binds in between is not stopped by the older
	// session's end (the window cannot be closed against a separate process
	// writing the binding, only made as short as the signal itself).
	allowed := func(t string) (bool, string) {
		b := p.bound(p.config().WorktreePath(t))
		switch {
		case b.Unusable:
			return false, fmt.Sprintf("worktree %s has a session binding that cannot be read, so its processes were not touched", t)
		case b.WorkerID != "" && b.WorkerID != op.WorkerID:
			return false, fmt.Sprintf("worktree %s is bound to another worker, so its processes were not touched", t)
		case b.WorkerID != "" && b.SessionID != "" && b.SessionID != session:
			return false, fmt.Sprintf("worktree %s is bound to another session of this worker than the one being ended, so its processes were not touched", t)
		}
		return true, ""
	}
	if agent != nil {
		said := ""
		// The launched agent runs in the tree it was started in. Without that
		// identity nothing can be checked, so nothing is stopped; with it, the
		// session its start bound must be the one being ended (both must be
		// known: a launch whose session was not recorded, or an end that names
		// none, is declined), and the same binding rules as a scanned agent
		// apply. A restart forgets the launch, and the scan path decides.
		t, ok := p.agentWorktree(op.WorkerID)
		if !ok {
			why := "the launched agent's worktree is not recorded, so it could not be identified and was not touched"
			p.log("stop-skipped", op.ID, op.Type, "", why)
			return why
		}
		p.mu.Lock()
		launched := p.agentSessions[op.WorkerID]
		p.mu.Unlock()
		if session == "" {
			why := "this end names no session, so the launched agent could not be identified and was not touched"
			p.log("stop-skipped", op.ID, op.Type, "", why)
			return why
		}
		// identified answers whether the launched agent is the one serving the
		// session being ended: by the session its start (or its reset
		// relaunch) recorded; when that is not recorded (a start the server
		// had already finished), only a binding that names this worker AND
		// this session proves it, and an empty binding does not.
		identified := func() (bool, string) {
			if launched != "" {
				if launched != session {
					return false, "the launched agent serves another session than the one being ended, so it was not touched"
				}
				return true, ""
			}
			b := p.bound(p.config().WorktreePath(t))
			if b.Unusable || b.WorkerID != op.WorkerID || b.SessionID == "" || b.SessionID != session {
				return false, "the session of the launched agent is not recorded and the worktree's binding does not name this session, so the agent could not be identified and was not touched"
			}
			return true, ""
		}
		if ok, why := identified(); !ok {
			p.log("stop-skipped", op.ID, op.Type, "", why)
			return why
		}
		if ok, why := allowed(t); !ok {
			p.log("stop-skipped", op.ID, op.Type, "", why)
			return why
		}
		tw, ok := agent.(interface {
			TerminateWith(Authorize) (bool, string)
		})
		if !ok {
			// An agent that cannot be stopped under the binding lock is not
			// stopped at all: no silent fall back to an unguarded Terminate.
			why := "the launched agent cannot be stopped under the session-binding lock, so it was not touched"
			p.log("stop-skipped", op.ID, op.Type, "", why)
			return why
		}
		// Authorize before each signal, holding the binding lock through it,
		// and never across the grace between SIGTERM and SIGKILL.
		stopped, detail := tw.TerminateWith(p.authorizeStop(t, func() bool {
			if ok, _ := identified(); !ok {
				return false
			}
			ok, _ := allowed(t)
			return ok
		}, op))
		said = fmt.Sprintf("stopped the agent this host launched (pid %d)", agent.PID())
		if !stopped {
			said = fmt.Sprintf("the agent this host launched (pid %d) could not be confirmed stopped and its records are kept", agent.PID())
		}
		if detail != "" {
			said += ": " + detail
		}
		if stopped {
			p.forgetAgent(op)
			p.log("stopped", op.ID, op.Type, "", said)
		} else {
			p.log("stop-unconfirmed", op.ID, op.Type, "", said)
		}
		return said
	}
	trees := p.state.worktreesFor(ref, w.Worktrees)
	if t, ok := p.agentWorktree(op.WorkerID); ok {
		trees = []string{t}
	}
	if len(trees) == 0 {
		said := "no worktree is registered for this worker on this host, so no process could be identified and none was touched"
		p.log("stop-skipped", op.ID, op.Type, "", said)
		return said
	}
	parts := []string{}
	say := func(kind, text string) {
		p.log(kind, op.ID, op.Type, "", text)
		parts = append(parts, text)
	}
	for _, t := range trees {
		if ok, why := allowed(t); !ok {
			say("stop-skipped", why)
			continue
		}
		stops, err := p.host.StopAgents(ctx, p.config().WorktreePath(t), p.authorizeStop(t, func() bool { ok, _ := allowed(t); return ok }, op))
		if err != nil {
			say("stop-skipped", fmt.Sprintf("this host could not look for agent processes in worktree %s, so none was touched: %v", t, err))
			continue
		}
		if len(stops) == 0 {
			say("stop-skipped", fmt.Sprintf("no agent process was found in worktree %s", t))
			continue
		}
		for _, s := range stops {
			kind := "stopped"
			if !s.Stopped {
				kind = "stop-unconfirmed"
			}
			say(kind, fmt.Sprintf("in worktree %s: %s", t, s.Detail))
		}
	}
	return strings.Join(parts, "; ")
}

// trackAgent remembers a launched agent and the worktree it runs in, in
// memory and in the state file (it outlives a poller restart).
func (p *Poller) trackAgent(workerID, token string, a Agent) {
	id, hasID := p.launchIdentity(a)
	p.mu.Lock()
	p.agents[workerID] = a
	p.agentTrees[workerID] = token
	// A new launch starts with no recorded session: an earlier run that
	// exited on its own leaves its session behind, and this launch must not
	// inherit it (its own start or reset records the one it serves).
	delete(p.agentSessions, workerID)
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
	p.watchForExit(workerID, a)
}

// launchIdentity observes a just-launched agent's process identity for the
// relaunch pin, when the launch holds a process handle.
func (p *Poller) launchIdentity(a Agent) (ProcessIdentity, bool) {
	if agent, ok := a.(*OrcaAgent); ok && agent.h != nil {
		select {
		case <-agent.Done():
			return ProcessIdentity{}, false
		default:
		}
		identity := agent.h.Identity()
		if identity.PID > 0 && identity.StartTime != "" {
			return identity, true
		}
	}
	return ProcessIdentity{}, false
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

// bindingLockWait bounds how long a stop waits for a binding writer: a bind
// holds the lock for one write, so this is generous, and a writer is never
// made to wait beyond one read and signal (#2068). The stop that holds the
// lock is Linux-only and releases right after the signal, before any grace
// wait, so this constant bounds acquisition only.
const bindingLockWait = 2 * time.Second

// authorizeStop is the Authorize for one worktree: before each signal it takes
// the worktree's session-binding lock (the lock `team session start` holds
// while it writes), re-reads the binding under it through check, and returns
// the release the caller invokes right after the one signal syscall it
// authorized. A lock that cannot be taken within bindingLockWait declines the
// stop, and so does a check that no longer passes; both are logged. The
// release precedes every grace wait.
func (p *Poller) authorizeStop(token string, check func() bool, op Operation) Authorize {
	return func() (func(), bool) {
		release, err := p.lockBinding(p.config().WorktreePath(token), bindingLockWait)
		if err != nil {
			p.log("stop-skipped", op.ID, op.Type, "", fmt.Sprintf("the session binding of worktree %s could not be locked, so nothing was signalled: %v", token, err))
			return nil, false
		}
		if !check() {
			release()
			return nil, false
		}
		return release, true
	}
}

// worktreeDirs are the paths of the worktrees a worker.end's census looks in:
// every tree the worker has here, and the tree of the launch this host
// remembers if that is not one of them, each once. The launch record outlives
// its agent's exit, so it must never narrow the set: an exited launch in one
// tree says nothing about another (#2207 BLIND F2). The stop keeps its own
// narrower choice; this is a proof of absence, which needs every tree.
func (p *Poller) worktreeDirs(op Operation, ref string, w WorkerConfig) []string {
	trees := append([]string(nil), p.state.worktreesFor(ref, w.Worktrees)...)
	if t, ok := p.agentWorktree(op.WorkerID); ok && !contains(trees, t) {
		trees = append(trees, t)
	}
	dirs := make([]string, 0, len(trees))
	for _, t := range trees {
		dirs = append(dirs, p.config().WorktreePath(t))
	}
	return dirs
}
