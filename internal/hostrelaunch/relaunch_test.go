package hostrelaunch

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type memJournal struct {
	rows   map[string]Record
	saves  int
	fail   bool
	onSave func(Record)
}

func (m *memJournal) Load(k string) (Record, error) {
	v := m.rows[k]
	v.Attempts = append([]time.Time(nil), v.Attempts...)
	return v, nil
}
func (m *memJournal) Save(k string, v Record) error {
	if m.fail {
		return errors.New("private journal unavailable")
	}
	m.saves++
	v.Attempts = append([]time.Time(nil), v.Attempts...)
	m.rows[k] = v
	if m.onSave != nil {
		m.onSave(v)
	}
	return nil
}

type fakeAgent struct {
	f   *fixture
	ref string
}

func (a *fakeAgent) Reference() string { return a.ref }
func (a *fakeAgent) Verify(_ context.Context, t Target, o Observation) error {
	a.f.verified++
	if t != a.f.target || !reflect.DeepEqual(o.PIDs, []int{42}) {
		return errors.New("foreign process")
	}
	return a.f.verifyErr
}
func (a *fakeAgent) Rollback(context.Context) error { a.f.rolledBack++; return a.f.rollbackErr }

type fixture struct {
	target                                                             Target
	desired                                                            Desired
	binding                                                            Binding
	runner                                                             Runner
	store                                                              *memJournal
	profile                                                            string
	launched                                                           bool
	starts, verified, rolledBack, locks, unlocks, logs, claimedAtStart int
	probeErr, startErr, verifyErr, rollbackErr                         error
	post                                                               Observation
	onLock, onStart                                                    func()
	notices                                                            map[string]int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{profile: "typed-profile-v1", store: &memJournal{rows: map[string]Record{}}, notices: map[string]int{}, post: Observation{PIDs: []int{42}, Proven: true}}
	f.target = Target{AppID: "app", HostID: "host", WorkerID: "worker", SessionID: "open-session", Server: "https://private.invalid", Worktree: t.TempDir()}
	f.desired = Desired{Target: f.target, SweepMode: "APPLY", State: "RUNNING", Reason: "HOLDS_TASK"}
	f.binding = Binding{AppID: "app", WorkerID: "worker", SessionID: "open-session", Server: f.target.Server, Usable: true}
	f.runner = Runner{Sweep: 1, Journal: f.store, Now: func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) },
		Current: func(context.Context, Target) (Desired, error) { return f.desired, nil },
		Binding: func(context.Context, Target) (Binding, error) { return f.binding, nil },
		Probe: func(context.Context, Target) (Observation, error) {
			if f.probeErr != nil {
				return Observation{}, f.probeErr
			}
			if f.launched {
				return f.post, nil
			}
			return Observation{Proven: true}, nil
		},
		Prepare: func(context.Context, Target) (Prepared, error) {
			return Prepared{Fingerprint: f.profile, Launch: func(context.Context) (Agent, error) {
				f.starts++
				rec, _ := f.store.Load(f.target.WorkerID)
				f.claimedAtStart = len(rec.Attempts)
				if f.onStart != nil {
					f.onStart()
				}
				if f.startErr != nil {
					return nil, f.startErr
				}
				f.launched = true
				return &fakeAgent{f: f, ref: "own-terminal"}, nil
			}}, nil
		},
		Lock: func(context.Context, Target) (func(), error) {
			f.locks++
			if f.onLock != nil {
				f.onLock()
			}
			return func() { f.unlocks++ }, nil
		},
		Notice: func(_ context.Context, _ Target, r Result) error { f.notices[r.Reason]++; return nil },
		Log:    func(Target, Result) { f.logs++ },
	}
	return f
}
func (f *fixture) run(t *testing.T, sweep uint64) Result {
	t.Helper()
	f.runner.Sweep = sweep
	r, err := f.runner.Execute(context.Background(), f.target)
	if err != nil {
		t.Fatalf("%+v: %v", r, err)
	}
	return r
}
func TestTwoConsecutiveSweepsAndAttemptClaim2060(t *testing.T) {
	f := newFixture(t)
	if r := f.run(t, 1); r.Status != Skipped || r.Reason != Pending {
		t.Fatal(r)
	}
	if f.starts != 0 || len(f.store.rows[f.target.WorkerID].Attempts) != 0 {
		t.Fatal("first observation acted")
	}
	if r := f.run(t, 2); r.Status != Applied || r.Reference != "own-terminal" {
		t.Fatal(r)
	}
	if f.starts != 1 || f.claimedAtStart != 1 || f.verified != 1 || f.rolledBack != 0 || f.unlocks != 2 {
		t.Fatalf("fixture=%+v", f)
	}
}
func TestSameOrInterruptedSweepDoesNotEstablishContinuity2060(t *testing.T) {
	for _, second := range []uint64{1, 3} {
		t.Run(string(rune('0'+second)), func(t *testing.T) {
			f := newFixture(t)
			f.run(t, 1)
			if r := f.run(t, second); r.Reason != Pending || f.starts != 0 {
				t.Fatal(r)
			}
		})
	}
}
func TestIdentityAndServerStopsRefuse2060(t *testing.T) {
	cases := map[string]func(*fixture){
		"worker": func(f *fixture) { f.desired.WorkerID = "other" }, "session": func(f *fixture) { f.desired.SessionID = "new" }, "host": func(f *fixture) { f.desired.HostID = "other" }, "app": func(f *fixture) { f.desired.AppID = "other" }, "directory": func(f *fixture) { f.desired.Worktree += "-other" },
		"binding-worker": func(f *fixture) { f.binding.WorkerID = "other" }, "binding-session": func(f *fixture) { f.binding.SessionID = "other" }, "binding-app": func(f *fixture) { f.binding.AppID = "other" }, "binding-server": func(f *fixture) { f.binding.Server = "https://other.invalid" }, "unusable-binding": func(f *fixture) { f.binding.Usable = false },
		"mode": func(f *fixture) { f.desired.SweepMode = "OBSERVE" }, "desired": func(f *fixture) { f.desired.State = "STOPPED" }, "unknown": func(f *fixture) { f.desired.Reason = "NEW_SERVER_REASON" },
	}
	for _, reason := range []string{"PAUSED", "HALTED", "LEAVE_ALONE", "TEAM_PAUSED", "RESETTING", "RETIRED", "SWEEP_OFF"} {
		reason := reason
		cases[reason] = func(f *fixture) { f.desired.State = "STOPPED"; f.desired.Reason = reason }
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			change(f)
			r := f.run(t, 1)
			if r.Status != Skipped || f.starts != 0 || f.locks != 0 {
				t.Fatalf("%+v starts=%d locks=%d", r, f.starts, f.locks)
			}
			if name == "PAUSED" && r.Reason != "PAUSED" {
				t.Fatal(r)
			}
		})
	}
}
func TestChangeDuringLockOrAttemptClaimRefuses2060(t *testing.T) {
	for _, stage := range []string{"lock", "claim"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t)
			f.run(t, 1)
			if stage == "lock" {
				f.onLock = func() { f.binding.SessionID = "new" }
			} else {
				f.store.onSave = func(r Record) {
					if len(r.Attempts) > 0 {
						f.desired.Reason = "PAUSED"
					}
				}
			}
			r := f.run(t, 2)
			if r.Status != Skipped || f.starts != 0 {
				t.Fatal(r)
			}
			wantAttempts := 1
			if stage == "lock" {
				wantAttempts = 0
			}
			if got := len(f.store.rows[f.target.WorkerID].Attempts); got != wantAttempts {
				t.Fatalf("%s: attempts=%d want%d", stage, got, wantAttempts)
			}
		})
	}
}
func TestDryRunDoesNotLockStoreLaunchLogOrNotify2060(t *testing.T) {
	f := newFixture(t)
	f.runner.DryRun = true
	r := f.run(t, 0)
	if r.Status != Planned || f.starts != 0 || f.locks != 0 || f.store.saves != 0 || f.logs != 0 || len(f.notices) != 0 {
		t.Fatalf("%+v %+v", r, f)
	}
}
func TestAttemptCapIncludesFailuresAndDoesNotSpam2060(t *testing.T) {
	f := newFixture(t)
	f.startErr = errors.New("launch refused")
	for s := uint64(1); s <= 6; s++ {
		f.runner.Sweep = s
		_, _ = f.runner.Execute(context.Background(), f.target)
	}
	if f.starts != 3 || f.claimedAtStart != 3 {
		t.Fatalf("starts=%d claimed=%d", f.starts, f.claimedAtStart)
	}
	for s := uint64(7); s <= 9; s++ {
		if r := f.run(t, s); r.Reason != RateLimited {
			t.Fatal(r)
		}
	}
	if f.starts != 3 || f.notices[LaunchFailed] != 1 || f.notices[RateLimited] != 1 {
		t.Fatalf("starts=%d notices=%v", f.starts, f.notices)
	}
}
func TestFutureAttemptsCannotBeDroppedOnClockRollback2060(t *testing.T) {
	f := newFixture(t)
	now := f.runner.Now()
	f.store.rows[f.target.WorkerID] = Record{Attempts: []time.Time{now.Add(time.Minute), now, now}}
	if r := f.run(t, 1); r.Reason != RateLimited || f.starts != 0 {
		t.Fatal(r)
	}
}
func TestProfileChangeBreaksContinuity2060(t *testing.T) {
	f := newFixture(t)
	f.run(t, 1)
	f.profile = "other-account-profile"
	if r := f.run(t, 2); r.Reason != Pending || f.starts != 0 {
		t.Fatal(r)
	}
}
func TestUnprovenOrExistingProcessNeverLaunches2060(t *testing.T) {
	for _, obs := range []Observation{{}, {Proven: true, PIDs: []int{9}}, {Proven: true, PIDs: []int{9, 10}}, {Proven: true, PIDs: []int{0}}} {
		f := newFixture(t)
		f.runner.Probe = func(context.Context, Target) (Observation, error) { return obs, nil }
		for sweep := uint64(1); sweep <= 2; sweep++ {
			r := f.run(t, sweep)
			// Pending is also Skipped. A first-sweep no-launch assertion alone
			// cannot distinguish a working refusal from the continuity delay.
			if r.Status != Skipped || r.Reason == Pending || f.starts != 0 || f.locks != 0 || f.store.saves != 0 {
				t.Fatalf("%+v starts=%d locks=%d saves=%d", r, f.starts, f.locks, f.store.saves)
			}
		}
	}
}
func TestJournalFailurePreventsLaunch2060(t *testing.T) {
	f := newFixture(t)
	f.run(t, 1)
	f.store.fail = true
	f.runner.Sweep = 2
	r, err := f.runner.Execute(context.Background(), f.target)
	if err == nil || f.starts != 0 || r.Reason != StateUnavailable {
		t.Fatalf("%+v %v starts=%d", r, err, f.starts)
	}
}
func TestPostLaunchMustBeExactlyOwnProcess2060(t *testing.T) {
	cases := map[string]func(*fixture){"none": func(f *fixture) { f.post.PIDs = nil }, "two": func(f *fixture) { f.post.PIDs = []int{42, 43} }, "unproven": func(f *fixture) { f.post.Proven = false }, "foreign": func(f *fixture) { f.post.PIDs = []int{99} }, "binding-drift": func(f *fixture) { f.onStart = func() { f.binding.SessionID = "new" } }, "server-pause": func(f *fixture) { f.onStart = func() { f.desired.Reason = "PAUSED" } }, "server-session": func(f *fixture) { f.onStart = func() { f.desired.SessionID = "rotated" } }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.run(t, 1)
			change(f)
			f.runner.Sweep = 2
			r, err := f.runner.Execute(context.Background(), f.target)
			if err == nil || r.Status != Failed || r.Reason != VerifyFailed || f.rolledBack != 1 || f.starts != 1 {
				t.Fatalf("%+v %v rollback=%d", r, err, f.rolledBack)
			}
		})
	}
}

func TestCancelledRunNeverLaunches2060(t *testing.T) {
	f := newFixture(t)
	f.run(t, 1)
	f.runner.Sweep = 2
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.runner.Execute(ctx, f.target)
	if !errors.Is(err, context.Canceled) || f.starts != 0 {
		t.Fatalf("err=%v starts=%d", err, f.starts)
	}
}
func TestPartialLaunchFailureRollsBackOnlyReturnedAgent2060(t *testing.T) {
	f := newFixture(t)
	f.runner.Prepare = func(context.Context, Target) (Prepared, error) {
		return Prepared{Fingerprint: "typed-profile-v1", Launch: func(context.Context) (Agent, error) {
			f.starts++
			return &fakeAgent{f: f, ref: "own-partial-terminal"}, errors.New("post-create failure")
		}}, nil
	}
	f.run(t, 1)
	f.runner.Sweep = 2
	r, err := f.runner.Execute(context.Background(), f.target)
	if err == nil || r.Status != Failed || f.rolledBack != 1 {
		t.Fatalf("%+v err=%v rollback=%d", r, err, f.rolledBack)
	}
}
func TestMissingLaunchReferenceNeverClaimsSuccess2060(t *testing.T) {
	f := newFixture(t)
	f.runner.Prepare = func(context.Context, Target) (Prepared, error) {
		return Prepared{Fingerprint: "typed-profile-v1", Launch: func(context.Context) (Agent, error) { f.starts++; f.launched = true; return &fakeAgent{f: f}, nil }}, nil
	}
	f.run(t, 1)
	f.runner.Sweep = 2
	r, err := f.runner.Execute(context.Background(), f.target)
	if err == nil || r.Status != Failed || f.rolledBack != 1 {
		t.Fatalf("%+v err=%v rollback=%d", r, err, f.rolledBack)
	}
}

func TestFailedRepairNoticeRetriesWithoutAnotherLaunch2060(t *testing.T) {
	f := newFixture(t)
	now := f.runner.Now()
	f.store.rows[f.target.WorkerID] = Record{Attempts: []time.Time{now, now, now}}
	calls := 0
	f.runner.Notice = func(context.Context, Target, Result) error {
		calls++
		if calls == 1 {
			return errors.New("transport unavailable")
		}
		return nil
	}
	first := f.run(t, 1)
	second := f.run(t, 2)
	if !first.NoticeFailed || second.NoticeFailed || calls != 2 || f.starts != 0 {
		t.Fatalf("first=%+v second=%+v calls=%d starts=%d", first, second, calls, f.starts)
	}
}
func TestNoProfileNoticeUsesProvenDarkCandidateWithoutSpendingBudget2060(t *testing.T) {
	f := newFixture(t)
	f.runner.Prepare = func(context.Context, Target) (Prepared, error) { return Prepared{}, errors.New("profile missing") }
	for sweep := uint64(1); sweep <= 3; sweep++ {
		f.runner.Sweep = sweep
		r, err := f.runner.Execute(context.Background(), f.target)
		if err == nil {
			t.Fatal("missing profile lost its refusal error")
		}
		if r.Reason != NoProfile || r.Attempt != 1 {
			t.Fatal(r)
		}
	}
	if f.starts != 0 || len(f.store.rows[f.target.WorkerID].Attempts) != 0 || f.notices[NoProfile] != 1 {
		t.Fatalf("starts=%d notices=%v record=%+v", f.starts, f.notices, f.store.rows[f.target.WorkerID])
	}
}

func TestFailureNoticeDoesNotHideADifferentTypedReason2060(t *testing.T) {
	f := newFixture(t)
	var reasons []string
	f.runner.Notice = func(_ context.Context, _ Target, r Result) error {
		reasons = append(reasons, r.FailureReason)
		return nil
	}
	for i, reason := range []string{FailureTerminalCreateRefused, FailureTimeout} {
		f.startErr = &LaunchError{Code: reason, Err: errors.New("private launch failure")}
		for sweep := uint64(i*2 + 1); sweep <= uint64(i*2+2); sweep++ {
			f.runner.Sweep = sweep
			_, _ = f.runner.Execute(context.Background(), f.target)
		}
	}
	if !reflect.DeepEqual(reasons, []string{FailureTerminalCreateRefused, FailureTimeout}) {
		t.Fatalf("notice reasons=%v", reasons)
	}
}

func TestObservedLiveAgentRearmsNoticeWithoutResettingBudget2060(t *testing.T) {
	f := newFixture(t)
	f.runner.Prepare = func(context.Context, Target) (Prepared, error) { return Prepared{}, errors.New("profile missing") }
	f.store.rows[f.target.WorkerID] = Record{Attempts: []time.Time{f.runner.Now()}}
	f.runner.Sweep = 1
	_, _ = f.runner.Execute(context.Background(), f.target)
	f.runner.Probe = func(context.Context, Target) (Observation, error) {
		return Observation{Proven: true, PIDs: []int{42}}, nil
	}
	before, saves := f.store.rows[f.target.WorkerID], f.store.saves
	f.runner.DryRun = true
	f.run(t, 2)
	if !reflect.DeepEqual(before, f.store.rows[f.target.WorkerID]) || f.store.saves != saves {
		t.Fatal("dry live observation changed the journal or notice marker")
	}
	f.runner.DryRun = false
	f.run(t, 2)
	f.runner.Probe = func(context.Context, Target) (Observation, error) { return Observation{Proven: true}, nil }
	f.runner.Sweep = 3
	_, _ = f.runner.Execute(context.Background(), f.target)
	if f.notices[NoProfile] != 2 || len(f.store.rows[f.target.WorkerID].Attempts) != 1 || f.starts != 0 {
		t.Fatalf("notices=%v record=%+v starts=%d", f.notices, f.store.rows[f.target.WorkerID], f.starts)
	}
}
