//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// hadron-cli#2201 C10 Ask 3: the sweep ends a SHORT_LIVED worker's open
// session when it has no open task and the strict census proves no agent runs,
// so the backstop does not wait for a fresh worker.end. A leave-alone worker,
// a long-lived one, an unproven census and a changed desired state never end
// anything.

// agentlessSource serves a snapshot per read, so a test can change the
// desired state between the first read and the guard read before the end.
type agentlessSource struct {
	mu     sync.Mutex
	reads  int
	nth    func(read int) RelaunchSnapshot
	failAt int
}

func (s *agentlessSource) RelaunchSnapshot(context.Context, string) (RelaunchSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.failAt != 0 && s.reads == s.failAt {
		return RelaunchSnapshot{}, errors.New("server unreachable")
	}
	return s.nth(s.reads), nil
}

type agentlessFixture struct {
	*loopFixture2060
	src     *agentlessSource
	ends    []AgentlessEnd
	endRes  AgentlessEndResult
	endErr  error
	census  *censusStub
	reports []SweepReport
}

func stoppedRow(session string) RelaunchWorker {
	return RelaunchWorker{WorkerID: "id-iris", Family: "claude", Desired: "STOPPED", Reason: "NO_OPEN_TASK", SessionID: session, Worktrees: []string{"repo/iris"}}
}

func newAgentlessFixture(t *testing.T, row RelaunchWorker) *agentlessFixture {
	t.Helper()
	f := &agentlessFixture{loopFixture2060: newLoopFixture2060(t), endRes: AgentlessEndResult{Ended: true}, census: &censusStub{obs: provenEmpty()}}
	snap := f.source.snapshot
	snap.Workers = []RelaunchWorker{row}
	f.src = &agentlessSource{nth: func(int) RelaunchSnapshot { return snap }}
	d := f.p.relaunch
	d.Source = f.src
	d.Probe = f.census.probe
	d.EndAgentless = func(_ context.Context, e AgentlessEnd) (AgentlessEndResult, error) {
		f.ends = append(f.ends, e)
		return f.endRes, f.endErr
	}
	d.ReportSweep = func(_ context.Context, r SweepReport) error { f.reports = append(f.reports, r); return nil }
	return f
}

func (f *agentlessFixture) sweep(t *testing.T) {
	t.Helper()
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
}

func (f *agentlessFixture) lines() []SweepReportLine {
	var out []SweepReportLine
	for _, r := range f.reports {
		out = append(out, r.Lines...)
	}
	return out
}

func TestSweepEndsAnAgentlessShortLivedSession2201(t *testing.T) {
	f := newAgentlessFixture(t, stoppedRow("still-open"))
	f.sweep(t)
	if len(f.ends) != 1 || f.ends[0] != (AgentlessEnd{AppID: "app-id", WorkerID: "id-iris", SessionID: "still-open"}) {
		t.Fatalf("ends = %+v", f.ends)
	}
	if f.census.calls() == 0 {
		t.Fatal("the end must rest on a census")
	}
	lines := f.lines()
	if len(lines) != 1 || lines[0].Outcome != "APPLIED" || lines[0].Reason != AgentAbsent {
		t.Fatalf("the run record must show the end asked for (AGENT_ABSENT) as applied: %+v", lines)
	}
	if len(f.launches) != 0 {
		t.Fatal("ending a session never launches")
	}
}

func TestSweepNeverEndsWithoutAProof2201(t *testing.T) {
	cases := map[string]*censusStub{
		"census unavailable":     {err: hostrelaunch.ErrCensusUnavailable},
		"census errored":         {err: errors.New("proc unreadable")},
		"observation not proven": {obs: hostrelaunch.Observation{PIDs: []int{}}},
		"an agent found":         {obs: hostrelaunch.Observation{Proven: true, PIDs: []int{7}}},
	}
	for name, census := range cases {
		f := newAgentlessFixture(t, stoppedRow("still-open"))
		f.census.obs, f.census.err = census.obs, census.err
		f.sweep(t)
		if len(f.ends) != 0 {
			t.Errorf("%s: ended a session with no proof: %+v", name, f.ends)
		}
	}
}

func TestSweepNeverEndsWhatItMustNotTouch2201(t *testing.T) {
	cases := map[string]RelaunchWorker{
		"leave-alone":                  {WorkerID: "id-iris", Family: "claude", Desired: "", Reason: "LEAVE_ALONE", SessionID: "still-open", Worktrees: []string{"repo/iris"}},
		"long-lived":                   {WorkerID: "id-iris", Family: "claude", Desired: "RUNNING", Reason: "LONG_LIVED", SessionID: "still-open", Worktrees: []string{"repo/iris"}},
		"holds a task":                 {WorkerID: "id-iris", Family: "claude", Desired: "RUNNING", Reason: "HOLDS_TASK", SessionID: "still-open", Worktrees: []string{"repo/iris"}},
		"paused":                       {WorkerID: "id-iris", Family: "claude", Desired: "STOPPED", Reason: "PAUSED", SessionID: "still-open", Worktrees: []string{"repo/iris"}},
		"team paused":                  {WorkerID: "id-iris", Family: "claude", Desired: "STOPPED", Reason: "TEAM_PAUSED", SessionID: "still-open", Worktrees: []string{"repo/iris"}},
		"halted":                       {WorkerID: "id-iris", Family: "claude", Desired: "STOPPED", Reason: "HALTED", SessionID: "still-open", Worktrees: []string{"repo/iris"}},
		"resetting":                    {WorkerID: "id-iris", Family: "claude", Desired: "STOPPED", Reason: "RESETTING", SessionID: "still-open", Worktrees: []string{"repo/iris"}},
		"retired":                      {WorkerID: "id-iris", Family: "claude", Desired: "STOPPED", Reason: "RETIRED", SessionID: "still-open", Worktrees: []string{"repo/iris"}},
		"no task, but desired RUNNING": {WorkerID: "id-iris", Family: "claude", Desired: "RUNNING", Reason: "NO_OPEN_TASK", SessionID: "still-open", Worktrees: []string{"repo/iris"}},
		"no open session":              stoppedRow(""),
		"no worktree":                  {WorkerID: "id-iris", Family: "claude", Desired: "STOPPED", Reason: "NO_OPEN_TASK", SessionID: "still-open"},
		"two worktrees":                {WorkerID: "id-iris", Family: "claude", Desired: "STOPPED", Reason: "NO_OPEN_TASK", SessionID: "still-open", Worktrees: []string{"repo/iris", "repo/other"}},
		"an unknown name":              {WorkerID: "id-iris", Family: "claude", Desired: "STOPPED", Reason: "NO_OPEN_TASK", SessionID: "still-open", Worktrees: []string{"repo/elsewhere"}},
		"a bad name token":             {WorkerID: "id-iris", Family: "claude", Desired: "STOPPED", Reason: "NO_OPEN_TASK", SessionID: "still-open", Worktrees: []string{"../escape"}},
	}
	for name, row := range cases {
		f := newAgentlessFixture(t, row)
		f.sweep(t)
		if len(f.ends) != 0 {
			t.Errorf("%s: a session was ended: %+v", name, f.ends)
		}
		if row.Reason == "LEAVE_ALONE" && f.census.calls() != 0 {
			t.Errorf("%s: a leave-alone worker is reported, never even looked at (%d census calls)", name, f.census.calls())
		}
	}
}

func TestSweepOutsideApplyEndsNothing2201(t *testing.T) {
	for _, mode := range []string{"OBSERVE", "OFF", ""} {
		f := newAgentlessFixture(t, stoppedRow("still-open"))
		snap := f.src.nth(1)
		snap.SweepMode = mode
		f.src.nth = func(int) RelaunchSnapshot { return snap }
		f.sweep(t)
		if len(f.ends) != 0 {
			t.Errorf("sweep mode %q ended a session", mode)
		}
	}
}

func TestSweepEndsOnlyOnAFreshMatchingState2201(t *testing.T) {
	changes := map[string]func(*RelaunchSnapshot){
		"the worker took a task":        func(s *RelaunchSnapshot) { s.Workers[0].Desired, s.Workers[0].Reason = "RUNNING", "HOLDS_TASK" },
		"the worker became leave-alone": func(s *RelaunchSnapshot) { s.Workers[0].Desired, s.Workers[0].Reason = "", "LEAVE_ALONE" },
		"the session changed":           func(s *RelaunchSnapshot) { s.Workers[0].SessionID = "a-newer-session" },
		"the sweep left APPLY":          func(s *RelaunchSnapshot) { s.SweepMode = "OBSERVE" },
		"the worker is gone":            func(s *RelaunchSnapshot) { s.Workers = nil },
	}
	for name, change := range changes {
		f := newAgentlessFixture(t, stoppedRow("still-open"))
		first := f.src.nth(1)
		f.src.nth = func(read int) RelaunchSnapshot {
			if read == 1 {
				return first
			}
			next := first
			next.Workers = append([]RelaunchWorker(nil), first.Workers...)
			change(&next)
			return next
		}
		f.sweep(t)
		if len(f.ends) != 0 {
			t.Errorf("%s: ended on a stale read: %+v", name, f.ends)
		}
	}
	// And an unreadable guard read is not a pass.
	f := newAgentlessFixture(t, stoppedRow("still-open"))
	f.src.failAt = 2
	f.sweep(t)
	if len(f.ends) != 0 {
		t.Errorf("an unreadable fresh state ended a session")
	}
}

func TestSweepAnAgentThisHostLaunchedIsNotAbsent2201(t *testing.T) {
	f := newAgentlessFixture(t, stoppedRow("still-open"))
	f.p.trackAgent("id-iris", "repo/iris", &fakeAgent{done: make(chan struct{}), pid: 4242})
	f.sweep(t)
	if len(f.ends) != 0 {
		t.Fatalf("a launched agent is alive: %+v", f.ends)
	}
}

func TestSweepSessionAlreadyEndedIsNotApplied2201(t *testing.T) {
	f := newAgentlessFixture(t, stoppedRow("still-open"))
	f.endRes = AgentlessEndResult{Ended: false}
	f.sweep(t)
	if len(f.ends) != 1 {
		t.Fatalf("the end was asked for once: %+v", f.ends)
	}
	for _, l := range f.lines() {
		if l.Outcome == "APPLIED" {
			t.Fatalf("a session that had already ended is not an applied end: %+v", l)
		}
	}
}

// A closed refusal is the server saying no to a state it can see (a task
// opened, another host launched the session, a long-lived worker): nothing
// failed, and a standing refusal must not write an S10 record on every pass.
// It is a skipped line carrying the server's code; only an unexpected error
// is FAILED.
func TestSweepAServerRefusalIsASkippedLineWithItsCode2201(t *testing.T) {
	for _, code := range []string{"WORKER_HAS_OPEN_TASK", "WORKER_NOT_SHORT_LIVED", "HOST_DOES_NOT_RUN_WORKER", "SESSION_NOT_WORKERS", AgentlessUnsupported} {
		f := newAgentlessFixture(t, stoppedRow("still-open"))
		f.endErr = &AgentlessRefusal{Code: code}
		f.sweep(t)
		if len(f.ends) != 1 {
			t.Fatalf("%s: the end was asked for once: %+v", code, f.ends)
		}
		if len(f.reports) != 0 {
			t.Errorf("%s: a standing refusal must not write an S10 record each pass: %+v", code, f.reports)
		}
	}
}

func TestSweepAnUnexpectedEndErrorIsFailedEndFailed2201(t *testing.T) {
	f := newAgentlessFixture(t, stoppedRow("still-open"))
	f.endErr = errors.New("connection reset")
	f.sweep(t)
	lines := f.lines()
	if len(lines) != 1 || lines[0].Outcome != "FAILED" || lines[0].Reason != "END_FAILED" {
		t.Fatalf("%+v", lines)
	}
}

func TestSweepWithoutAnEndDependencyEndsNothing2201(t *testing.T) {
	f := newAgentlessFixture(t, stoppedRow("still-open"))
	f.p.relaunch.EndAgentless = nil
	f.sweep(t)
	if len(f.ends) != 0 {
		t.Fatal("no dependency, no end")
	}
}
