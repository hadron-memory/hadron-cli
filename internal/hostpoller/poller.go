package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
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
	// IssuedBy is who issued it, as the server names it: PLATFORM, USER or
	// WORKER ("" when the server did not say).
	IssuedBy string
}

// The issuers the server names (HostOperationIssuer).
const (
	IssuedByPlatform = "PLATFORM"
	IssuedByUser     = "USER"
	IssuedByWorker   = "WORKER"
)

// Registration is one registerHost call: the whole worker list.
type Registration struct {
	Capabilities        []string
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
	// Refused are the listed workers a checked registration left out (the
	// App's setup source is SERVER and the platform did not assign them to
	// this host), reported per row; the registration itself succeeded
	// (hadron-cli#2091, hadron-server#4241).
	Refused []RefusedWorker
	// RefusedKnown is false when the server cannot report refusals (it
	// predates hadron-server#4241): Refused is then unknown, not empty.
	RefusedKnown bool
}

// RefusedWorker is one worker a registration left out, with the server's code
// (HOST_WORKER_NOT_ASSIGNED). Both strings are made safe to print by the
// adapter that reads them.
type RefusedWorker struct {
	Worker string
	Code   string
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

// FinalError is ErrFinal carrying, when the refusal said it, the outcome the
// server already recorded.
type FinalError struct {
	// Recorded is "SUCCEEDED" or "FAILED" (upper case), or "" when the
	// refusal did not name one.
	Recorded string
	// Msg is the refusal as it reached the client.
	Msg string
}

func (e *FinalError) Error() string {
	if e.Msg == "" {
		return ErrFinal.Error()
	}
	return ErrFinal.Error() + ": " + e.Msg
}

func (e *FinalError) Unwrap() error { return ErrFinal }

// ErrFinalWithRecorded wraps an ALREADY_REPORTED refusal as a FinalError.
// The recorded outcome comes from the refusal's extensions.status — the
// server spells it there as the host sends its own reports
// (SUCCEEDED / FAILED) so a client reads the code's extension instead of
// parsing the message. The read tolerates case and padding, and only the
// two outcomes are kept: anything else a server might place there reads as
// unnamed, so no caller ever acts on an inferred outcome (#1474).
func ErrFinalWithRecorded(msg, recorded string) *FinalError {
	switch strings.ToUpper(strings.TrimSpace(recorded)) {
	case "SUCCEEDED":
		return &FinalError{Msg: msg, Recorded: Succeeded}
	case "FAILED":
		return &FinalError{Msg: msg, Recorded: Failed}
	default:
		return &FinalError{Msg: msg}
	}
}

// Event is one line of what the poller did, for the operator. The --json
// shape is the command package's DTO, not this.
type Event struct {
	ForeignUIDSkipped                           int
	LocalCause, NoticeCause                     string
	FailureReason                               string
	CheckedAt                                   time.Time
	NoticeFailed                                bool
	Time                                        time.Time
	Kind                                        string
	Operation                                   string
	Type                                        string
	Outcome                                     string
	Detail                                      string
	WorkerID, SessionID, TerminalHandle, Reason string
	Attempt                                     int
}

// Poller is one host's loop.
type Poller struct {
	// orcaFacts and orcaCreate serve a platform worktree.create (SetOrcaWorktrees, hadron-cli#3237).
	orcaFacts     OrcaWorktreeFacts
	orcaCreate    orcaterm.Runner
	relaunchEpoch string
	// outageBackoff paces S10_UNAVAILABLE retries of a pending outage report
	// (cli#2156 F9). In memory only: a restart may retry at once.
	outageRetryAfter time.Time
	outageDelay      time.Duration
	relaunch         *RelaunchDependencies
	relaunchMu       sync.Mutex
	// relaunchCancel is guarded by mu. A fatal check-in cancels the owned
	// recovery job before joining it, without cancelling unrelated operations.
	relaunchCancel     context.CancelFunc
	launchingWorktrees map[string]bool
	// startCfg is the configuration the poller started with and NEVER the one
	// in force after a reload: read configuration through config(), once per
	// pass, and use that one snapshot throughout. The name is the guard: a
	// reader of startCfg in production code is a bug (cli#2623).
	startCfg      *Config
	live          atomic.Pointer[Config]
	removed       map[string]removedWorker // by worker id: workers a reload removed, with their last entry (cli#2623)
	trust         trustWalk                // the filesystem the reload trust check reads; a per-poller seam so tests may override it in parallel (cli#2653 item 3)
	reloadSeen    [32]byte                 // content of a configuration file already refused or reported
	reloadPending [32]byte                 // content seen once, applied only if the next poll sees it again
	server        Server
	host          Host
	state         *State
	key           string
	emit          func(Event)
	now           func() time.Time
	sleep         func(context.Context, time.Duration) error
	// bound reads the session binding of a worktree path (worker.end's stop
	// identifies a process by its worktree and that binding, #2057).
	bound func(path string) delivery.Binding
	// lockBinding takes a worktree's session-binding lock, waiting at most the
	// given time (hadron-cli#2068).
	lockBinding func(path string, wait time.Duration) (func(), error)
	// agentCensus is the strict process census of one worktree (the one C5
	// proves absence with, cli#2060); the agentless end (cli#2201) asks it
	// before it says no agent runs. A seam so a test can answer it.
	agentCensus func(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error)
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
	// lastRefused is the refusal set last reported, so a steady one is said once.
	lastRefused string
	// refusalsUnknownSaid: the "server cannot report refusals" event was emitted.
	refusalsUnknownSaid bool

	// DeliveryStep, when set, is told how each delivery step's batch read
	// went (delivery.StepRead, StepReadFailed, StepOffByScope).
	DeliveryStep func(at time.Time, read string)
	// WakeStep, when set, is told how each step's wake read went (cli#906
	// row 1).
	WakeStep func(delivery.WakeRead)
	// agents are the agent processes this poller launched, by worker id.
	agents map[string]Agent
	// agentTrees is the worktree each launched agent runs in, by worker id.
	agentTrees map[string]string
	// agentIDs is each launched agent's process identity, observed when it
	// was tracked: a later pin must match it, so a reused pid is never
	// taken for the agent (#1043). Absent when resets were not enabled at
	// launch, or the observation failed; a reset then refuses.
	agentIDs map[string]ProcessIdentity
	// agentSessions is the session each launched agent's start bound, as the
	// server reported it (in memory only): worker.end stops a launched agent
	// only for that session (#2057).
	agentSessions map[string]string
	// orcaLaunch and profiles start a launch_via: orca worker through Orca
	// (hadron-cli#2058 C3); nil until EnableOrcaLaunch, and such a worker's
	// start then refuses rather than falling back to the exec path.
	orcaLaunch OrcaLaunchFunc
	profiles   ProfileSource
	wg         sync.WaitGroup

	// Chat delivery (cli#907): off unless EnableDelivery, and switched off
	// by a confirmed missing attention:deliver scope (re-checked, cli#2663).
	batches     BatchSource
	deliverer   Deliverer
	deliveryOff bool
	// Delivery's scope verdict (cli#2663): consecutive scope answers, and the
	// off state's re-check backoff.
	scopeStreak  int
	recheckEvery time.Duration
	recheckAt    time.Time
	// Tick and task wakes (cli#906 row 1): off unless EnableWakes.
	wakes         WakeSource
	wakeDeliverer WakeDeliverer
	// budgetLogged is the UTC day an exhausted wake budget was last logged.
	budgetLogged string

	// Exit relaunch (cli#2161): exitWake is made by EnableRelaunch and wakes
	// the run loop when a tracked agent exits; exited holds those agents by
	// worker id until an exit pass takes them.
	exitWake chan struct{}
	exited   map[string]Agent
	// Test hooks, nil in production: told when an exit pass for a worker has
	// finished, and when a relaunch job has released its lock.
	afterExitPass    func(workerID string)
	afterRelaunchJob func()
	// otherKeys are the other host keys this process holds (SetScrubKeys).
	otherKeys []string
	// Refresh, when set, is a host that runs from the platform (hadron-cli#3237): each Step builds the
	// Config from the platform's answer instead of reloading a file (SetPlatformRefresh).
	refresh        func(ctx context.Context) (*Config, error)
	sourceHostSaid bool
}

// New builds a poller. key is the host key, kept only to scrub it from
// launched agents' environments (defence in depth: it is never put there).
func New(cfg *Config, server Server, host Host, state *State, key string, emit func(Event)) *Poller {
	return &Poller{
		startCfg: cfg, server: server, host: host, state: state, key: key, emit: emit,
		now:         time.Now,
		sleep:       sleepCtx,
		bound:       delivery.WorktreeBinding,
		lockBinding: delivery.LockBinding,
		agentCensus: probeRelaunchWorktree,
		BindPoll:    5 * time.Second,
		inFlight:    map[string]bool{},
		tails:       map[string]chan struct{}{},
		workerRefs:  map[string]string{},
		agents:      map[string]Agent{},
		agentTrees:  map[string]string{},
		agentIDs:    map[string]ProcessIdentity{},

		agentSessions: map[string]string{},
	}
}

// SetScrubKeys names every host key this process holds (hadron-cli#3237: one poller per stored credential).
// A launched or ended agent's environment drops a value carrying any of them, and a reported cause redacts
// them all, not only this poller's own. Call before Run.
func (p *Poller) SetScrubKeys(keys []string) { p.otherKeys = append([]string{}, keys...) }

func (p *Poller) scrubKeys() []string { return append([]string{p.key}, p.otherKeys...) }

// ScrubKeys is every key this poller scrubs from a launched agent's environment and redacts from what it
// reports: its own and each one SetScrubKeys named (hadron-cli#3237: a run of several hosts scrubs them all).
func (p *Poller) ScrubKeys() []string { return p.scrubKeys() }

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

// removedWorker is a worker a reload took out of the file, with the entry it
// had: ending its session still needs that entry, and ending only de-escalates.
type removedWorker struct {
	ref string
	cfg WorkerConfig
}

// config is the configuration in force: the one the poller started with until
// a reload applies, then the newest applied. A Config never changes after it
// is built, so a caller that takes it once sees one consistent worker set.
func (p *Poller) config() *Config {
	if c := p.live.Load(); c != nil {
		return c
	}
	return p.startCfg
}

// Current is the configuration in force: what a component wired at start asks
// for when it needs the workers as they are now (cli#2623).
func (p *Poller) Current() *Config { return p.config() }

// Run polls until ctx ends, then waits for operations in flight.
func (p *Poller) Run(ctx context.Context) error {
	for {
		if err := p.Step(ctx); err != nil {
			p.log("error", "", "", "", err.Error())
			if p.Fatal != nil && p.Fatal(err) {
				p.cancelRelaunch()
				p.wg.Wait()
				return err
			}
		}
		// Read after every Step: a platform host's interval is its platform row's, which the Step just read,
		// so a change applies at the next cycle (hadron-cli#3237); a file's is read only at start.
		interval := time.Duration(p.config().PollIntervalSeconds) * time.Second
		if err := p.pause(ctx, interval); err != nil {
			p.wg.Wait()
			return nil
		}
	}
}

// pause sleeps out the interval. An exit wake (cli#2161) runs the exit passes
// at once and sleeps on to the same deadline: it never brings the next
// check-in forward.
func (p *Poller) pause(ctx context.Context, interval time.Duration) error {
	if p.exitWake == nil {
		return p.sleep(ctx, interval)
	}
	deadline := time.Now().Add(interval)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return ctx.Err()
		}
		sctx, cancel := context.WithCancel(ctx)
		stop, woke := make(chan struct{}), make(chan bool, 1)
		go func() {
			select {
			case <-p.exitWake:
				cancel()
				woke <- true
			case <-stop:
				woke <- false
			}
		}()
		err := p.sleep(sctx, left)
		close(stop)
		woken := <-woke // the watcher has finished: a wake is never lost
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !woken {
			return err
		}
		p.relaunchExitStep(ctx)
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
	if p.refresh != nil {
		if hold, err := p.platformRefresh(ctx); err != nil || hold {
			return err
		}
	} else {
		p.reloadConfig()
	}
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
	p.relaunchStep(ctx)
	return nil
}

func (p *Poller) register(ctx context.Context) error {
	c := p.config() // one snapshot for the whole registration
	r := Registration{Capabilities: append([]string{}, c.Capabilities...), Label: c.Label, PollIntervalSeconds: c.PollIntervalSeconds, Workers: []RegisteredWorker{}}
	for _, ref := range c.WorkerRefs() {
		w, _ := c.Worker(ref)
		r.Workers = append(r.Workers, RegisteredWorker{
			Worker: ref, Family: CanonicalFamily(w.Family), Worktrees: p.state.worktreesFor(ref, w.Worktrees),
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
	if ans.RefusedKnown {
		p.reportRefused(ans.Refused)
	} else {
		p.reportRefusalsUnknown()
	}
	return nil
}

// reportRefusalsUnknown says once that this server cannot tell which workers a
// registration left out, so their absence is not evidence they were accepted.
func (p *Poller) reportRefusalsUnknown() {
	p.mu.Lock()
	said := p.refusalsUnknownSaid
	p.refusalsUnknownSaid = true
	p.mu.Unlock()
	if !said {
		p.log("refusals-unknown", "", "", "", "this server does not report which workers a registration left out (it predates hadron-server#4241); the registration itself succeeded")
	}
}

// reportRefused makes the per-worker refusals visible without failing the
// registration or repeating itself every poll: one event per refused worker
// when the set changes (hadron-cli#2091).
func (p *Poller) reportRefused(refused []RefusedWorker) {
	// Each pair is quoted, so no two different sets share a key (a worker
	// containing a space cannot pose as a different code and worker).
	keys := make([]string, 0, len(refused))
	for _, r := range refused {
		keys = append(keys, strconv.Quote(r.Code)+":"+strconv.Quote(r.Worker))
	}
	sort.Strings(keys)
	key := strings.Join(keys, ",")
	p.mu.Lock()
	previous := p.lastRefused
	changed := key != previous
	p.lastRefused = key
	p.mu.Unlock()
	if !changed {
		return
	}
	if len(refused) == 0 && previous != "" {
		p.log("worker-refusals-cleared", "", "", "", "no worker is left out of this host's registration now")
		return
	}
	for _, r := range refused {
		p.log("worker-refused", "", "", "", r.Worker+": "+r.Code+" (left out of the registration; the platform's assignments decide which workers this host runs, see `hadron team host setup`)")
	}
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
	case OpWorkerResetContext:
		// The reset-context host handler was removed (hadron-cli#3017): no
		// host declares the capability, so the server issues this to no host.
		// A server that does anyway is answered, not left to time out. The
		// reason must carry the pre-claim code HOST_UNSUPPORTED: the server
		// judges a FAILED reset report's leading code against its allow-list
		// (hadron-server src/lib/contextReset/types.ts; pinned by
		// internal/cmd/team_host_reset_refusal_3017_test.go) and refuses
		// anything else with BAD_CONTEXT_RESET_INPUT, re-delivering the
		// operation forever.
		p.finish(ctx, op, Failed, "", "HOST_UNSUPPORTED: this host carries out no worker.reset-context (hadron-cli#3017)")
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
	w, ok := p.config().Worker(ref)
	return ref, w, ok
}
