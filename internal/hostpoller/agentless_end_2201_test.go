package hostpoller

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// hadron-cli#2201 C10: when a short-lived worker's worker.end finds no agent,
// the host proves it with the strict census (the one C5 relaunches on) and
// reports FAILED with the closed reason AGENT_ABSENT, which the server turns
// into the end (hadron-server#4292). Anything short of a proof reports as
// before, with the census result in the reason, and never AGENT_ABSENT.

const agentAbsentCode = "AGENT_ABSENT"

// censusStub answers the strict census per worktree and records who asked.
type censusStub struct {
	mu    sync.Mutex
	obs   hostrelaunch.Observation
	err   error
	byDir map[string]hostrelaunch.Observation
	asked []string
}

func (c *censusStub) probe(_ context.Context, t hostrelaunch.Target) (hostrelaunch.Observation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, t.Worktree)
	if c.err != nil {
		return hostrelaunch.Observation{}, c.err
	}
	if o, ok := c.byDir[t.Worktree]; ok {
		return o, nil
	}
	return c.obs, nil
}

func (c *censusStub) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.asked)
}

// agentlessEndPoller is a short-lived worker (stop_on_end: always) with no end
// prompt configured: the orca1 test run 2 shape (AT0.7687).
func agentlessEndPoller(t *testing.T, census *censusStub) (*Poller, *fakeServer, *fakeHost) {
	t.Helper()
	p, srv, host := testPoller(t)
	w := p.cfg.Workers["iris"]
	w.StopOnEnd, w.StopWait, w.End = StopOnEndAlways, "20ms", nil
	p.cfg.Workers["iris"] = w
	p.bound = func(string) delivery.Binding { return delivery.Binding{} }
	p.agentCensus = census.probe
	srv.batches = [][]Operation{{stopOp(IssuedByPlatform)}}
	return p, srv, host
}

func provenEmpty() hostrelaunch.Observation {
	return hostrelaunch.Observation{Proven: true, PIDs: []int{}}
}

func TestWorkerEndAgentAbsentWhenTheCensusProvesNoAgent2201(t *testing.T) {
	census := &censusStub{obs: provenEmpty()}
	p, srv, host := agentlessEndPoller(t, census)
	step(t, p)
	r := only(t, srv)
	if r.outcome != Failed {
		t.Fatalf("the host still reports FAILED (it ends no session itself): %+v", r)
	}
	if !strings.HasPrefix(r.reason, agentAbsentCode) {
		t.Fatalf("the reason must LEAD with the closed code the server reads: %q", r.reason)
	}
	if census.calls() != 1 || !strings.HasSuffix(census.asked[0], "repo/iris") {
		t.Fatalf("the census must run once, in the worker's worktree: %v", census.asked)
	}
	if len(host.stopped) != 0 {
		t.Fatalf("nothing runs, so nothing is scanned for or stopped: %v", host.stopped)
	}
}

func TestWorkerEndNeverAgentAbsentWithoutAProof2201(t *testing.T) {
	cases := map[string]*censusStub{
		"census unavailable":         {err: hostrelaunch.ErrCensusUnavailable},
		"census errored":             {err: errors.New("proc is not the kernel proc filesystem")},
		"observation not proven":     {obs: hostrelaunch.Observation{Proven: false, PIDs: []int{}}},
		"an agent found":             {obs: hostrelaunch.Observation{Proven: true, PIDs: []int{7}}},
		"several agents found":       {obs: hostrelaunch.Observation{Proven: true, PIDs: []int{7, 9}}},
		"zero value observation":     {},
		"not proven, no PIDs listed": {obs: hostrelaunch.Observation{PIDs: []int{}}},
		"proven, one agent (pid 3)":  {obs: hostrelaunch.Observation{Proven: true, PIDs: []int{3}}},
	}
	for name, census := range cases {
		p, srv, _ := agentlessEndPoller(t, census)
		step(t, p)
		r := only(t, srv)
		if strings.Contains(r.reason, agentAbsentCode+":") || strings.HasPrefix(r.reason, agentAbsentCode) {
			t.Errorf("%s: AGENT_ABSENT without a proof: %q", name, r.reason)
		}
		if !strings.Contains(r.reason, "census") {
			t.Errorf("%s: the census result must be in the reason: %q", name, r.reason)
		}
	}
}

func TestWorkerEndNamesTheAgentTheCensusFound2201(t *testing.T) {
	p, srv, _ := agentlessEndPoller(t, &censusStub{obs: hostrelaunch.Observation{Proven: true, PIDs: []int{4242}}})
	step(t, p)
	if r := only(t, srv); !strings.Contains(r.reason, "4242") {
		t.Fatalf("an agent found must be named by pid: %q", r.reason)
	}
}

func TestWorkerEndLongLivedNeverAgentAbsent2201(t *testing.T) {
	for _, mode := range []string{"", StopOnEndNever, StopOnEndManager} {
		census := &censusStub{obs: provenEmpty()}
		p, srv, _ := agentlessEndPoller(t, census)
		w := p.cfg.Workers["iris"]
		w.StopOnEnd = mode
		p.cfg.Workers["iris"] = w
		step(t, p)
		if r := only(t, srv); strings.HasPrefix(r.reason, agentAbsentCode) {
			t.Errorf("stop_on_end %q is not a short-lived worker: %q", mode, r.reason)
		}
		if census.calls() != 0 {
			t.Errorf("stop_on_end %q: a long-lived worker is not even censused (%d)", mode, census.calls())
		}
	}
}

func TestWorkerEndLiveLaunchedAgentIsNotAbsent2201(t *testing.T) {
	census := &censusStub{obs: provenEmpty()}
	p, srv, _ := agentlessEndPoller(t, census)
	agent := &fakeAgent{done: make(chan struct{}), pid: 4242}
	p.trackAgent("id-iris", "repo/iris", agent)
	p.agentSessions["id-iris"] = "sess1"
	step(t, p)
	if r := only(t, srv); strings.HasPrefix(r.reason, agentAbsentCode) {
		t.Fatalf("an agent this host launched is running: %q", r.reason)
	}
	if census.calls() != 0 {
		t.Fatalf("a known live agent needs no census: %d", census.calls())
	}
}

func TestWorkerEndNoRegisteredWorktreeIsUnproven2201(t *testing.T) {
	census := &censusStub{obs: provenEmpty()}
	p, srv, _ := agentlessEndPoller(t, census)
	w := p.cfg.Workers["iris"]
	w.Worktrees = nil
	p.cfg.Workers["iris"] = w
	step(t, p)
	if r := only(t, srv); strings.HasPrefix(r.reason, agentAbsentCode) {
		t.Fatalf("no worktree to look in proves nothing: %q", r.reason)
	}
}

func TestWorkerEndEveryWorktreeMustBeProvenEmpty2201(t *testing.T) {
	census := &censusStub{obs: provenEmpty()}
	p, srv, _ := agentlessEndPoller(t, census)
	w := p.cfg.Workers["iris"]
	w.Worktrees = []string{"repo/iris", "repo/iris2"}
	p.cfg.Workers["iris"] = w
	census.byDir = map[string]hostrelaunch.Observation{
		p.cfg.WorktreePath("repo/iris2"): {Proven: true, PIDs: []int{55}},
	}
	step(t, p)
	if r := only(t, srv); strings.HasPrefix(r.reason, agentAbsentCode) {
		t.Fatalf("an agent in the second worktree: %q", r.reason)
	}
}

func TestWorkerEndAnEndWithNoSessionIsNotAgentAbsent2201(t *testing.T) {
	census := &censusStub{obs: provenEmpty()}
	p, srv, _ := agentlessEndPoller(t, census)
	op := stopOp(IssuedByPlatform)
	op.Args = map[string]any{"workerId": "id-iris"}
	srv.batches = [][]Operation{{op}}
	step(t, p)
	if r := only(t, srv); strings.HasPrefix(r.reason, agentAbsentCode) {
		t.Fatalf("with no session named there is nothing for the server to end: %q", r.reason)
	}
}

// #2207 BLIND F2: a remembered launch worktree must not narrow the census. The
// record outlives its agent's exit, so an exited launch in one tree must not
// hide a live agent in another registered tree.
func TestWorkerEndRememberedLaunchTreeDoesNotNarrowTheCensus2201(t *testing.T) {
	census := &censusStub{obs: provenEmpty()}
	p, srv, _ := agentlessEndPoller(t, census)
	w := p.cfg.Workers["iris"]
	w.Worktrees = []string{"repo/iris", "repo/iris2"}
	p.cfg.Workers["iris"] = w
	census.byDir = map[string]hostrelaunch.Observation{
		p.cfg.WorktreePath("repo/iris2"): {Proven: true, PIDs: []int{55}},
	}
	exited := &fakeAgent{done: make(chan struct{}), pid: 4242}
	close(exited.done)
	p.trackAgent("id-iris", "repo/iris", exited)
	step(t, p)
	if r := only(t, srv); strings.HasPrefix(r.reason, agentAbsentCode) {
		t.Fatalf("an agent runs in the second registered worktree: %q", r.reason)
	}
	if census.calls() != 2 {
		t.Fatalf("both registered trees must be censused (once each): %v", census.asked)
	}
}

func TestWorkerEndCensusesTheUnionOnceEach2201(t *testing.T) {
	census := &censusStub{obs: provenEmpty()}
	p, srv, _ := agentlessEndPoller(t, census)
	w := p.cfg.Workers["iris"]
	w.Worktrees = []string{"repo/iris", "repo/iris2"}
	p.cfg.Workers["iris"] = w
	exited := &fakeAgent{done: make(chan struct{}), pid: 4242}
	close(exited.done)
	p.trackAgent("id-iris", "repo/iris", exited)
	step(t, p)
	if r := only(t, srv); !strings.HasPrefix(r.reason, agentAbsentCode) {
		t.Fatalf("every tree proven empty: %q", r.reason)
	}
	if census.calls() != 2 {
		t.Fatalf("the remembered tree is one of the registered ones, not a third: %v", census.asked)
	}
}

// The remembered launch tree is censused too when it is not a registered one:
// an agent this host launched there is an agent in the worker's reach.
func TestWorkerEndRememberedTreeOutsideTheRegistrationIsCensused2201(t *testing.T) {
	census := &censusStub{obs: provenEmpty()}
	p, srv, _ := agentlessEndPoller(t, census)
	census.byDir = map[string]hostrelaunch.Observation{
		p.cfg.WorktreePath("repo/ghost"): {Proven: true, PIDs: []int{88}},
	}
	exited := &fakeAgent{done: make(chan struct{}), pid: 4242}
	close(exited.done)
	p.trackAgent("id-iris", "repo/ghost", exited)
	step(t, p)
	if r := only(t, srv); strings.HasPrefix(r.reason, agentAbsentCode) || !strings.Contains(r.reason, "88") {
		t.Fatalf("an agent in the remembered tree: %q", r.reason)
	}
}
