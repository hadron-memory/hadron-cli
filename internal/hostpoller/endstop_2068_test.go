package hostpoller

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// hadron-cli#2068: the stop holds the worktree's binding lock from its final
// check through each signal, so a bind cannot land between them.

func realTree(t *testing.T, p *Poller) string {
	t.Helper()
	dir := p.cfg.WorktreePath("repo/iris")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWorkerEndHoldsTheBindingLockAtTheMomentOfTheSignal(t *testing.T) {
	p, srv, host := testPoller(t)
	withStop(p, StopOnEndAlways)
	dir := realTree(t, p)
	p.bound = func(string) delivery.Binding { return delivery.Binding{} }
	var busyDuring, freeAfter bool
	host.whileHeld = func() {
		// A bind (session start) arriving right now.
		_, err := delivery.LockBinding(dir, 50*time.Millisecond)
		busyDuring = errors.Is(err, delivery.ErrBindingLockBusy)
	}
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if len(host.stopped) != 1 {
		t.Fatalf("stopped = %v", host.stopped)
	}
	if !busyDuring {
		t.Error("a writer could take the binding lock while the signal was being sent")
	}
	rel, err := delivery.LockBinding(dir, time.Second)
	if err == nil {
		rel()
		freeAfter = true
	}
	if !freeAfter {
		t.Error("the lock must be released right after the signal: nothing may hold it across a wait")
	}
}

func TestWorkerEndDeclinesWhenTheBindingLockCannotBeTaken(t *testing.T) {
	p, srv, host := testPoller(t)
	withStop(p, StopOnEndAlways)
	p.bound = func(string) delivery.Binding { return delivery.Binding{} }
	p.lockBinding = func(string, time.Duration) (func(), error) { return nil, delivery.ErrBindingLockBusy }
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if len(host.stopped) != 0 {
		t.Fatalf("signalled without the lock: %v", host.stopped)
	}
	if r := only(t, srv); !strings.Contains(r.reason, "binding moved on") {
		t.Fatalf("%q", r.reason)
	}
}

// A binding written between the first check and the lock is seen under it: the
// check runs AFTER the lock is held, so a bind that landed first declines the
// stop, and one that comes later waits for the release.
func TestWorkerEndChecksTheBindingUnderTheLock(t *testing.T) {
	p, srv, host := testPoller(t)
	withStop(p, StopOnEndAlways)
	b := delivery.Binding{WorkerID: "id-iris", SessionID: "sess1"}
	p.bound = func(string) delivery.Binding { return b }
	locked := false
	p.lockBinding = func(string, time.Duration) (func(), error) {
		locked = true
		b = delivery.Binding{WorkerID: "id-iris", SessionID: "sess2"} // the bind that won the lock first
		return func() {}, nil
	}
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if !locked || len(host.stopped) != 0 {
		t.Fatalf("locked=%v stopped=%v: the binding was read before the lock, not under it", locked, host.stopped)
	}
}

func TestWorkerEndTerminatesALaunchedAgentThroughTheLockedAuthorization(t *testing.T) {
	p, srv, _ := testPoller(t)
	withStop(p, StopOnEndAlways)
	dir := realTree(t, p)
	agent := &fakeAgent{done: make(chan struct{}), pid: 4242}
	p.trackAgent("id-iris", "repo/iris", agent)
	p.agentSessions["id-iris"] = "sess1"
	p.bound = func(string) delivery.Binding { return delivery.Binding{} }
	took := 0
	real := p.lockBinding
	p.lockBinding = func(path string, wait time.Duration) (func(), error) { took++; return real(path, wait) }
	srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
	step(t, p)
	if agent.terminations() != 1 || took == 0 {
		t.Fatalf("terminations=%d lockTaken=%d: the launched agent must be stopped under the binding lock", agent.terminations(), took)
	}
	if rel, err := delivery.LockBinding(dir, time.Second); err != nil {
		t.Errorf("the lock outlived the stop: %v", err)
	} else {
		rel()
	}
}

// #2070: an agent whose start the server had already finished has no recorded
// session; only a binding naming this worker and this session identifies it.
func TestWorkerEndIdentifiesAnUnrecordedLaunchByItsBinding(t *testing.T) {
	for name, tc := range map[string]struct {
		b    delivery.Binding
		want int
	}{
		"binding names the worker and session": {delivery.Binding{WorkerID: "id-iris", SessionID: "sess1"}, 1},
		"an empty binding":                     {delivery.Binding{}, 0},
		"another session":                      {delivery.Binding{WorkerID: "id-iris", SessionID: "sess2"}, 0},
		"another worker":                       {delivery.Binding{WorkerID: "id-other", SessionID: "sess1"}, 0},
		"no session in the binding":            {delivery.Binding{WorkerID: "id-iris"}, 0},
		"an unreadable binding":                {delivery.Binding{Unusable: true}, 0},
	} {
		p, srv, _ := testPoller(t)
		withStop(p, StopOnEndAlways)
		agent := &fakeAgent{done: make(chan struct{}), pid: 4242}
		p.trackAgent("id-iris", "repo/iris", agent) // no agentSessions entry: the already-finished path
		b := tc.b
		p.bound = func(string) delivery.Binding { return b }
		srv.batches = [][]Operation{{stopOp(IssuedByUser)}}
		step(t, p)
		if got := agent.terminations(); got != tc.want {
			t.Errorf("%s: terminations = %d, want %d", name, got, tc.want)
		}
	}
}
