package hostpoller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// hadron-cli#3301 fix head (BLIND P2-3, P2-4 on C3): delivery is wired once on a platform host and follows the
// row both ways with no restart; an agent's recorded worktree token that did not resolve at this poll ("") is
// never a directory a census proves empty, a stop looks in, or an end prompt runs in (SECOND delta P2).

func TestPlatformDeliveryTurnsOnAtTheNextStepWhenTheRowStartedOff3301(t *testing.T) {
	fx := newPlatform3237(t, iris3237())
	fx.tree("r1", "/orca/hadron-cli/iris")
	off := false
	fx.set.DeliveryEnabled = &off
	src := &fakeBatches{}
	fx.p.EnableDelivery(src, &fakeDeliverer{})
	step(t, fx.p)
	if src.calls != 0 {
		t.Fatalf("a row with deliveryEnabled false reads no batches; read %d", src.calls)
	}
	fx.mu.Lock()
	fx.set.DeliveryEnabled = nil // null is on
	fx.mu.Unlock()
	step(t, fx.p)
	if src.calls == 0 {
		t.Fatal("turning deliveryEnabled on at the platform delivers at the next step, with no restart")
	}
	fx.mu.Lock()
	fx.set.DeliveryEnabled = &off
	fx.mu.Unlock()
	n := src.calls
	step(t, fx.p)
	if src.calls != n {
		t.Fatalf("turning it off again stops the reads at the next step: %d → %d", n, src.calls)
	}
}

func TestACensusNeverProvesAbsenceInAnUnresolvedWorktree3301(t *testing.T) {
	p, _, _ := testPoller(t)
	probed := 0
	probe := func(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error) {
		probed++
		return hostrelaunch.Observation{Proven: true}, nil
	}
	got := p.proveAgentAbsent(context.Background(), probe, []string{"/orca/hadron-cli/iris", ""})
	if got.Absent || got.Found {
		t.Fatalf("an unresolved worktree proves nothing: %+v", got)
	}
	if !strings.Contains(got.Detail, "did not resolve") {
		t.Errorf("the detail says why: %q", got.Detail)
	}
}

// unresolved3301 is a platform poller whose worker Iris has an agent recorded in hadron-cli/iris, a token Orca
// did not resolve at this poll (no such worktree), with an end prompt and a binding that would let a stop go on.
func unresolved3301(t *testing.T) *platformFixture3237 {
	t.Helper()
	fx := newPlatform3237(t, iris3237())
	step(t, fx.p)
	c := *fx.p.config()
	ws := map[string]WorkerConfig{}
	for k, v := range c.Workers {
		ws[k] = v
	}
	w := ws["w-iris"]
	w.End, w.EndWait = []string{"/usr/bin/end-session"}, "1ms"
	ws["w-iris"] = w
	c.Workers = ws
	fx.p.live.Store(&c)
	fx.p.mu.Lock()
	fx.p.workerRefs["id-w-iris"] = "w-iris"
	fx.p.agentTrees["id-w-iris"] = "hadron-cli/iris"
	fx.p.mu.Unlock()
	fx.p.bound = func(string) delivery.Binding { return delivery.Binding{WorkerID: "id-w-iris", SessionID: "sess1"} }
	if got := fx.p.config().WorktreePath("hadron-cli/iris"); got != "" {
		t.Fatalf("the fixture's token must be unresolved, got %q", got)
	}
	return fx
}

func TestAStopNeverLooksInAnUnresolvedWorktree3301(t *testing.T) {
	fx := unresolved3301(t)
	w, _ := fx.p.config().Worker("w-iris")
	op := Operation{ID: "e1", Type: OpWorkerEnd, WorkerID: "id-w-iris", Args: map[string]any{"sessionId": "sess1"}}
	got := fx.p.stopWorkerProcess(context.Background(), op, "w-iris", w, "sess1", nil)
	fx.host.mu.Lock()
	stopped := append([]string{}, fx.host.stopped...)
	fx.host.mu.Unlock()
	if len(stopped) != 0 {
		t.Fatalf("no process is looked for in an unresolved worktree; StopAgents ran on %q", stopped)
	}
	if !strings.Contains(got, "did not resolve") {
		t.Errorf("the stop says why nothing was touched: %q", got)
	}
}

func TestAnEndPromptNeverRunsInAnUnresolvedWorktree3301(t *testing.T) {
	fx := unresolved3301(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	op := Operation{ID: "e1", Type: OpWorkerEnd, WorkerID: "id-w-iris", Args: map[string]any{"sessionId": "sess1"}}
	fx.srv.mu.Lock()
	fx.srv.notDone[op.ID] = 1
	fx.srv.mu.Unlock()
	fx.p.workerEnd(ctx, op)
	fx.host.mu.Lock()
	prompts := len(fx.host.prompts)
	fx.host.mu.Unlock()
	if prompts != 0 {
		t.Fatalf("an end prompt never runs with no directory (it would run in the poller's own); %d ran", prompts)
	}
	if r := only(t, fx.srv); !strings.Contains(r.reason, "the end prompt was not sent") {
		t.Errorf("the report says the prompt was not sent and why: %q", r.reason)
	}
}
