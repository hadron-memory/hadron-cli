package hostpoller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// hadron-cli#2057 C2a: worker.end stops the worker's process, behind a
// per-worker switch that is off by default.

func stopOp(issuedBy string) Operation {
	o := endOp()
	o.IssuedBy = issuedBy
	return o
}

func withStop(p *Poller, mode string) {
	w := p.startCfg.Workers["iris"]
	w.StopOnEnd = mode
	w.StopWait = "20ms"
	p.startCfg.Workers["iris"] = w
}

func TestWorkerEndStopsNothingByDefault(t *testing.T) {
	p, srv, host := testPoller(t)
	host.stops = []AgentStop{{PID: 7, Stopped: true, Detail: "pid 7"}}
	srv.batches = [][]Operation{{stopOp(IssuedByPlatform)}}
	step(t, p)
	if len(host.stopped) != 0 {
		t.Fatalf("stop_on_end unset must keep today's behaviour, got stops at %v", host.stopped)
	}
	if r := only(t, srv); strings.Contains(r.reason, "stopped") {
		t.Fatalf("%+v", r)
	}
}

func TestWorkerEndStopsTheScannedProcessAndSaysWhich(t *testing.T) {
	p, srv, host := testPoller(t)
	withStop(p, StopOnEndAlways)
	p.bound = func(string) delivery.Binding { return delivery.Binding{WorkerID: "id-iris", SessionID: "sess1"} }
	host.stops = []AgentStop{{PID: 7, Stopped: true, Detail: "terminated pid 7 (claude), its process group"}}
	srv.batches = [][]Operation{{stopOp(IssuedByPlatform)}}
	step(t, p)
	r := only(t, srv)
	if r.outcome != Failed {
		t.Fatalf("a stopping end still reports FAILED (the host ends no session): %+v", r)
	}
	if len(host.stopped) != 1 || !strings.HasSuffix(host.stopped[0], "repo/iris") {
		t.Fatalf("stops = %v", host.stopped)
	}
	if !strings.Contains(r.reason, "pid 7") {
		t.Fatalf("the report must say which process was stopped: %q", r.reason)
	}
}

func TestWorkerEndStopOnlyWaitsForADeliveredPrompt(t *testing.T) {
	p, srv, host := testPoller(t)
	withStop(p, StopOnEndAlways)
	w := p.startCfg.Workers["iris"]
	w.StopWait = "3s"
	p.startCfg.Workers["iris"] = w
	host.promptErr = errors.New("terminal gone")
	p.bound = func(string) delivery.Binding { return delivery.Binding{} }
	srv.batches = [][]Operation{{stopOp(IssuedByPlatform)}}
	began := time.Now()
	step(t, p)
	if time.Since(began) > time.Second {
		t.Fatalf("a worker that was never asked to end waited %s for nothing", time.Since(began))
	}
	if len(host.stopped) != 1 {
		t.Fatalf("a failed end prompt leaves nothing to wait for: the stop is immediate (%v)", host.stopped)
	}
	if r := only(t, srv); !strings.Contains(r.reason, "not asked to end") {
		t.Fatalf("%+v", r)
	}
}

func TestWorkerEndManagerModeIgnoresThePlatformsOwnEnd(t *testing.T) {
	for issuer, want := range map[string]int{IssuedByPlatform: 0, "": 0, IssuedByUser: 1, IssuedByWorker: 0} {
		p, srv, host := testPoller(t)
		withStop(p, StopOnEndManager)
		p.bound = func(string) delivery.Binding { return delivery.Binding{} }
		srv.batches = [][]Operation{{stopOp(issuer)}}
		step(t, p)
		if len(host.stopped) != want {
			t.Errorf("issuer %q: %d stops, want %d", issuer, len(host.stopped), want)
		}
	}
}

func TestWorkerEndNeverTouchesATreeBoundElsewhere(t *testing.T) {
	for name, b := range map[string]delivery.Binding{
		"another worker":                {WorkerID: "id-other", SessionID: "sess1"},
		"another session of the worker": {WorkerID: "id-iris", SessionID: "sess2"},
		"an unreadable binding":         {Unusable: true},
	} {
		p, srv, host := testPoller(t)
		withStop(p, StopOnEndAlways)
		p.bound = func(string) delivery.Binding { return b }
		srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
		step(t, p)
		if len(host.stopped) != 0 {
			t.Errorf("%s: a process the host cannot identify by worktree and binding was touched", name)
		}
		if r := only(t, srv); !strings.Contains(r.reason, "not touched") {
			t.Errorf("%s: the report must say nothing was touched: %q", name, r.reason)
		}
	}
}

func TestWorkerEndStopsTheLaunchedAgentByItsOwnGroup(t *testing.T) {
	p, srv, host := testPoller(t)
	withStop(p, StopOnEndAlways)
	agent := &fakeAgent{done: make(chan struct{}), pid: 4242}
	p.trackAgent("id-iris", "repo/iris", agent)
	p.agentSessions["id-iris"] = "sess1"
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if agent.terminations() != 1 || len(host.stopped) != 0 {
		t.Fatalf("a launched agent is stopped by its launch group, not by the scan: terminated=%d scans=%v", agent.terminations(), host.stopped)
	}
	if r := only(t, srv); !strings.Contains(r.reason, "pid 4242") {
		t.Fatalf("%+v", r)
	}
	if _, ok := p.agentWorktree("id-iris"); ok {
		t.Error("a confirmed stop forgets the launch")
	}
}

func TestWorkerEndKeepsRecordsWhenTheStopIsUnconfirmed(t *testing.T) {
	p, srv, _ := testPoller(t)
	withStop(p, StopOnEndAlways)
	agent := &fakeAgent{done: make(chan struct{}), pid: 4242, stopFails: true}
	p.trackAgent("id-iris", "repo/iris", agent)
	p.agentSessions["id-iris"] = "sess1"
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if _, ok := p.agentWorktree("id-iris"); !ok {
		t.Error("an unconfirmed stop keeps the agent's records (the one-agent-per-worktree guard)")
	}
	if r := only(t, srv); !strings.Contains(r.reason, "could not be confirmed") {
		t.Fatalf("%+v", r)
	}
}

func TestConfigRefusesABadStopSwitch(t *testing.T) {
	c := Config{App: "a", Label: "l", WorktreesRoot: "/w",
		Workers: map[string]WorkerConfig{"iris": {Family: "claude", Launch: []string{"x"}, StopOnEnd: "sometimes"}}}
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "stop_on_end") {
		t.Fatalf("err = %v", err)
	}
	c.Workers["iris"] = WorkerConfig{Family: "claude", Launch: []string{"x"}, StopWait: "soon"}
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "stop_wait") {
		t.Fatalf("err = %v", err)
	}
}

// Blind P1: a LAUNCHED agent is held to the same binding rules as a scanned one.
func TestWorkerEndNeverStopsALaunchedAgentOfAnotherSession(t *testing.T) {
	for name, b := range map[string]delivery.Binding{
		"another worker":                {WorkerID: "id-other", SessionID: "sess1"},
		"another session of the worker": {WorkerID: "id-iris", SessionID: "sess2"},
		"an unreadable binding":         {Unusable: true},
	} {
		p, srv, _ := testPoller(t)
		withStop(p, StopOnEndAlways)
		agent := &fakeAgent{done: make(chan struct{}), pid: 4242}
		p.trackAgent("id-iris", "repo/iris", agent)
		p.agentSessions["id-iris"] = "sess1"
		p.bound = func(string) delivery.Binding { return b }
		srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
		step(t, p)
		if agent.terminations() != 0 {
			t.Errorf("%s: an end for an older session stopped the launched agent", name)
		}
		if r := only(t, srv); !strings.Contains(r.reason, "not touched") {
			t.Errorf("%s: %q", name, r.reason)
		}
	}
}

// Blind P1 (second): the binding is asked again right before the signal, so a
// replacement session that binds between discovery and the stop is left alone.
func TestWorkerEndRechecksTheBindingRightBeforeTheSignal(t *testing.T) {
	p, srv, host := testPoller(t)
	withStop(p, StopOnEndAlways)
	b := delivery.Binding{WorkerID: "id-iris", SessionID: "sess1"}
	p.bound = func(string) delivery.Binding { return b }
	host.stops = []AgentStop{{PID: 7, Stopped: true, Detail: "pid 7"}}
	host.betweenCheckAndStop = func() { b = delivery.Binding{WorkerID: "id-iris", SessionID: "sess2"} }
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if len(host.stopped) != 0 {
		t.Fatalf("a session that bound after the first check was stopped: %v", host.stopped)
	}
	if r := only(t, srv); !strings.Contains(r.reason, "binding moved on") {
		t.Fatalf("%q", r.reason)
	}
}

// Blind delta P1: a launched agent whose start bound a NEWER session than the
// one being ended is not stopped, even with the binding cleared.
func TestWorkerEndNeverStopsALaunchedAgentOfANewerSession(t *testing.T) {
	p, srv, _ := testPoller(t)
	withStop(p, StopOnEndAlways)
	agent := &fakeAgent{done: make(chan struct{}), pid: 4242}
	p.trackAgent("id-iris", "repo/iris", agent)
	p.agentSessions["id-iris"] = "sess-newer"
	p.bound = func(string) delivery.Binding { return delivery.Binding{} }
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if agent.terminations() != 0 {
		t.Fatal("an end for an older session stopped the agent of a newer one")
	}
	if r := only(t, srv); !strings.Contains(r.reason, "another session") {
		t.Fatalf("%q", r.reason)
	}
}

// ... and the session the start bound is the one that may be ended.
func TestWorkerEndStopsALaunchedAgentOfTheSessionBeingEnded(t *testing.T) {
	p, srv, _ := testPoller(t)
	withStop(p, StopOnEndAlways)
	agent := &fakeAgent{done: make(chan struct{}), pid: 4242}
	p.trackAgent("id-iris", "repo/iris", agent)
	p.agentSessions["id-iris"] = "sess1"
	p.bound = func(string) delivery.Binding { return delivery.Binding{} }
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if agent.terminations() != 1 {
		t.Fatal("the agent of the session being ended was not stopped")
	}
}

// Blind delta P1: launch metadata that is missing declines the stop.
func TestWorkerEndDeclinesALaunchedAgentWithNoRecordedWorktree(t *testing.T) {
	p, srv, _ := testPoller(t)
	withStop(p, StopOnEndAlways)
	agent := &fakeAgent{done: make(chan struct{}), pid: 4242}
	p.agents["id-iris"] = agent // tracked in memory only: no worktree recorded anywhere
	p.bound = func(string) delivery.Binding { return delivery.Binding{} }
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if agent.terminations() != 0 {
		t.Fatal("an agent that could not be identified was stopped")
	}
	if r := only(t, srv); !strings.Contains(r.reason, "not recorded") {
		t.Fatalf("%q", r.reason)
	}
}

// Blind delta P1: identity that is not available declines the stop, on either
// side: a launch whose session was not recorded, or an end that names none.
func TestWorkerEndDeclinesALaunchedAgentWhenASessionIsUnknown(t *testing.T) {
	for name, tc := range map[string]struct{ launched, end string }{
		"no recorded launch session": {"", "sess1"},
		"an end naming no session":   {"sess1", ""},
	} {
		p, srv, _ := testPoller(t)
		withStop(p, StopOnEndAlways)
		agent := &fakeAgent{done: make(chan struct{}), pid: 4242}
		p.trackAgent("id-iris", "repo/iris", agent)
		if tc.launched != "" {
			p.agentSessions["id-iris"] = tc.launched
		}
		p.bound = func(string) delivery.Binding { return delivery.Binding{} }
		op := stopOp(IssuedByUser)
		op.Args = map[string]any{"workerId": "id-iris", "sessionId": tc.end}
		srv.batches = [][]Operation{{op}}
		step(t, p)
		if agent.terminations() != 0 {
			t.Errorf("%s: an agent that could not be identified was stopped", name)
		}
		if r := only(t, srv); !strings.Contains(r.reason, "could not be identified") {
			t.Errorf("%s: %q", name, r.reason)
		}
	}
}

// Jill's local P3 on da901deb: the production write of the launch's session is
// pinned. A start that binds session sessA, then an end naming sessA stops the
// agent; an end naming another session does not.
func TestWorkerEndUsesTheSessionTheStartBound(t *testing.T) {
	for name, tc := range map[string]struct {
		end  string
		want int
	}{"the bound session": {"sessA", 1}, "another session": {"sessB", 0}} {
		p, srv, host := testPoller(t)
		withStop(p, StopOnEndAlways)
		p.bound = func(string) delivery.Binding { return delivery.Binding{} }
		srv.startSession = "sessA"
		srv.notDone["s1"] = 1
		srv.batches = [][]Operation{{startOp()}}
		step(t, p)
		if host.agent == nil {
			t.Fatal("no agent launched")
		}
		end := stopOp(IssuedByUser)
		end.Args = map[string]any{"workerId": "id-iris", "sessionId": tc.end}
		srv.batches = [][]Operation{{end}}
		step(t, p)
		if got := host.agent.terminations(); got != tc.want {
			t.Errorf("%s: terminations = %d, want %d", name, got, tc.want)
		}
	}
}

// #2070: the context-reset relaunch records the session the reset names (a
// reset keeps the session), so a stopping end can identify that agent.
func TestRelaunchRecordsTheSessionTheResetNames(t *testing.T) {
	x, _ := relaunchFixture(t)
	op := resetOp()
	if _, code, detail := x.p.relaunchAgent(context.Background(), op, irisCfg(x), "repo/iris", oldIdentity, "hrn:node:acme.com:iris:sheds:auto-1"); code != "" {
		t.Fatalf("%s: %s", code, detail)
	}
	x.p.mu.Lock()
	got := x.p.agentSessions["id-iris"]
	x.p.mu.Unlock()
	if want := argString(op, "sessionId"); want == "" || got != want {
		t.Fatalf("recorded session %q, want %q", got, want)
	}
}

// plainAgent implements only the original Agent interface: it cannot be
// stopped under the binding lock.
type plainAgent struct {
	done       chan struct{}
	terminated int
}

func (a *plainAgent) Done() <-chan struct{} { return a.done }
func (a *plainAgent) ExitDetail() string    { return "" }
func (a *plainAgent) PID() int              { return 4343 }
func (a *plainAgent) Terminate() (bool, string) {
	a.terminated++
	return true, ""
}

// Blind P2: an agent that cannot be stopped under the lock is declined, never
// stopped through an unguarded Terminate.
func TestWorkerEndDeclinesAnAgentThatCannotStopUnderTheLock(t *testing.T) {
	p, srv, _ := testPoller(t)
	withStop(p, StopOnEndAlways)
	agent := &plainAgent{done: make(chan struct{})}
	p.trackAgent("id-iris", "repo/iris", agent)
	p.agentSessions["id-iris"] = "sess1"
	p.bound = func(string) delivery.Binding { return delivery.Binding{} }
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if agent.terminated != 0 {
		t.Fatal("an agent without an authorized stop was terminated")
	}
	if r := only(t, srv); !strings.Contains(r.reason, "cannot be stopped under") {
		t.Fatalf("%q", r.reason)
	}
}

// Jill's local P2 on 7a7aa948: a run that exited on its own leaves its session
// recorded, and the next launch must not inherit it.
func TestTrackAgentDoesNotInheritAnEarlierRunsSession(t *testing.T) {
	p, srv, _ := testPoller(t)
	withStop(p, StopOnEndAlways)
	first := &fakeAgent{done: make(chan struct{}), pid: 4100}
	p.trackAgent("id-iris", "repo/iris", first)
	p.agentSessions["id-iris"] = "sess-old"
	close(first.done) // exited on its own
	second := &fakeAgent{done: make(chan struct{}), pid: 4200}
	p.trackAgent("id-iris", "repo/iris", second) // a launch whose session is not recorded
	p.bound = func(string) delivery.Binding { return delivery.Binding{WorkerID: "id-iris", SessionID: "sess1"} }
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if second.terminations() != 1 {
		t.Fatalf("the second run was declined by the first run's session (terminations=%d): identification by binding never ran", second.terminations())
	}
	if _, left := p.agentSessions["id-iris"]; left {
		t.Error("a stale session survived the new launch")
	}
}

// §10 F2: an unrecorded launch is identified again UNDER the lock, immediately
// before each signal: a binding that empties after the first check declines.
func TestWorkerEndRechecksAnUnrecordedLaunchUnderTheLock(t *testing.T) {
	p, srv, _ := testPoller(t)
	withStop(p, StopOnEndAlways)
	agent := &fakeAgent{done: make(chan struct{}), pid: 4242}
	p.trackAgent("id-iris", "repo/iris", agent) // no recorded session
	b := delivery.Binding{WorkerID: "id-iris", SessionID: "sess1"}
	p.bound = func(string) delivery.Binding { return b }
	p.lockBinding = func(string, time.Duration) (func(), error) {
		b = delivery.Binding{} // the binding goes empty before the signal
		return func() {}, nil
	}
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if agent.terminations() != 0 {
		t.Fatal("an unrecorded launch was stopped after its binding stopped identifying it")
	}
}
