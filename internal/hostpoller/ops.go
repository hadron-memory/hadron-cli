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
	dir := p.cfg.WorktreePath(token)
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
	st, err := p.host.Inspect(ctx, dir)
	if err != nil {
		p.finish(ctx, op, Failed, "", "reading the worktree: "+err.Error())
		return
	}
	if !st.Exists {
		_ = p.state.removeCreated(token)
		p.finish(ctx, op, Succeeded, "", "")
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
	if fam := argString(op, "family"); fam != w.Family {
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
	for {
		select {
		case <-agent.Done():
			// One last ask: an agent that bound and then exited is a start
			// that succeeded.
			if _, err := p.server.Report(ctx, op.ID, Succeeded, "", ""); err == nil {
				p.log("reported", op.ID, op.Type, Succeeded, "its session bound before the agent exited")
				return
			}
			p.finish(ctx, op, Failed, "", "the agent exited before binding a session ("+agent.ExitDetail()+"); the launch command must stay in the foreground for the agent's life")
			return
		default:
		}
		if !p.now().Before(deadline) {
			p.finish(ctx, op, Failed, "", fmt.Sprintf("no session was bound within %s, the launch grant's lifetime", LaunchGrantTTL))
			return
		}
		if err := p.sleep(ctx, p.BindPoll); err != nil {
			return
		}
		_, err := p.server.Report(ctx, op.ID, Succeeded, "", "")
		if err == nil {
			p.log("reported", op.ID, op.Type, Succeeded, "its session bound")
			return
		}
		if errors.Is(err, ErrFinal) {
			// Already decided server-side (e.g. a refused bind failed it).
			p.log("reported", op.ID, op.Type, "", "the server already finished it: "+err.Error())
			return
		}
		if !errors.Is(err, ErrNotDone) {
			p.log("report-failed", op.ID, op.Type, Succeeded, err.Error())
		}
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
	p.mu.Lock()
	p.agents[workerID] = a
	p.agentTrees[workerID] = token
	p.mu.Unlock()
	if err := p.state.recordAgent(workerID, token, a.PID()); err != nil {
		p.log("state-failed", "", "", "", err.Error())
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
