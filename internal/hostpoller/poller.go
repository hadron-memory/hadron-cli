package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The closed set of operation types a host carries out (cor:agt:070:03).
// Anything else is refused here as well as by the server, and there is no
// shell passthrough of any kind.
const (
	OpWorktreeCreate = "worktree.create"
	OpWorkerStart    = "worker.start"
	OpWorkerDeliver  = "worker.deliver"
	OpWorkerEnd      = "worker.end"
	OpWorktreeRemove = "worktree.remove"
)

// Outcomes a host reports.
const (
	Succeeded = "SUCCEEDED"
	Failed    = "FAILED"
)

// reportTimeout bounds one report, which is sent even while stopping.
const reportTimeout = 30 * time.Second

// LaunchGrantTTL is how long a collected start's grant lives (#1883,
// Proposed). Waiting for the bind longer than this cannot succeed.
const LaunchGrantTTL = 10 * time.Minute

// Operation is a collected host operation.
type Operation struct {
	ID            string
	Type          string
	Args          map[string]any
	WorkerID      string
	Status        string
	Result        map[string]any
	FailureReason string
	// LaunchGrant is set only on a collected worker.start.
	LaunchGrant string
}

// Registration is one registerHost call: the whole worker list.
type Registration struct {
	Label               string
	PollIntervalSeconds int
	Workers             []RegisteredWorker
}

// RegisteredWorker is one worker in a registration.
type RegisteredWorker struct {
	Worker    string // the configured reference
	Family    string
	Worktrees []string
}

// RegisteredHost is the server's answer to a registration: which worker id
// each configured reference resolved to.
type RegisteredHost struct {
	HostID  string
	Workers []RegisteredHostWorker
}

// RegisteredHostWorker pairs a resolved worker id with its name.
type RegisteredHostWorker struct {
	WorkerID string
	URN      string
	Name     string
}

// Server is the host's four server calls, made with the host key.
type Server interface {
	Register(ctx context.Context, r Registration) (RegisteredHost, error)
	Collect(ctx context.Context) ([]Operation, error)
	// Report returns the operation as the server recorded it.
	Report(ctx context.Context, id, outcome, worktree, reason string) (Operation, error)
}

// ErrNotDone is a Report refusal meaning a worker.start's session is not
// bound yet (HOST_OPERATION_NOT_DONE); the poller asks again.
var ErrNotDone = errors.New("no session bound with this start's grant yet")

// ErrFinal is a Report refusal meaning the operation is already finished
// server-side (HOST_OPERATION_ALREADY_REPORTED): asking again cannot change
// it.
var ErrFinal = errors.New("the operation is already finished")

// Event is one line of what the poller did, for the operator. The --json
// shape is the command package's DTO, not this.
type Event struct {
	Time      time.Time
	Kind      string
	Operation string
	Type      string
	Outcome   string
	Detail    string
}

// Poller is one host's loop.
type Poller struct {
	cfg    *Config
	server Server
	host   Host
	state  *State
	key    string
	emit   func(Event)
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
	// BindPoll is how often a pending start asks whether its session bound.
	BindPoll time.Duration
	// Fatal, when set, decides that a failed check-in ends Run: a refused or
	// revoked host key does not heal by retrying, and looping on it would
	// hide it.
	Fatal func(error) bool

	mu sync.Mutex
	// inFlight are operations being carried out; a re-collection returns
	// them again until they are reported, and they must not run twice.
	inFlight map[string]bool
	// reportFailures counts reports that did not reach the server.
	reportFailures int
	// tails is each worker's last dispatched operation: the next one for
	// that worker waits for it.
	tails map[string]chan struct{}
	// workerRefs maps a resolved worker id to its configured reference, from
	// the last registration's answer.
	workerRefs map[string]string

	// DeliveryStep, when set, is told how each delivery step's batch read
	// went (delivery.StepRead, StepReadFailed, StepOffByScope).
	DeliveryStep func(at time.Time, read string)
	// agents are the agent processes this poller launched, by worker id.
	agents map[string]Agent
	// agentTrees is the worktree each launched agent runs in, by worker id.
	agentTrees map[string]string
	wg         sync.WaitGroup

	// Chat delivery (cli#907): off unless EnableDelivery, and switched off
	// for the run by a missing attention:deliver scope.
	batches     BatchSource
	deliverer   Deliverer
	deliveryOff bool
}

// New builds a poller. key is the host key, kept only to scrub it from
// launched agents' environments (defence in depth: it is never put there).
func New(cfg *Config, server Server, host Host, state *State, key string, emit func(Event)) *Poller {
	return &Poller{
		cfg: cfg, server: server, host: host, state: state, key: key, emit: emit,
		now:        time.Now,
		sleep:      sleepCtx,
		BindPoll:   5 * time.Second,
		inFlight:   map[string]bool{},
		tails:      map[string]chan struct{}{},
		workerRefs: map[string]string{},
		agents:     map[string]Agent{},
		agentTrees: map[string]string{},
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (p *Poller) log(kind, op, typ, outcome, detail string) {
	if p.emit != nil {
		p.emit(Event{Time: p.now().UTC(), Kind: kind, Operation: op, Type: typ, Outcome: outcome, Detail: detail})
	}
}

// Run polls until ctx ends, then waits for operations in flight.
func (p *Poller) Run(ctx context.Context) error {
	interval := time.Duration(p.cfg.PollIntervalSeconds) * time.Second
	for {
		if err := p.Step(ctx); err != nil {
			p.log("error", "", "", "", err.Error())
			if p.Fatal != nil && p.Fatal(err) {
				p.wg.Wait()
				return err
			}
		}
		if err := p.sleep(ctx, interval); err != nil {
			p.wg.Wait()
			return nil
		}
	}
}

// Wait blocks until every dispatched operation has been carried out.
func (p *Poller) Wait() { p.wg.Wait() }

// ReportFailures is how many reports did not reach the server; those
// operations are re-delivered at the next collect.
func (p *Poller) ReportFailures() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reportFailures
}

// Step is one check-in: register (always — a host that stops registering
// lapses), then collect and dispatch.
//
// A worker.start waiting for its bind is re-delivered by every collect and
// skipped here as in flight. It must not come back with a fresh grant: that
// would revoke the one its agent was handed. The server stops doing that
// (Liam, team chat #11415: a re-collect omits a start whose grant is still
// live), and this poller depends on it rather than pausing collection.
func (p *Poller) Step(ctx context.Context) error {
	if err := p.register(ctx); err != nil {
		return fmt.Errorf("registering: %w", err)
	}
	ops, err := p.server.Collect(ctx)
	if err != nil {
		return fmt.Errorf("collecting: %w", err)
	}
	for _, op := range ops {
		p.dispatch(ctx, op)
	}
	p.deliverStep(ctx)
	return nil
}

func (p *Poller) register(ctx context.Context) error {
	r := Registration{Label: p.cfg.Label, PollIntervalSeconds: p.cfg.PollIntervalSeconds, Workers: []RegisteredWorker{}}
	for _, ref := range p.cfg.WorkerRefs() {
		w := p.cfg.Workers[ref]
		r.Workers = append(r.Workers, RegisteredWorker{
			Worker: ref, Family: w.Family, Worktrees: p.state.worktreesFor(ref, w.Worktrees),
		})
	}
	ans, err := p.server.Register(ctx, r)
	if err != nil {
		return err
	}
	if err := p.state.setHostID(ans.HostID); err != nil {
		p.log("state-failed", "", "", "", err.Error())
	}
	refs := map[string]string{}
	for _, w := range ans.Workers {
		if ref := refFor(w, r.Workers); ref != "" {
			refs[w.WorkerID] = ref
		}
	}
	p.mu.Lock()
	p.workerRefs = refs
	p.mu.Unlock()
	return nil
}

// refFor matches a registered worker back to the configured reference it
// came from: by id, URN or name (names are unique per App case-insensitively,
// cor:agt:020:02). An unmatched worker gets no operations here, rather than a
// guess.
func refFor(w RegisteredHostWorker, sent []RegisteredWorker) string {
	for _, s := range sent {
		if refMatches(s.Worker, w.WorkerID, w.URN, w.Name) {
			return s.Worker
		}
	}
	return ""
}

func refMatches(ref, id, urn, name string) bool {
	return ref == id || (urn != "" && ref == urn) || strings.EqualFold(ref, name)
}

// dispatch carries an operation out in the background, once.
func (p *Poller) dispatch(ctx context.Context, op Operation) {
	key := op.WorkerID
	if key == "" {
		key = argString(op, "workerId")
	}
	if p.enqueue(key, op.ID, func() { p.carryOut(ctx, op) }) {
		p.log("collected", op.ID, op.Type, "", "")
	}
}

// enqueue runs job in the background once per id while it is in flight,
// after the worker's previous job. One worker's operations and deliveries
// run one at a time, in order (the server collects oldest first): a
// worktree.remove queued behind a worker.start or worker.end sees the agent
// it launched and any keep a failed end recorded, and a delivery never types
// into a terminal a start or an end is changing. Nothing checks a tree while
// another job on it is between its check and its side effect. Different
// workers' jobs still run concurrently: their trees are disjoint, since a
// worktree token belongs to one worker (config validation). It reports
// whether the job was queued.
func (p *Poller) enqueue(worker, id string, job func()) bool {
	p.mu.Lock()
	if p.inFlight[id] {
		p.mu.Unlock()
		return false
	}
	p.inFlight[id] = true
	prev := p.tails[worker]
	done := make(chan struct{})
	p.tails[worker] = done
	p.mu.Unlock()
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			p.mu.Lock()
			delete(p.inFlight, id)
			if p.tails[worker] == done {
				delete(p.tails, worker)
			}
			p.mu.Unlock()
			close(done)
		}()
		if prev != nil {
			<-prev
		}
		job()
	}()
	return true
}

func (p *Poller) carryOut(ctx context.Context, op Operation) {
	switch op.Type {
	// A stop signal does not cut a worktree operation off midway: a killed
	// `git worktree add|remove` can leave a half-made tree. Those run to the
	// end and report; only the long waits (a start's bind, an end's grace)
	// stop early, leaving their operation collected for the next poller.
	case OpWorktreeCreate:
		p.worktreeCreate(context.WithoutCancel(ctx), op)
	case OpWorktreeRemove:
		p.worktreeRemove(context.WithoutCancel(ctx), op)
	case OpWorkerStart:
		p.workerStart(ctx, op)
	case OpWorkerEnd:
		p.workerEnd(ctx, op)
	case OpWorkerDeliver:
		// A per-task-event operation (hostOperations/types.ts:45). Chat
		// delivery reads S1's batches instead (cli#907), so this host has
		// no delivery path for it.
		p.finish(ctx, op, Failed, "", "no delivery path for worker.deliver: chat delivery reads batches, not this operation (hadron-cli#907)")
	default:
		// Reported, not left in flight: an unreported operation stays
		// collected until the server's timeout (#1881), which isn't built.
		p.finish(ctx, op, Failed, "", fmt.Sprintf("unknown operation type %q: this host carries out only the five typed operations (cor:agt:070:03)", op.Type))
	}
}

// report sends an outcome and logs what the server recorded.
func (p *Poller) report(ctx context.Context, op Operation, outcome, worktree, reason string) (Operation, error) {
	// A final report is sent even while stopping, bounded.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportTimeout)
	defer cancel()
	got, err := p.server.Report(rctx, op.ID, outcome, worktree, reason)
	if err != nil {
		p.mu.Lock()
		p.reportFailures++
		p.mu.Unlock()
		p.log("report-failed", op.ID, op.Type, outcome, err.Error())
		return Operation{}, err
	}
	detail := reason
	if got.Status == Succeeded && outcome == Failed {
		detail = "the server recorded it as succeeded"
	}
	p.log("reported", op.ID, op.Type, got.Status, detail)
	return got, nil
}

// finish reports a final outcome. A failed report is already logged; the
// operation stays collected server-side and is re-delivered at the next
// collect, where it runs again (each operation is safe to repeat).
func (p *Poller) finish(ctx context.Context, op Operation, outcome, worktree, reason string) {
	_, _ = p.report(ctx, op, outcome, worktree, reason)
}

// argString reads one string argument.
func argString(op Operation, name string) string {
	if v, ok := op.Args[name].(string); ok {
		return v
	}
	return ""
}

// workerRef is the configured reference for the operation's worker.
func (p *Poller) workerRef(op Operation) (string, WorkerConfig, bool) {
	id := op.WorkerID
	if id == "" {
		id = argString(op, "workerId")
	}
	p.mu.Lock()
	ref, ok := p.workerRefs[id]
	p.mu.Unlock()
	if !ok {
		return "", WorkerConfig{}, false
	}
	w, ok := p.cfg.Workers[ref]
	return ref, w, ok
}
