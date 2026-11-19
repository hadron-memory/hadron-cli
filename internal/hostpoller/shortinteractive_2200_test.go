package hostpoller

import (
	"path/filepath"
	"testing"
)

// cli#2200 C9 at the poller: a short-lived run is the interactive agent, so
// it does NOT exit when its prompt completes. worker.end (#2057) stops it as
// the fast path once the agent has ended its own session.
func TestShortRunThatDidNotExitIsStoppedByWorkerEnd2200(t *testing.T) {
	p, srv, _ := testPoller(t)
	orcaWorker(p, filepath.Join(t.TempDir(), "disk"),
		LauncherProfile{Family: "claude", Mode: ModeShort, Model: "sonnet", Account: "a"}, AccountProfile{Bin: "claude-worker"})
	withStop(p, StopOnEndAlways)
	rec := &recordedOrca{}
	p.EnableOrcaLaunch(rec.launch, ConfigProfiles{Cfg: p.startCfg})
	srv.notDone["s1"] = 2
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if len(rec.specs) != 1 {
		t.Fatalf("launches = %d", len(rec.specs))
	}
	for _, a := range rec.specs[0].Argv {
		if a == "-p" {
			t.Fatalf("the short run launched headless: %q", rec.specs[0].Argv)
		}
	}
	// The run did not exit by itself: its agent is still tracked and running.
	if _, ok := p.agentWorktree("id-iris"); !ok {
		t.Fatal("the interactive short run must still be running after its prompt completes")
	}
	agent, ok := p.agents["id-iris"].(*fakeAgent)
	if !ok || agent == nil {
		t.Fatal("no tracked agent")
	}
	select {
	case <-agent.Done():
		t.Fatal("the agent exited by itself")
	default:
	}
	// The agent has ended its own session; worker.end stops the process.
	p.agentSessions["id-iris"] = "sess1"
	srv.reports = nil
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if agent.terminations() != 1 {
		t.Fatalf("worker.end must stop a short run that did not exit: terminated=%d", agent.terminations())
	}
	if _, ok := p.agentWorktree("id-iris"); ok {
		t.Fatal("the stopped agent is still tracked")
	}
}
