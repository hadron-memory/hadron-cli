package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// ResetDeps is what the worker.reset-context handler needs beyond the
// poller (#1043). Each is injected so the handler runs against fakes; in
// production only `team host run` could supply them, and this slice wires
// none, so the handler is unreachable there.
type ResetDeps struct {
	// Proc observes and signals agent processes (LinuxProcessControl).
	Proc ProcessControl
	// Git is the Git determination (GitTree).
	Git func(ctx context.Context, dir string) (GitTreeState, error)
	// Idle proves the worker's agent idle with an empty composer and no
	// menu (BUSY, DRAFT_OR_MENU). Nil means this host cannot prove it, and
	// every reset refuses NOT_IDLE: idle is never assumed.
	Idle func(ctx context.Context, workerRef string) error
}

// EnableResets turns the handler on for this poller. It declares no
// capability: registration still sends exactly cfg.Capabilities, and
// advertising worker.reset-context is a separate authorized activation.
func (p *Poller) EnableResets(rs ResetServer, deps ResetDeps) {
	p.resets, p.resetDeps = rs, deps
}

// workerResetContext carries out a collected worker.reset-context up to the
// claim. Every refusal here comes before any process control and before
// any protocol step; each is reported FAILED with its code first, once.
//
// The claim and everything after it (checkpoint, recovery registration,
// the stop, relaunch, restart recovery) are not built: they wait on three
// server answers (no phase read for restart recovery, no relaunch binding,
// the checkpoint writer's authority — hadron-cli#1043 comment 6587438356).
// Until then a reset that passes every check here still refuses
// HOST_UNSUPPORTED, unclaimed: the worker is never marked RESETTING by a
// host that cannot finish the reset.
func (p *Poller) workerResetContext(ctx context.Context, op Operation) {
	refuse := func(code, detail string) { p.finish(ctx, op, Failed, "", code+": "+detail) }
	if p.resets == nil {
		refuse("HOST_UNSUPPORTED", "this host carries out no worker.reset-context (hadron-cli#1043)")
		return
	}
	for _, a := range []string{"resetId", "sessionId", "taskId"} {
		if argString(op, a) == "" {
			refuse("BAD_OPERATION", "the operation names no "+a)
			return
		}
	}
	ref, w, ok := p.workerRef(op)
	if !ok {
		refuse("HOST_UNSUPPORTED", "the worker is not configured on this host")
		return
	}
	// Without a recover prompt a relaunched agent would start with no
	// recovery: refuse before anything is touched, never after a stop.
	if len(w.Recover) == 0 {
		refuse("HOST_UNSUPPORTED", "no recover prompt is configured for this worker on this host")
		return
	}
	// Only an agent this poller launched, still running, can be reset: the
	// poller is its wrapper. Anything else (exited, launched by hand or by
	// another supervisor) is not this host's to stop.
	p.mu.Lock()
	agent := p.agents[op.WorkerID]
	p.mu.Unlock()
	token, hasTree := p.agentWorktree(op.WorkerID)
	if agent == nil || !hasTree {
		refuse("PROCESS_CHANGED", "no agent this host launched is running for the worker")
		return
	}
	select {
	case <-agent.Done():
		refuse("PROCESS_CHANGED", "the agent this host launched for the worker has exited")
		return
	default:
	}
	// Git first (ruling 0012): only inside a Git tree do cleanliness and
	// push state refuse. An unreadable tree refuses, never passes.
	git, err := p.resetDeps.Git(ctx, p.cfg.WorktreePath(token))
	switch {
	case err != nil:
		refuse("DIRTY_TREE", "the worktree could not be read: "+err.Error())
		return
	case git.InGit && git.Dirty:
		refuse("DIRTY_TREE", "the worktree has uncommitted or untracked changes")
		return
	case git.InGit && git.Upstream == "":
		refuse("UNPUSHED_HEAD", "HEAD is not on a branch with a remote upstream")
		return
	case git.InGit && git.Ahead > 0:
		refuse("UNPUSHED_HEAD", fmt.Sprintf("HEAD is %d commit(s) ahead of %s", git.Ahead, git.Upstream))
		return
	}
	h, err := p.resetDeps.Proc.Observe(agent.PID())
	if errors.Is(err, ErrUnsupportedPlatform) {
		refuse("HOST_UNSUPPORTED", err.Error())
		return
	}
	if err != nil {
		refuse("PROCESS_CHANGED", "the agent process could not be observed: "+err.Error())
		return
	}
	defer h.Close()
	if p.resetDeps.Idle == nil {
		refuse("NOT_IDLE", "this host has no proven terminal read to show the agent idle")
		return
	}
	if err := p.resetDeps.Idle(ctx, ref); err != nil {
		refuse("NOT_IDLE", err.Error())
		return
	}
	refuse("HOST_UNSUPPORTED", "the claim and later reset steps are not built on this host yet (hadron-cli#1043)")
}

// relaunchAgent starts the worker's agent again in the same worktree, after
// its old process stopped. A reset is a shed, not a session end
// (cor:agt:050:27 part 3): the binding and session stay, so the new agent
// gets no launch grant and finds its kept binding where it runs. Then the
// configured recover argv hands it the recovery prompt. It verifies on its
// own evidence: a new process identity, the prompt delivered, the agent
// still running. It returns the new identity, or a failure code
// (RELAUNCH_BLOCKED: nothing started; RELAUNCH_UNVERIFIED: something started
// but is not proven to be the recovered agent) and its detail.
func (p *Poller) relaunchAgent(ctx context.Context, op Operation, w WorkerConfig, token string, old ProcessIdentity, checkpoint string) (ProcessIdentity, string, string) {
	dir := p.cfg.WorktreePath(token)
	agent, err := p.host.Launch(ctx, LaunchSpec{Argv: w.Launch, Dir: dir, Env: agentEnv(os.Environ(), p.key)})
	if err != nil {
		return ProcessIdentity{}, "RELAUNCH_BLOCKED", "launching the agent failed: " + err.Error()
	}
	p.trackAgent(op.WorkerID, token, agent)
	h, err := p.resetDeps.Proc.Observe(agent.PID())
	if err != nil {
		return ProcessIdentity{}, "RELAUNCH_UNVERIFIED", "the relaunched agent could not be observed: " + err.Error()
	}
	defer h.Close()
	id := h.Identity()
	if id == old {
		return ProcessIdentity{}, "RELAUNCH_UNVERIFIED", "the relaunched agent has the stopped agent's process identity"
	}
	env := agentEnv(os.Environ(), p.key, "HADRON_SESSION_ID="+argString(op, "sessionId"),
		"HADRON_WORKER_ID="+op.WorkerID, "HADRON_RECOVERY_NODE="+checkpoint)
	if err := p.host.Prompt(ctx, w.Recover, dir, env); err != nil {
		return ProcessIdentity{}, "RELAUNCH_UNVERIFIED", "the recover prompt failed: " + err.Error()
	}
	select {
	case <-agent.Done():
		return ProcessIdentity{}, "RELAUNCH_UNVERIFIED", "the relaunched agent exited (" + agent.ExitDetail() + ")"
	default:
	}
	return id, "", ""
}
