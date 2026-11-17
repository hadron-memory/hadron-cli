package hostup

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeSource struct {
	snap Snapshot
	err  error
}

func (s fakeSource) DesiredState(context.Context, string) (Snapshot, error) { return s.snap, s.err }

type fakeObserver map[string]Observation

func (o fakeObserver) Observe(_ context.Context, w Worker) (Observation, error) {
	obs, ok := o[w.WorkerID]
	if !ok {
		return Observation{}, errors.New("cannot see " + w.WorkerID)
	}
	return obs, nil
}

type fakeActuator struct {
	supports map[string]bool
	fail     map[string]error // by worker id + " " + action
	applied  []string
}

func (a *fakeActuator) Supports(action string) bool { return a.supports[action] }

func (a *fakeActuator) Apply(_ context.Context, w Worker, s Step) (string, error) {
	key := w.WorkerID + " " + s.Action
	if err := a.fail[key]; err != nil {
		return "op-" + key, err
	}
	a.applied = append(a.applied, key)
	return "op-" + key, nil
}

func queueActuator() *fakeActuator {
	return &fakeActuator{supports: map[string]bool{ActionWorktreeCreate: true, ActionWorkerStart: true}}
}

func setup(name string) Setup {
	return Setup{Worktree: "hadron-cli/" + name, Repo: "hadron-memory/hadron-cli", Branch: name + "/work", Family: "claude"}
}

func lines(items []Item) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Worker+" "+it.Action+" "+it.Reason)
	}
	return out
}

func run(t *testing.T, e Engine, o Options) Result {
	t.Helper()
	if o.HostID == "" {
		o.HostID = "h1"
	}
	res, err := e.Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// The whole decision table, one worker per row.
func TestPlanTable(t *testing.T) {
	type row struct {
		name   string
		w      Worker
		obs    Observation
		act    *fakeActuator
		mode   string
		bucket string // planned | skipped | failed
		want   []string
	}
	reg := Observation{Registered: true}
	with := func(o Observation, tree bool, terms int) Observation {
		o.WorktreeExists, o.AgentTerminals = tree, terms
		return o
	}
	running := func(reason, session string) Worker {
		return Worker{WorkerID: "w", Name: "W", Desired: Running, Reason: reason, SessionID: session, Setup: setup("w")}
	}
	relaunch := &fakeActuator{supports: map[string]bool{ActionWorktreeCreate: true, ActionWorkerStart: true, ActionWorkerRelaunch: true}}
	rows := []row{
		{"in sync", running("LONG_LIVED", "s"), with(reg, true, 1), nil, "", "skipped", []string{"W  IN_SYNC"}},
		{"missing worktree", running("LONG_LIVED", ""), with(reg, false, 0), nil, "", "planned", []string{"W worktree.create WORKTREE_MISSING", "W worker.start NO_SESSION"}},
		{"missing terminal, no session", running("HOLDS_TASK", ""), with(reg, true, 0), nil, "", "planned", []string{"W worker.start NO_SESSION"}},
		{"missing register row", running("LONG_LIVED", ""), with(Observation{}, true, 0), nil, "", "skipped", []string{"W  NOT_REGISTERED"}},
		{"session, terminal gone", running("LONG_LIVED", "s"), with(reg, true, 0), nil, "", "skipped", []string{"W worker.relaunch RELAUNCH_UNPROVEN"}},
		{"session, terminal gone, relaunch available", running("LONG_LIVED", "s"), with(reg, true, 0), relaunch, "", "skipped", []string{"W worker.relaunch RELAUNCH_UNPROVEN"}},
		{"session, worktree gone", running("LONG_LIVED", "s"), with(reg, false, 0), relaunch, "", "skipped", []string{"W  SESSION_WITHOUT_WORKTREE"}},
		{"terminal without session", running("LONG_LIVED", ""), with(reg, true, 1), nil, "", "skipped", []string{"W  TERMINAL_WITHOUT_SESSION"}},
		{"two agents", running("LONG_LIVED", "s"), with(reg, true, 2), nil, "", "skipped", []string{"W  DUPLICATE_AGENTS"}},
		{"stopped and idle", Worker{WorkerID: "w", Name: "W", Desired: Stopped, Reason: "NO_OPEN_TASK"}, with(reg, true, 0), nil, "", "skipped", []string{"W  IN_SYNC"}},
		{"stopped and running", Worker{WorkerID: "w", Name: "W", Desired: Stopped, Reason: "NO_OPEN_TASK"}, with(reg, true, 1), nil, "", "skipped", []string{"W  STOP_PENDING"}},
		{"unknown desired", Worker{WorkerID: "w", Name: "W", Desired: "MAYBE", Reason: "LONG_LIVED"}, reg, nil, "", "skipped", []string{"W  UNKNOWN_DESIRED"}},
		{"unknown reason", running("SOMETHING_NEW", ""), with(reg, false, 0), nil, "", "skipped", []string{"W  UNKNOWN_REASON"}},
		{"no repo for a missing worktree", Worker{WorkerID: "w", Name: "W", Desired: Running, Reason: "LONG_LIVED", Setup: Setup{Worktree: "plain", Family: "claude"}}, with(reg, false, 0), nil, "", "skipped", []string{"W worktree.create NO_WORKTREE_SOURCE"}},
		{"plain directory present", Worker{WorkerID: "w", Name: "W", Desired: Running, Reason: "LONG_LIVED", Setup: Setup{Worktree: "plain", Family: "claude"}}, with(reg, true, 0), nil, "", "planned", []string{"W worker.start NO_SESSION"}},
		{"worktree not repo/label", Worker{WorkerID: "w", Name: "W", Desired: Running, Reason: "LONG_LIVED", Setup: Setup{Worktree: "other/w", Repo: "hadron-memory/hadron-cli", Branch: "b", Family: "claude"}}, with(reg, false, 0), nil, "", "skipped", []string{"W worktree.create SETUP_INVALID"}},
		{"no family", Worker{WorkerID: "w", Name: "W", Desired: Running, Reason: "LONG_LIVED", Setup: Setup{Worktree: "plain"}}, with(reg, true, 0), nil, "", "skipped", []string{"W  SETUP_INVALID"}},
		{"actuator cannot", running("LONG_LIVED", ""), with(reg, true, 0), &fakeActuator{}, "", "skipped", []string{"W worker.start ACTION_UNAVAILABLE"}},
		{"sweep observe", running("LONG_LIVED", ""), with(reg, true, 0), nil, "OBSERVE", "skipped", []string{"W worker.start SWEEP_NOT_APPLY"}},
		{"sweep unset", running("LONG_LIVED", ""), with(reg, true, 0), nil, "-", "skipped", []string{"W worker.start SWEEP_NOT_APPLY"}},
	}
	for _, reason := range []string{"LEAVE_ALONE", "HALTED", "PAUSED", "TEAM_PAUSED", "RESETTING", "RETIRED", "SWEEP_OFF"} {
		rows = append(rows, row{"never acts: " + reason, running(reason, ""), with(reg, false, 0), nil, "", "skipped", []string{"W  " + reason}})
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			act := r.act
			if act == nil {
				act = queueActuator()
			}
			mode := SweepApply
			switch r.mode {
			case "-":
				mode = ""
			case "":
			default:
				mode = r.mode
			}
			e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: mode, Workers: []Worker{r.w}}}, Observer: fakeObserver{"w": r.obs}, Actuator: act}
			res := run(t, e, Options{})
			got := map[string][]string{"planned": lines(res.Planned), "skipped": lines(res.Skipped), "failed": lines(res.Failed)}
			for bucket, items := range got {
				want := []string{}
				if bucket == r.bucket {
					want = r.want
				}
				if !reflect.DeepEqual(items, want) {
					t.Fatalf("%s = %q, want %q", bucket, items, want)
				}
			}
			if len(act.applied) != 0 {
				t.Fatalf("a dry run applied %v", act.applied)
			}
		})
	}
}

// A stop reason wins before the host is read: the observer is never asked.
func TestNeverActReadsNothing(t *testing.T) {
	e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: SweepApply, Workers: []Worker{{WorkerID: "w", Name: "W", Desired: Running, Reason: "LEAVE_ALONE", WouldBe: Running}}}},
		Observer: fakeObserver{}, Actuator: queueActuator()}
	res := run(t, e, Options{Apply: true})
	if got := lines(res.Skipped); !reflect.DeepEqual(got, []string{"W  LEAVE_ALONE"}) || len(res.Failed) != 0 {
		t.Fatalf("skipped = %q, failed = %q", got, lines(res.Failed))
	}
}

func TestObserveFailureActsOnNothingForThatWorker(t *testing.T) {
	e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: SweepApply, Workers: []Worker{
		{WorkerID: "a", Name: "A", Desired: Running, Reason: "LONG_LIVED", Setup: setup("a")},
		{WorkerID: "b", Name: "B", Desired: Running, Reason: "LONG_LIVED", Setup: setup("b")},
	}}}, Observer: fakeObserver{"b": {Registered: true, WorktreeExists: true}}, Actuator: queueActuator()}
	act := e.Actuator.(*fakeActuator)
	res := run(t, e, Options{Apply: true})
	if got := lines(res.Failed); !reflect.DeepEqual(got, []string{"A  OBSERVE_FAILED"}) {
		t.Fatalf("failed = %q", got)
	}
	if !reflect.DeepEqual(act.applied, []string{"b worker.start"}) {
		t.Fatalf("applied = %v", act.applied)
	}
}

// A failed step skips the same worker's later steps, and other workers go on.
func TestApplyFailureSkipsTheWorkersLaterSteps(t *testing.T) {
	act := queueActuator()
	act.fail = map[string]error{"a worktree.create": errors.New("git worktree add failed")}
	e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: SweepApply, Workers: []Worker{
		{WorkerID: "a", Name: "A", Desired: Running, Reason: "LONG_LIVED", Setup: setup("a")},
		{WorkerID: "b", Name: "B", Desired: Running, Reason: "LONG_LIVED", Setup: setup("b")},
	}}}, Observer: fakeObserver{"a": {Registered: true}, "b": {Registered: true, WorktreeExists: true}}, Actuator: act}
	res := run(t, e, Options{Apply: true})
	if got := lines(res.Failed); !reflect.DeepEqual(got, []string{"A worktree.create ACTION_FAILED", "A worker.start NOT_ATTEMPTED"}) {
		t.Fatalf("failed = %q", got)
	}
	if res.Failed[0].Ref != "op-a worktree.create" || res.Failed[0].Detail != "git worktree add failed" {
		t.Fatalf("failed[0] = %+v", res.Failed[0])
	}
	if got := lines(res.Applied); !reflect.DeepEqual(got, []string{"B worker.start NO_SESSION"}) {
		t.Fatalf("applied = %q", got)
	}
}

// An AbortError stops everything after it and is returned.
func TestAbortStopsTheRun(t *testing.T) {
	act := queueActuator()
	refused := errors.New("FORBIDDEN")
	act.fail = map[string]error{"a worker.start": &AbortError{Err: refused}}
	e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: SweepApply, Workers: []Worker{
		{WorkerID: "a", Name: "A", Desired: Running, Reason: "LONG_LIVED", Setup: setup("a")},
		{WorkerID: "b", Name: "B", Desired: Running, Reason: "LONG_LIVED", Setup: setup("b")},
	}}}, Observer: fakeObserver{"a": {Registered: true, WorktreeExists: true}, "b": {Registered: true, WorktreeExists: true}}, Actuator: act}
	res, err := e.Run(context.Background(), Options{HostID: "h1", Apply: true})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v", err)
	}
	if got := lines(res.Failed); !reflect.DeepEqual(got, []string{"A worker.start REFUSED", "B worker.start NOT_ATTEMPTED"}) {
		t.Fatalf("failed = %q", got)
	}
	if len(act.applied) != 0 {
		t.Fatalf("applied after a refusal: %v", act.applied)
	}
}

func TestOnlyAndSourceErrors(t *testing.T) {
	snap := Snapshot{HostID: "h1", SweepMode: SweepApply, Workers: []Worker{{WorkerID: "a", Name: "Ada", Desired: Running, Reason: "LONG_LIVED", Setup: setup("a")}}}
	e := Engine{Source: fakeSource{snap: snap}, Observer: fakeObserver{"a": {Registered: true, WorktreeExists: true}}, Actuator: queueActuator()}
	res := run(t, e, Options{Only: "ADA"})
	if got := lines(res.Planned); !reflect.DeepEqual(got, []string{"Ada worker.start NO_SESSION"}) {
		t.Fatalf("planned = %q", got)
	}
	res = run(t, e, Options{Only: "zed"})
	if got := lines(res.Skipped); !reflect.DeepEqual(got, []string{"zed  NOT_ON_HOST"}) || len(res.Planned) != 0 {
		t.Fatalf("skipped = %q planned = %q", got, lines(res.Planned))
	}
	if _, err := e.Run(context.Background(), Options{HostID: "h2"}); err == nil {
		t.Fatal("a snapshot for another host was accepted")
	}
	e.Source = fakeSource{err: errors.New("down")}
	res, err := e.Run(context.Background(), Options{HostID: "h1"})
	if err == nil || res.Planned == nil || res.Skipped == nil {
		t.Fatalf("source error: err=%v res=%+v", err, res)
	}
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	unlock, err := Lock(dir, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(dir, "h1"); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: %v", err)
	}
	other, err := Lock(dir, "h2")
	if err != nil {
		t.Fatalf("another host's lock: %v", err)
	}
	other()
	unlock()
	again, err := Lock(dir, "h1")
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	again()
}

// cli#2064 blind P2: an exact id wins over another worker's name, and a name
// two workers share is refused.
func TestSelectWorkerIsUnique(t *testing.T) {
	workers := []Worker{{WorkerID: "jonas", Name: "Zed"}, {WorkerID: "w2", Name: "Jonas"}, {WorkerID: "w3", Name: "iris"}, {WorkerID: "w4", Name: "Iris"}}
	if w, err := selectWorker(workers, "jonas"); err != nil || w == nil || w.Name != "Zed" {
		t.Fatalf("exact id: %+v %v", w, err)
	}
	if w, err := selectWorker(workers, "JONAS"); err != nil || w == nil || w.WorkerID != "w2" {
		t.Fatalf("name, any case: %+v %v", w, err)
	}
	if _, err := selectWorker(workers, "IRIS"); err == nil {
		t.Fatal("a shared name selected a worker")
	}
	if w, err := selectWorker(workers, "nobody"); err != nil || w != nil {
		t.Fatalf("no match: %+v %v", w, err)
	}
	e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: SweepApply, Workers: workers}}, Observer: fakeObserver{}, Actuator: queueActuator()}
	if _, err := e.Run(context.Background(), Options{HostID: "h1", Only: "iris"}); err == nil {
		t.Fatal("Run acted on an ambiguous --worker")
	}
}

// cli#1389 server adapter: a setup the source could not make usable is
// reported SETUP_INVALID before the host is read for that worker.
type countingObserver struct {
	fakeObserver
	asked []string
}

func (o *countingObserver) Observe(ctx context.Context, w Worker) (Observation, error) {
	o.asked = append(o.asked, w.WorkerID)
	return o.fakeObserver.Observe(ctx, w)
}

func TestSetupProblemIsReportedBeforeTheHostIsRead(t *testing.T) {
	obs := &countingObserver{fakeObserver: fakeObserver{"ok": {Registered: true, WorktreeExists: true}}}
	e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: SweepApply, Workers: []Worker{
		{WorkerID: "bad", Name: "Bad", Desired: Running, Reason: "LONG_LIVED", Setup: Setup{Problem: "the host registers 2 worktrees"}},
		{WorkerID: "ok", Name: "Ok", Desired: Running, Reason: "LONG_LIVED", Setup: setup("ok")},
	}}}, Observer: obs, Actuator: queueActuator()}
	res := run(t, e, Options{})
	if got := lines(res.Skipped); !reflect.DeepEqual(got, []string{"Bad  SETUP_INVALID"}) || res.Skipped[0].Detail != "the host registers 2 worktrees" {
		t.Fatalf("skipped = %q %+v", got, res.Skipped)
	}
	if !reflect.DeepEqual(obs.asked, []string{"ok"}) {
		t.Fatalf("observed %v; a worker with a setup problem must not be read", obs.asked)
	}
}

// C5 routing ruling #108587: even a capable actuator may not launch here.
func TestHostUpNeverExecutesRelaunch2060(t *testing.T) {
	w := Worker{WorkerID: "w", Name: "W", Desired: Running, Reason: "LONG_LIVED", SessionID: "open", Setup: setup("w")}
	a := &fakeActuator{supports: map[string]bool{ActionWorkerRelaunch: true}}
	e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: "APPLY", Workers: []Worker{w}}}, Observer: fakeObserver{"w": {Registered: true, WorktreeExists: true}}, Actuator: a}
	result := run(t, e, Options{Apply: true})
	if len(a.applied) != 0 || len(result.Applied) != 0 || len(result.Planned) != 0 {
		t.Fatalf("host up launched outside poller: %+v calls=%v", result, a.applied)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Reason != "RELAUNCH_UNPROVEN" {
		t.Fatalf("unknown poller did not refuse: %+v", result)
	}
}

func TestConnectedPollerStillOnlyReportsRelaunch2060(t *testing.T) {
	w := Worker{WorkerID: "w", Name: "W", Desired: Running, Reason: "HOLDS_TASK", SessionID: "open", Setup: setup("w")}
	a := &fakeActuator{supports: map[string]bool{ActionWorkerRelaunch: true}}
	e := Engine{Source: fakeSource{snap: Snapshot{HostID: "h1", SweepMode: "APPLY", Workers: []Worker{w}}}, Observer: fakeObserver{"w": {Registered: true, WorktreeExists: true, PollerConnected: true}}, Actuator: a}
	result := run(t, e, Options{Apply: true})
	if len(a.applied) != 0 || len(result.Planned) != 0 || len(result.Applied) != 0 || len(result.Skipped) != 1 || result.Skipped[0].Reason != "RELAUNCH_PENDING_POLLER" {
		t.Fatalf("connected poller was bypassed: %+v", result)
	}
}

// cli#2085: a worker the platform does not assign to this host is reported
// NOT_ASSIGNED and never observed or acted on, whatever its state says; an
// assigned worker with no desired state is NOT_REGISTERED. A missing worktree
// with a repo named still plans exactly worktree.create then worker.start.
func TestUnassignedAndAbsentWorkersAreReportedAndNeverActedOn(t *testing.T) {
	w := Worker{WorkerID: "w1", Name: "Iris", Desired: Running, Reason: "LONG_LIVED", Setup: setup("iris")}
	unassigned, absent := w, Worker{WorkerID: "w2", Name: "Jonas", Absent: true}
	unassigned.Unassigned = true
	act := queueActuator()
	// No observation exists for either: reading the host for them would fail.
	res := run(t, Engine{
		Source:   fakeSource{snap: Snapshot{HostID: "h1", SweepMode: SweepApply, SetupSource: "SERVER", Workers: []Worker{unassigned, absent}}},
		Observer: fakeObserver{}, Actuator: act,
	}, Options{Apply: true})
	if got := lines(res.Skipped); !reflect.DeepEqual(got, []string{"Iris  NOT_ASSIGNED", "Jonas  NOT_REGISTERED"}) {
		t.Fatalf("skipped = %q", got)
	}
	if len(res.Planned)+len(res.Applied)+len(res.Failed) != 0 || len(act.applied) != 0 {
		t.Fatalf("nothing is planned or applied: %+v %v", res, act.applied)
	}
	if res.SetupSource != "SERVER" {
		t.Fatalf("SetupSource = %q", res.SetupSource)
	}
}

func TestMissingAssignedWorktreeWithARepoPlansCreateThenStart(t *testing.T) {
	w := Worker{WorkerID: "w1", Name: "Iris", Desired: Running, Reason: "HOLDS_TASK", Setup: setup("iris")}
	res := run(t, Engine{
		Source:   fakeSource{snap: Snapshot{HostID: "h1", SweepMode: SweepApply, SetupSource: "SERVER", Workers: []Worker{w}}},
		Observer: fakeObserver{"w1": {Registered: true}}, Actuator: queueActuator(),
	}, Options{})
	if got := lines(res.Planned); !reflect.DeepEqual(got, []string{"Iris worktree.create WORKTREE_MISSING", "Iris worker.start NO_SESSION"}) {
		t.Fatalf("planned = %q", got)
	}
}
