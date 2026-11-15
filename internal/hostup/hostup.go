// Package hostup is the reconcile engine behind `hadron team host up`
// (hadron-cli#1389, epic hadron-server#4229): compare what a host SHOULD run
// with what it DOES run, plan the missing steps, and, on apply, carry them
// out through an Actuator.
//
// The engine is pure over three seams, so it can be driven by fakes:
//
//	Source    what should run: one Snapshot per host (the server's
//	          hostDesiredState once hadron-server#4222/#4224 land; a file or
//	          an in-memory fake until then)
//	Observer  what does run: registration, worktree, agent terminals
//	Actuator  how a planned step is carried out (the typed host-operation
//	          queue, cor:agt:070:05; a host-local relaunch later, cli#2060)
//
// What it never does, whatever the seams say: act on a worker the platform
// marked leave-alone, halted, paused, team-paused, resetting or retired; act
// when the App's sweep mode is not APPLY; cast, bind, rebind or end anything;
// act on a worker it could not observe.
package hostup

import (
	"context"
	"errors"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Desired values of Worker.Desired.
const (
	Running = "RUNNING"
	Stopped = "STOPPED"
)

// SweepApply is the only sweep mode under which the engine acts.
const SweepApply = "APPLY"

// The actions a plan can hold. The first two are the typed host operations
// `worker onboard` issues; the third is host-local (cli#2060) and is planned
// only when the Actuator supports it.
const (
	ActionWorktreeCreate = "worktree.create"
	ActionWorkerStart    = "worker.start"
	ActionWorkerRelaunch = "worker.relaunch"
)

// Reason codes. They are part of the `--json` contract: add, never repurpose.
const (
	// Why a step is planned (and, once done, applied).
	ReasonWorktreeMissing = "WORKTREE_MISSING"
	ReasonNoSession       = "NO_SESSION"
	ReasonTerminalMissing = "TERMINAL_MISSING"

	// Why a worker was left alone. The platform's own stop reasons
	// (LEAVE_ALONE, HALTED, PAUSED, TEAM_PAUSED, RESETTING, RETIRED,
	// SWEEP_OFF) pass through unchanged.
	ReasonInSync        = "IN_SYNC"
	ReasonSweepNotApply = "SWEEP_NOT_APPLY"
	ReasonNotOnHost     = "NOT_ON_HOST"
	ReasonNotRegistered = "NOT_REGISTERED"
	// ReasonNotAssigned: the App's setup source is SERVER and the platform
	// does not assign this worker to this host (cli#2085).
	ReasonNotAssigned            = "NOT_ASSIGNED"
	ReasonUnknownDesired         = "UNKNOWN_DESIRED"
	ReasonUnknownReason          = "UNKNOWN_REASON"
	ReasonSessionWithoutWorktree = "SESSION_WITHOUT_WORKTREE"
	ReasonTerminalWithoutSession = "TERMINAL_WITHOUT_SESSION"
	ReasonDuplicateAgents        = "DUPLICATE_AGENTS"
	ReasonNoWorktreeSource       = "NO_WORKTREE_SOURCE"
	ReasonSetupInvalid           = "SETUP_INVALID"
	ReasonRelaunchUnavailable    = "RELAUNCH_UNAVAILABLE"
	ReasonActionUnavailable      = "ACTION_UNAVAILABLE"
	ReasonStopPending            = "STOP_PENDING"

	// Why a step failed.
	ReasonObserveFailed = "OBSERVE_FAILED"
	ReasonActionFailed  = "ACTION_FAILED"
	ReasonRefused       = "REFUSED"
	ReasonNotAttempted  = "NOT_ATTEMPTED"
)

// neverAct are the platform's reasons that forbid acting on a worker, whatever
// its desired state reads (hadron-server#4224: they always win over lifecycle).
var neverAct = map[string]bool{
	"LEAVE_ALONE": true, "HALTED": true, "PAUSED": true, "TEAM_PAUSED": true,
	"RESETTING": true, "RETIRED": true, "SWEEP_OFF": true,
}

// runReasons are the reasons under which a RUNNING worker may be started. A
// reason outside this set is not acted on: a new server reason must be taught
// to the engine before the engine starts processes on it.
var runReasons = map[string]bool{"LONG_LIVED": true, "HOLDS_TASK": true}

// Setup is what the host needs to start a worker.
type Setup struct {
	// Worktree is the worker's worktree name token, relative to the host's
	// worktrees root: "<repo's last segment>/<label>" for a git worktree, or
	// the name of a plain directory for a worker without Git.
	Worktree string
	// Repo, BaseBranch and Branch are what worktree.create needs. A worker
	// with no Repo is never given a worktree: its directory must exist.
	Repo       string
	BaseBranch string
	Branch     string
	// Family is the model family worker.start launches.
	Family string
	// RepoSource, when set, names the repos the worker's role has, read only
	// when the worktree is missing and Repo is empty. Exactly one entry is the
	// repo a worktree.create is planned from; zero, several or a failed read is
	// NO_WORKTREE_SOURCE: a repo is never guessed (cli#2085).
	RepoSource func(ctx context.Context) ([]string, error)
	// Problem, when set, is why the source could not give a usable setup
	// (no worktree, several, or a malformed value). The worker is then
	// reported SETUP_INVALID before the host is read for it.
	Problem string
}

// Worker is one worker's desired state on a host.
type Worker struct {
	WorkerID string
	Name     string
	Desired  string
	Reason   string
	// WouldBe is what Desired would read without the marker in Reason (the
	// server reports it for LEAVE_ALONE). Echoed, never acted on.
	WouldBe string
	// SessionID is the worker's open session, or "".
	SessionID string
	Setup     Setup
	// Unassigned: the App's setup source is SERVER and the platform does not
	// assign this worker to this host. Reported, never acted on (cli#2085).
	Unassigned bool
	// Absent: assigned to this host by the platform, but the desired state
	// does not list it (the host's registration does not). It has no desired
	// state to act on: reported NOT_REGISTERED, never acted on (cli#2085).
	Absent bool
}

// Snapshot is a host's desired state.
type Snapshot struct {
	HostID    string
	SweepMode string
	// SetupSource is the App's hostSetupSource ("HOST" or "SERVER") when the
	// source read it, else "" (a file, or a server that predates it).
	SetupSource string
	Workers     []Worker
}

// Source reads a host's desired state.
type Source interface {
	DesiredState(ctx context.Context, hostID string) (Snapshot, error)
}

// Observation is what the host shows for one worker.
type Observation struct {
	// Registered: the host's registration lists this worker.
	Registered bool
	// WorktreeExists: the worker's worktree (or plain directory) is there.
	WorktreeExists bool
	// AgentTerminals: live agent terminals in that worktree.
	AgentTerminals int
}

// Observer reads the host. An error means "could not tell", and the engine
// then plans nothing for that worker; an *AbortError stops the whole run
// before anything is applied.
type Observer interface {
	Observe(ctx context.Context, w Worker) (Observation, error)
}

// Step is one planned action for one worker.
type Step struct {
	Action string
	Reason string
	Args   map[string]string
}

// Actuator carries out a step. Supports says whether it can, so a plan never
// holds a step that apply could not attempt. Apply returns a reference to what
// it did (a host-operation id), which may be set even when it fails.
type Actuator interface {
	Supports(action string) bool
	Apply(ctx context.Context, w Worker, s Step) (ref string, err error)
}

// AbortError is how an Actuator says the whole run must stop: nothing further
// is attempted (a refused credential must not be retried per worker).
type AbortError struct{ Err error }

func (e *AbortError) Error() string { return e.Err.Error() }
func (e *AbortError) Unwrap() error { return e.Err }

// Item is one line of a run's result: a step planned, applied or failed, or a
// worker skipped.
type Item struct {
	WorkerID string
	Worker   string
	// Action is the step, or for a skipped worker the step the engine did
	// not take ("" when there was none to take).
	Action string
	Reason string
	Args   map[string]string
	// Ref is the Actuator's reference for an applied or failed step.
	Ref string
	// Detail is free text for a human: the failure, or what was seen.
	Detail string
}

// Result is one run.
type Result struct {
	HostID    string
	SweepMode string
	// SetupSource echoes Snapshot.SetupSource.
	SetupSource string
	DryRun      bool
	Planned     []Item
	Applied     []Item
	Skipped     []Item
	Failed      []Item
}

// Options select the run.
type Options struct {
	HostID string
	// Apply carries the plan out. False is a dry run: the Actuator's Apply is
	// never called.
	Apply bool
	// Only narrows the run to one worker, matched against a worker's id or
	// name (case-insensitively). A worker the snapshot does not hold is
	// reported as NOT_ON_HOST and nothing is created for it.
	Only string
}

// Engine reconciles one host.
type Engine struct {
	Source   Source
	Observer Observer
	Actuator Actuator
}

type plannedStep struct {
	worker Worker
	step   Step
}

// Run reads the desired state, observes, plans and (with Options.Apply)
// applies. The Result is complete even when the returned error is non-nil:
// that error is the Source's, or the AbortError that stopped an apply.
func (e Engine) Run(ctx context.Context, o Options) (Result, error) {
	res := Result{HostID: o.HostID, DryRun: !o.Apply, Planned: []Item{}, Applied: []Item{}, Skipped: []Item{}, Failed: []Item{}}
	snap, err := e.Source.DesiredState(ctx, o.HostID)
	if err != nil {
		return res, err
	}
	if snap.HostID != o.HostID {
		return res, errors.New("the desired state is for host " + snap.HostID + ", not " + o.HostID)
	}
	res.SweepMode = snap.SweepMode
	res.SetupSource = snap.SetupSource

	workers := append([]Worker(nil), snap.Workers...)
	sort.SliceStable(workers, func(i, j int) bool { return workers[i].Name < workers[j].Name })
	if o.Only != "" {
		one, err := selectWorker(workers, o.Only)
		if err != nil {
			return res, err
		}
		if one == nil {
			res.Skipped = append(res.Skipped, Item{Worker: o.Only, Reason: ReasonNotOnHost, Detail: "not among the workers this host should run; nothing is created for it"})
			workers = nil
		} else {
			workers = []Worker{*one}
		}
	}

	var plan []plannedStep
	for _, w := range workers {
		steps, skip, fail := e.plan(ctx, snap.SweepMode, w)
		if fail != nil && fail.abort != nil {
			// Nothing has been applied yet; the plan so far is reported.
			return res, fail.abort
		}
		switch {
		case fail != nil:
			res.Failed = append(res.Failed, fail.Item)
		case skip != nil:
			res.Skipped = append(res.Skipped, *skip)
		}
		for _, s := range steps {
			plan = append(plan, plannedStep{w, s})
			res.Planned = append(res.Planned, item(w, s))
		}
	}
	if !o.Apply {
		return res, nil
	}

	var abort error
	failedWorker := map[string]bool{}
	for _, p := range plan {
		it := item(p.worker, p.step)
		if abort != nil || failedWorker[p.worker.WorkerID] {
			it.Reason, it.Detail = ReasonNotAttempted, "an earlier step failed"
			res.Failed = append(res.Failed, it)
			continue
		}
		ref, err := e.Actuator.Apply(ctx, p.worker, p.step)
		it.Ref = ref
		if err == nil {
			res.Applied = append(res.Applied, it)
			continue
		}
		it.Reason, it.Detail = ReasonActionFailed, err.Error()
		var ab *AbortError
		if errors.As(err, &ab) {
			it.Reason, abort = ReasonRefused, ab.Err
		}
		failedWorker[p.worker.WorkerID] = true
		res.Failed = append(res.Failed, it)
	}
	return res, abort
}

// selectWorker resolves --worker to exactly one worker (cli#2064 blind P2):
// an exact id wins; otherwise the name must match one worker, ignoring case.
// A name two workers share is refused, never acted on for both. nil, nil is
// no match at all.
func selectWorker(workers []Worker, ref string) (*Worker, error) {
	for i := range workers {
		if workers[i].WorkerID == ref {
			return &workers[i], nil
		}
	}
	var match *Worker
	for i := range workers {
		if !strings.EqualFold(workers[i].Name, ref) {
			continue
		}
		if match != nil {
			return nil, errors.New("--worker " + ref + " matches more than one worker by name; pass the worker's id")
		}
		match = &workers[i]
	}
	return match, nil
}

func item(w Worker, s Step) Item {
	return Item{WorkerID: w.WorkerID, Worker: w.Name, Action: s.Action, Reason: s.Reason, Args: s.Args}
}

// failure is a worker that could not be observed; abort is set when the
// Observer said the whole run must stop (an AbortError).
type failure struct {
	Item
	abort error
}

// plan decides one worker: the steps to take, or why it is skipped, or that it
// could not be observed. Exactly one of the three is set.
func (e Engine) plan(ctx context.Context, sweepMode string, w Worker) ([]Step, *Item, *failure) {
	skip := func(reason, action, detail string) ([]Step, *Item, *failure) {
		return nil, &Item{WorkerID: w.WorkerID, Worker: w.Name, Action: action, Reason: reason, Detail: detail}, nil
	}
	// A worker the platform does not assign to this host is never touched,
	// whatever else its state says (cli#2085).
	if w.Unassigned {
		return skip(ReasonNotAssigned, "", "the App's host setup (SERVER) does not assign this worker to this host")
	}
	if w.Absent {
		return skip(ReasonNotRegistered, "", "assigned to this host, but its registration does not list the worker, so there is no desired state to act on")
	}
	// The platform's stop reasons win before anything is read from the host.
	if neverAct[w.Reason] {
		return skip(w.Reason, "", "")
	}
	if w.Desired != Running && w.Desired != Stopped {
		return skip(ReasonUnknownDesired, "", "desired state "+w.Desired)
	}
	if w.Desired == Running && !runReasons[w.Reason] {
		return skip(ReasonUnknownReason, "", "reason "+w.Reason)
	}

	if w.Setup.Problem != "" {
		return skip(ReasonSetupInvalid, "", w.Setup.Problem)
	}

	obs, err := e.Observer.Observe(ctx, w)
	if err != nil {
		f := &failure{Item: Item{WorkerID: w.WorkerID, Worker: w.Name, Reason: ReasonObserveFailed, Detail: err.Error()}}
		var ab *AbortError
		if errors.As(err, &ab) {
			f.abort = ab.Err
		}
		return nil, nil, f
	}
	session := w.SessionID != ""

	if w.Desired == Stopped {
		if obs.AgentTerminals > 0 {
			// Stopping is the platform's worker.end (cli#2057), not this pass.
			return skip(ReasonStopPending, "", "an agent is running; the platform's worker.end stops it")
		}
		return skip(ReasonInSync, "", "")
	}

	var steps []Step
	switch {
	case obs.AgentTerminals > 1:
		return skip(ReasonDuplicateAgents, "", "more than one agent terminal in its worktree")
	case session && obs.AgentTerminals == 1:
		return skip(ReasonInSync, "", "")
	case !obs.Registered:
		return skip(ReasonNotRegistered, "", "this host's registration does not list the worker")
	case !session && obs.AgentTerminals == 1:
		// An agent with no session: never bound from here, never a second one.
		return skip(ReasonTerminalWithoutSession, "", "an agent terminal is open with no worker session")
	case session && !obs.WorktreeExists:
		// A live session is never rebound, and there is nowhere to relaunch.
		return skip(ReasonSessionWithoutWorktree, "", "the session is open and its worktree is missing")
	case session:
		// The process died under an open session: worker.start is refused for
		// it, so only a host-local relaunch in the same worktree can help.
		if !e.Actuator.Supports(ActionWorkerRelaunch) {
			return skip(ReasonRelaunchUnavailable, ActionWorkerRelaunch, "the session is open and no agent terminal is; a host-local relaunch is not available here")
		}
		steps = []Step{{Action: ActionWorkerRelaunch, Reason: ReasonTerminalMissing, Args: map[string]string{"worktree": w.Setup.Worktree, "family": w.Setup.Family}}}
	default:
		if w.Setup.Worktree == "" || w.Setup.Family == "" {
			return skip(ReasonSetupInvalid, "", "the setup names no worktree or no model family")
		}
		if !obs.WorktreeExists {
			s := w.Setup
			if s.Repo == "" && s.Branch != "" && s.RepoSource != nil {
				repos, err := s.RepoSource(ctx)
				switch {
				case err != nil:
					return skip(ReasonNoWorktreeSource, ActionWorktreeCreate, "the worktree is missing and the worker's repos could not be read: "+err.Error())
				case len(repos) != 1:
					return skip(ReasonNoWorktreeSource, ActionWorktreeCreate, "the worktree is missing and the worker's role names "+strconv.Itoa(len(repos))+" repos, so none is chosen for it")
				}
				s.Repo = repos[0]
			}
			if s.Repo == "" || s.Branch == "" {
				return skip(ReasonNoWorktreeSource, ActionWorktreeCreate, "the worktree is missing and the setup names no repo or branch to create it from")
			}
			label := path.Base(s.Worktree)
			if path.Base(s.Repo)+"/"+label != s.Worktree {
				return skip(ReasonSetupInvalid, ActionWorktreeCreate, "worktree "+s.Worktree+" is not <repo's last segment>/<label> for repo "+s.Repo)
			}
			base := s.BaseBranch
			if base == "" {
				base = "main"
			}
			steps = append(steps, Step{Action: ActionWorktreeCreate, Reason: ReasonWorktreeMissing, Args: map[string]string{"repo": s.Repo, "baseBranch": base, "branchName": s.Branch, "label": label}})
		}
		steps = append(steps, Step{Action: ActionWorkerStart, Reason: ReasonNoSession, Args: map[string]string{"worktree": w.Setup.Worktree, "family": w.Setup.Family}})
	}

	for _, s := range steps {
		if !e.Actuator.Supports(s.Action) {
			return skip(ReasonActionUnavailable, s.Action, "this run cannot carry out "+s.Action)
		}
	}
	// The App's switch: nothing is planned unless the sweep mode is APPLY.
	if sweepMode != SweepApply {
		return skip(ReasonSweepNotApply, steps[0].Action, "the App's sweep mode is "+orNone(sweepMode)+", so nothing is planned")
	}
	return steps, nil, nil
}

func orNone(s string) string {
	if s == "" {
		return "unset"
	}
	return s
}
