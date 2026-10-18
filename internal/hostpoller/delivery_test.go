package hostpoller

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

type fakeBatches struct {
	mu    sync.Mutex
	calls int
	queue [][]delivery.Batch
	err   error
}

func (f *fakeBatches) Batches(context.Context) ([]delivery.Batch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if len(f.queue) == 0 {
		return nil, nil
	}
	b := f.queue[0]
	f.queue = f.queue[1:]
	return b, nil
}

type delivered struct {
	batch delivery.Batch
	to    delivery.Recipient
}

type fakeDeliverer struct {
	mu    sync.Mutex
	got   []delivered
	gate  chan struct{}
	enter chan struct{}
}

func (f *fakeDeliverer) Deliver(_ context.Context, b delivery.Batch, r delivery.Recipient) delivery.Outcome {
	if f.gate != nil {
		if f.enter != nil {
			close(f.enter)
			f.enter = nil
		}
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, delivered{b, r})
	return delivery.Outcome{Kind: delivery.Delivered}
}

func (f *fakeDeliverer) deliveries() []delivered {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]delivered{}, f.got...)
}

func batchFor(worker string, seq int) delivery.Batch {
	return delivery.Batch{ID: "proof", BatchKey: worker + "-" + string(rune('a'+seq)), WorkerID: worker,
		Ranges: []delivery.Range{{ChannelID: "ch", Incarnation: 1, FromSeq: seq, ThroughSeq: seq, Seqs: []int{seq}}},
		Text:   "[hadron chat #1] x"}
}

func deliveryPoller(t *testing.T) (*Poller, *fakeServer, *fakeHost, *fakeBatches, *fakeDeliverer) {
	t.Helper()
	p, srv, host := testPoller(t)
	src, d := &fakeBatches{}, &fakeDeliverer{}
	p.EnableDelivery(src, d)
	return p, srv, host, src, d
}

func TestDeliveryIsOffUnlessEnabled(t *testing.T) {
	p, _, _ := testPoller(t)
	step(t, p) // no source: nothing to call, nothing to panic on
	if p.DeliveryEnabled() {
		t.Fatal("delivery on without EnableDelivery")
	}
}

func TestDeliversToTheRegisteredWorkerInItsRegisteredWorktrees(t *testing.T) {
	p, _, _, src, d := deliveryPoller(t)
	src.queue = [][]delivery.Batch{{batchFor("id-iris", 5)}}
	step(t, p)
	got := d.deliveries()
	if len(got) != 1 {
		t.Fatalf("deliveries %+v", got)
	}
	r := got[0].to
	if r.WorkerID != "id-iris" || r.Name != "iris" || r.TUI != "claude" ||
		len(r.Worktrees) != 1 || r.Worktrees[0] != p.cfg.WorktreePath("repo/iris") {
		t.Fatalf("recipient %+v", r)
	}
}

func TestABatchForAnUnregisteredWorkerIsNotDelivered(t *testing.T) {
	p, _, _, src, d := deliveryPoller(t)
	events := recordEvents(p)
	src.queue = [][]delivery.Batch{{batchFor("id-stranger", 5)}}
	step(t, p)
	if len(d.deliveries()) != 0 {
		t.Fatalf("delivered to a stranger: %+v", d.deliveries())
	}
	if !hasEvent(events(), "delivery-skipped", "not registered on this host") {
		t.Fatalf("events %+v", events())
	}
}

// recordEvents collects the poller's events, which arrive from its
// goroutines.
func recordEvents(p *Poller) func() []Event {
	var mu sync.Mutex
	var events []Event
	p.emit = func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}
	return func() []Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]Event{}, events...)
	}
}

func hasEvent(events []Event, kind, detail string) bool {
	for _, e := range events {
		if e.Kind == kind && strings.Contains(e.Detail, detail) {
			return true
		}
	}
	return false
}

func TestAMissingScopeSwitchesDeliveryOffForTheRunOnly(t *testing.T) {
	p, srv, _, src, _ := deliveryPoller(t)
	events := recordEvents(p)
	src.err = ErrDeliveryScope
	srv.batches = [][]Operation{{}, {{ID: "o1", Type: "shell.exec"}}}
	step(t, p)
	step(t, p)
	if src.calls != 1 {
		t.Fatalf("batches read %d times after the scope refusal", src.calls)
	}
	if p.DeliveryEnabled() {
		t.Fatal("delivery still on")
	}
	if len(srv.finalReports()) != 1 {
		t.Fatal("host operations must keep running")
	}
	if !hasEvent(events(), "delivery-off", "attention:deliver") {
		t.Fatalf("events %+v", events())
	}
}

func TestAFailedBatchReadIsLoggedAndRetriedNextStep(t *testing.T) {
	p, _, _, src, _ := deliveryPoller(t)
	src.err = errors.New("gateway 502")
	if err := p.Step(context.Background()); err != nil {
		t.Fatalf("a failed batch read must not fail the step: %v", err)
	}
	p.Wait()
	src.err = nil
	step(t, p)
	if src.calls != 2 || !p.DeliveryEnabled() {
		t.Fatalf("calls %d enabled %v", src.calls, p.DeliveryEnabled())
	}
}

type failingRegister struct{ *fakeServer }

func (failingRegister) Register(context.Context, Registration) (RegisteredHost, error) {
	return RegisteredHost{}, errors.New("refused")
}

func TestNoDeliveryWithoutThisStepsRegistration(t *testing.T) {
	p, srv, _, src, _ := deliveryPoller(t)
	p.server = failingRegister{srv}
	src.queue = [][]delivery.Batch{{batchFor("id-iris", 5)}}
	if err := p.Step(context.Background()); err == nil {
		t.Fatal("want the registration error")
	}
	if src.calls != 0 {
		t.Fatal("batches read without a registration this step")
	}
}

func TestADeliveryWaitsForTheWorkersOperationInFlight(t *testing.T) {
	p, srv, host, src, d := deliveryPoller(t)
	host.gate, host.entered = make(chan struct{}), make(chan struct{})
	srv.notDone["s1"] = 1 // not bound at the pre-check: the start launches
	srv.batches = [][]Operation{{startOp()}}
	src.queue = [][]delivery.Batch{{batchFor("id-iris", 5)}}
	go func() { _ = p.Step(context.Background()) }()
	<-host.entered
	time.Sleep(20 * time.Millisecond)
	if len(d.deliveries()) != 0 {
		t.Fatal("a delivery ran while the worker's start was in flight")
	}
	close(host.gate)
	p.Wait()
	if len(d.deliveries()) != 1 {
		t.Fatalf("deliveries %+v", d.deliveries())
	}
}

func TestABatchInFlightIsNotDeliveredTwice(t *testing.T) {
	p, _, _, src, d := deliveryPoller(t)
	d.gate, d.enter = make(chan struct{}), make(chan struct{})
	b := batchFor("id-iris", 5)
	src.queue = [][]delivery.Batch{{b}, {b}}
	entered := d.enter
	if err := p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(d.gate)
		t.Fatal("the first delivery never started")
	}
	if err := p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(d.gate)
	p.Wait()
	if n := len(d.deliveries()); n != 1 {
		t.Fatalf("delivered %d times", n)
	}
}

func TestAStoppingPollerStartsNoDelivery(t *testing.T) {
	p, _, _, src, d := deliveryPoller(t)
	src.queue = [][]delivery.Batch{{batchFor("id-iris", 5)}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = p.Step(ctx)
	p.Wait()
	if len(d.deliveries()) != 0 {
		t.Fatal("delivered while stopping")
	}
}

func TestDeliveryConfig(t *testing.T) {
	base := func() *Config {
		return &Config{App: "a", Label: "l", WorktreesRoot: "/w", StateFile: "/s/state.json",
			Workers: map[string]WorkerConfig{"iris": {Family: "glm", Launch: []string{"x"}}}}
	}
	c := base()
	c.Delivery = DeliveryConfig{Enabled: true}
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "orca_bin") {
		t.Fatalf("enabled without orca_bin: %v", err)
	}
	c = base()
	c.Delivery = DeliveryConfig{Enabled: true, OrcaBin: "orca"}
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("a relative orca_bin: %v", err)
	}
	c = base()
	w := c.Workers["iris"]
	w.TUI = "vim"
	c.Workers["iris"] = w
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "tui") {
		t.Fatalf("an unknown tui: %v", err)
	}
	c = base()
	c.Delivery = DeliveryConfig{Enabled: true, OrcaBin: "/usr/local/bin/orca"}
	w = c.Workers["iris"]
	w.TUI = "claude"
	c.Workers["iris"] = w
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if got := c.DeliveryStateDir(); got != "/s" {
		t.Fatalf("delivery state beside the state file, got %q", got)
	}
}
