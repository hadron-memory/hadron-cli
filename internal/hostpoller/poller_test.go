package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- fakes ----

type report struct{ id, outcome, worktree, reason string }

type fakeServer struct {
	mu        sync.Mutex
	regs      []Registration
	collects  int
	batches   [][]Operation
	reports   []report
	notDone   map[string]int // id -> NOT_DONE answers left before success
	endedOnce map[string]bool
	// reportErr fails every Report (a report that does not reach the server).
	reportErr error
}

func (s *fakeServer) Register(_ context.Context, r Registration) (RegisteredHost, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.regs = append(s.regs, r)
	ans := RegisteredHost{HostID: "h1"}
	for _, w := range r.Workers {
		ans.Workers = append(ans.Workers, RegisteredHostWorker{WorkerID: "id-" + w.Worker, Name: w.Worker})
	}
	return ans, nil
}

func (s *fakeServer) Collect(context.Context) ([]Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collects++
	if len(s.batches) == 0 {
		return nil, nil
	}
	b := s.batches[0]
	s.batches = s.batches[1:]
	return b, nil
}

func (s *fakeServer) Report(_ context.Context, id, outcome, worktree, reason string) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reportErr != nil {
		return Operation{}, s.reportErr
	}
	if outcome == Succeeded && s.notDone != nil {
		if n, ok := s.notDone[id]; ok {
			if n > 0 {
				s.notDone[id] = n - 1
				return Operation{}, ErrNotDone
			}
		}
	}
	s.reports = append(s.reports, report{id, outcome, worktree, reason})
	status := outcome
	if s.endedOnce[id] {
		status = Succeeded // the server records a FAILED end on an ended session as success
	}
	return Operation{ID: id, Status: status, Result: map[string]any{"handoff": true}}, nil
}

func (s *fakeServer) finalReports() []report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]report{}, s.reports...)
}

type fakeAgent struct {
	mu   sync.Mutex
	done chan struct{}
	pid  int
	// terminated counts Terminate calls, so a failed start's cleanup is
	// observable (#1474); stopFails makes Terminate report a stop that
	// could not be confirmed (the records must then be kept).
	terminated int
	stopFails  bool
}

func (a *fakeAgent) Done() <-chan struct{} { return a.done }
func (a *fakeAgent) ExitDetail() string    { return "exit status 1" }
func (a *fakeAgent) PID() int              { return a.pid }

func (a *fakeAgent) Terminate() (bool, string) {
	a.mu.Lock()
	a.terminated++
	a.mu.Unlock()
	if a.stopFails {
		return false, "the launched group did not exit within the grace"
	}
	return true, "terminated the fake"
}

func (a *fakeAgent) terminations() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.terminated
}

type fakeHost struct {
	mu       sync.Mutex
	trees    map[string]WorktreeState
	created  []string
	removed  []string
	launches []LaunchSpec
	prompts  [][]string
	agent    *fakeAgent
	occupant string
	// occupantErr fails Occupied (a scan the host cannot answer).
	occupantErr error
	// launchErr / promptErr fail Launch / Prompt; promptEnvs records each
	// prompt's environment.
	launchErr  error
	promptErr  error
	promptEnvs [][]string
	alive      map[int]bool
	// gate, when set, blocks Launch until closed (interleaving tests);
	// entered is closed when Launch reaches it.
	gate    chan struct{}
	entered chan struct{}
	// beforeLaunch, when set, runs as Launch is entered.
	beforeLaunch func()
}

func (h *fakeHost) Occupied(context.Context, string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.occupant, h.occupantErr
}

func (h *fakeHost) Alive(pid int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.alive[pid]
}

func (h *fakeHost) CreateWorktree(_ context.Context, _, path, branch, _ string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.created = append(h.created, path)
	h.trees[path] = WorktreeState{Exists: true, IsWorktree: true, Branch: branch}
	return nil
}

func (h *fakeHost) Inspect(_ context.Context, path string) (WorktreeState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.trees[path], nil
}

func (h *fakeHost) RemoveWorktree(_ context.Context, path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removed = append(h.removed, path)
	delete(h.trees, path)
	return nil
}

func (h *fakeHost) Launch(_ context.Context, spec LaunchSpec) (Agent, error) {
	if h.beforeLaunch != nil {
		h.beforeLaunch()
	}
	if h.gate != nil {
		if h.entered != nil {
			close(h.entered)
		}
		<-h.gate
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.launchErr != nil {
		return nil, h.launchErr
	}
	h.launches = append(h.launches, spec)
	if h.agent == nil {
		h.agent = &fakeAgent{done: make(chan struct{})}
	}
	return h.agent, nil
}

func (h *fakeHost) Prompt(_ context.Context, argv []string, _ string, env []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prompts = append(h.prompts, argv)
	h.promptEnvs = append(h.promptEnvs, env)
	return h.promptErr
}

const hostKey = "hk_secret_host_key_value"

func testPoller(t *testing.T) (*Poller, *fakeServer, *fakeHost) {
	t.Helper()
	root := t.TempDir()
	cfg := &Config{
		App: "hrn:app:acme.com:team", Label: "box1", PollIntervalSeconds: 30,
		WorktreesRoot: root, StateFile: filepath.Join(root, "state.json"),
		Repos: map[string]string{"acme/repo": "/src/repo"},
		Workers: map[string]WorkerConfig{
			"iris": {Family: "claude", Launch: []string{"claude-worker", "--model", "opus"},
				End: []string{"orca-send", "iris", "end"}, EndWait: "20ms", Worktrees: []string{"repo/iris"},
				Recover: []string{"orca-send", "iris", "recover"}},
		},
	}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	srv := &fakeServer{notDone: map[string]int{}, endedOnce: map[string]bool{}}
	host := &fakeHost{trees: map[string]WorktreeState{
		cfg.WorktreePath("repo/iris"): {Exists: true, IsWorktree: true, Branch: "iris/work", Upstream: "origin/iris/work"},
	}}
	p := New(cfg, srv, host, st, hostKey, nil)
	// A fake clock: sleeping advances it, so bind waits run instantly.
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	var clock sync.Mutex
	p.now = func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	p.sleep = func(ctx context.Context, d time.Duration) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		clock.Lock()
		now = now.Add(d)
		clock.Unlock()
		return nil
	}
	return p, srv, host
}

func step(t *testing.T, p *Poller) {
	t.Helper()
	if err := p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.Wait()
}

func only(t *testing.T, s *fakeServer) report {
	t.Helper()
	r := s.finalReports()
	if len(r) != 1 {
		t.Fatalf("want exactly one report, got %+v", r)
	}
	return r[0]
}

// ---- the closed set ----

func TestUnknownTypeIsRefusedLocallyAndReported(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.batches = [][]Operation{{{ID: "o1", Type: "shell.exec", Args: map[string]any{"cmd": "rm -rf /"}}}}
	step(t, p)
	r := only(t, srv)
	if r.outcome != Failed || !strings.Contains(r.reason, "unknown operation type") || !strings.Contains(r.reason, "cor:agt:070:03") {
		t.Errorf("unknown type must be reported FAILED with the rule: %+v", r)
	}
	if len(host.launches)+len(host.created)+len(host.prompts) != 0 {
		t.Error("an unknown type must touch nothing on the host")
	}
}

func TestDeliverFailsWithNoDeliveryPath(t *testing.T) {
	p, srv, _ := testPoller(t)
	srv.batches = [][]Operation{{{ID: "o1", Type: OpWorkerDeliver, WorkerID: "id-iris"}}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "no delivery path") {
		t.Errorf("deliver: %+v", r)
	}
}

// ---- registration ----

func TestRegistersEveryStepWithTheWholeWorkerList(t *testing.T) {
	p, srv, _ := testPoller(t)
	step(t, p)
	step(t, p)
	if len(srv.regs) != 2 {
		t.Fatalf("register on every poll: got %d", len(srv.regs))
	}
	r := srv.regs[0]
	if r.Label != "box1" || r.PollIntervalSeconds != 30 || len(r.Workers) != 1 {
		t.Fatalf("registration: %+v", r)
	}
	if w := r.Workers[0]; w.Worker != "iris" || w.Family != "claude" || strings.Join(w.Worktrees, ",") != "repo/iris" {
		t.Errorf("worker: %+v", w)
	}
}

func TestUnmatchedRegisteredWorkerGetsNoOperations(t *testing.T) {
	got := refFor(RegisteredHostWorker{WorkerID: "x", Name: "someone"}, []RegisteredWorker{{Worker: "iris"}})
	if got != "" {
		t.Errorf("an unmatched worker must not be guessed: %q", got)
	}
	if got := refFor(RegisteredHostWorker{WorkerID: "x", Name: "Iris"}, []RegisteredWorker{{Worker: "iris"}}); got != "iris" {
		t.Errorf("names match case-insensitively: %q", got)
	}
}

// ---- dedupe ----

func TestReDeliveredOperationRunsOnce(t *testing.T) {
	p, srv, host := testPoller(t)
	op := Operation{ID: "o1", Type: OpWorktreeCreate, WorkerID: "id-iris",
		Args: map[string]any{"repo": "acme/repo", "baseBranch": "main", "branchName": "iris/x", "label": "x"}}
	// Hold the first run in flight while the same op is collected again.
	p.mu.Lock()
	p.inFlight["o1"] = true
	p.mu.Unlock()
	srv.batches = [][]Operation{{op}}
	step(t, p)
	if len(host.created) != 0 || len(srv.finalReports()) != 0 {
		t.Error("an operation in flight must not run again when re-collected")
	}
}

// ---- worktree.create ----

func TestWorktreeCreateReportsTheTokenAndRegistersIt(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.batches = [][]Operation{{{ID: "o1", Type: OpWorktreeCreate, WorkerID: "id-iris",
		Args: map[string]any{"repo": "acme/repo", "baseBranch": "main", "branchName": "iris/x", "label": "x"}}}}
	step(t, p)
	r := only(t, srv)
	if r.outcome != Succeeded || r.worktree != "repo/x" {
		t.Errorf("create: %+v", r)
	}
	if len(host.created) != 1 {
		t.Fatal("git worktree add must run once")
	}
	step(t, p)
	last := srv.regs[len(srv.regs)-1].Workers[0].Worktrees
	if strings.Join(last, ",") != "repo/iris,repo/x" {
		t.Errorf("the next registration must list the created worktree: %v", last)
	}
}

func TestWorktreeCreateRefusesAnUnconfiguredRepo(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.batches = [][]Operation{{{ID: "o1", Type: OpWorktreeCreate, WorkerID: "id-iris",
		Args: map[string]any{"repo": "evil/repo", "baseBranch": "main", "branchName": "b", "label": "x"}}}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "not configured") {
		t.Errorf("%+v", r)
	}
	if len(host.created) != 0 {
		t.Error("nothing may be created for an unconfigured repo")
	}
}

func TestWorktreeCreateIsIdempotentOnRedelivery(t *testing.T) {
	p, srv, host := testPoller(t)
	host.trees[p.cfg.WorktreePath("repo/x")] = WorktreeState{Exists: true, IsWorktree: true, Branch: "iris/x"}
	srv.batches = [][]Operation{{{ID: "o1", Type: OpWorktreeCreate, WorkerID: "id-iris",
		Args: map[string]any{"repo": "acme/repo", "baseBranch": "main", "branchName": "iris/x", "label": "x"}}}}
	step(t, p)
	if r := only(t, srv); r.outcome != Succeeded || r.worktree != "repo/x" {
		t.Errorf("%+v", r)
	}
	if len(host.created) != 0 {
		t.Error("an existing tree on the branch must not be re-added")
	}
}

// ---- worktree.remove ----

func TestWorktreeRemoveRefusesAnythingWithWorkToLose(t *testing.T) {
	cases := []struct {
		name string
		st   WorktreeState
		want string
	}{
		{"dirty", WorktreeState{Exists: true, IsWorktree: true, Branch: "b", Upstream: "origin/b", Dirty: true}, "uncommitted"},
		{"no upstream", WorktreeState{Exists: true, IsWorktree: true, Branch: "b"}, "no upstream"},
		{"unpushed", WorktreeState{Exists: true, IsWorktree: true, Branch: "b", Upstream: "origin/b", Ahead: 2}, "2 commit(s)"},
		{"not a worktree", WorktreeState{Exists: true}, "not a git worktree"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, srv, host := testPoller(t)
			host.trees[p.cfg.WorktreePath("repo/iris")] = c.st
			srv.batches = [][]Operation{{{ID: "o1", Type: OpWorktreeRemove, WorkerID: "id-iris", Args: map[string]any{"worktree": "repo/iris"}}}}
			step(t, p)
			if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, c.want) {
				t.Errorf("%+v", r)
			}
			if len(host.removed) != 0 {
				t.Error("a tree with work to lose must not be removed")
			}
		})
	}
}

func TestWorktreeRemoveRemovesACleanPushedTree(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.batches = [][]Operation{{{ID: "o1", Type: OpWorktreeRemove, WorkerID: "id-iris", Args: map[string]any{"worktree": "repo/iris"}}}}
	step(t, p)
	if r := only(t, srv); r.outcome != Succeeded {
		t.Errorf("%+v", r)
	}
	if len(host.removed) != 1 {
		t.Error("a clean, pushed tree is removed")
	}
}

// ---- worker.start ----

func startOp() Operation {
	return Operation{ID: "s1", Type: OpWorkerStart, WorkerID: "id-iris", LaunchGrant: "hlg_grant",
		Args: map[string]any{"workerId": "id-iris", "worktree": "repo/iris", "family": "claude", "taskId": "t1"}}
}

func TestWorkerStartHandsOnlyTheGrantToTheAgent(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "person_token")
	t.Setenv(HostKeyEnv, hostKey)
	t.Setenv("SOME_VAR", "carries "+hostKey+" inside")
	t.Setenv("PATH_LIKE", "/usr/bin")
	p, srv, host := testPoller(t)
	srv.notDone["s1"] = 3 // the pre-check, then two more polls before the bind
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Succeeded {
		t.Fatalf("start must report SUCCEEDED once bound: %+v", r)
	}
	if len(host.launches) != 1 {
		t.Fatalf("launched %d times", len(host.launches))
	}
	spec := host.launches[0]
	if strings.Join(spec.Argv, " ") != "claude-worker --model opus" {
		t.Errorf("argv must be the configured one: %v", spec.Argv)
	}
	if spec.Dir != p.cfg.WorktreePath("repo/iris") {
		t.Errorf("dir: %s", spec.Dir)
	}
	var grant bool
	for _, kv := range spec.Env {
		if strings.Contains(kv, hostKey) {
			t.Errorf("the host key reached the agent's environment: %q", kv)
		}
		if strings.HasPrefix(kv, "HADRON_TOKEN=") || strings.HasPrefix(kv, HostKeyEnv+"=") {
			t.Errorf("a credential variable reached the agent: %q", kv)
		}
		if kv == "HADRON_LAUNCH_GRANT=hlg_grant" {
			grant = true
		}
	}
	if !grant {
		t.Error("the agent must get HADRON_LAUNCH_GRANT")
	}
	if !envHas(spec.Env, "PATH_LIKE=/usr/bin") {
		t.Error("the rest of the environment is passed through")
	}
	for _, a := range spec.Argv {
		if strings.Contains(a, hostKey) || strings.Contains(a, "hlg_") {
			t.Errorf("no credential in argv: %q", a)
		}
	}
}

// HADRON_HOST_KEY is scrubbed by name, not only by value: a poller reading
// its key from key_file may still carry another host key in the variable.
func TestWorkerStartScrubsTheHostKeyVariableByName(t *testing.T) {
	t.Setenv(HostKeyEnv, "hk_some_other_key")
	p, srv, host := testPoller(t)
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	for _, kv := range host.launches[0].Env {
		if strings.HasPrefix(kv, HostKeyEnv+"=") {
			t.Errorf("%s reached the agent: %q", HostKeyEnv, kv)
		}
	}
}

func envHas(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

// A start re-delivered while it waits for its bind is not launched again.
func TestReDeliveredStartIsNotRelaunched(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.notDone["s1"] = 1 << 20
	release := make(chan struct{})
	p.sleep = func(ctx context.Context, d time.Duration) error {
		<-release // hold the first start in its bind wait
		return context.Canceled
	}
	srv.batches = [][]Operation{{startOp()}, {startOp()}}
	if err := p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	p.Wait()
	if len(host.launches) != 1 {
		t.Errorf("a re-delivered start in flight must not launch again: %d launches", len(host.launches))
	}
}

func TestWorkerStartAlreadyBoundIsNotRelaunched(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.batches = [][]Operation{{startOp()}} // no NOT_DONE: the session is bound
	step(t, p)
	if len(host.launches) != 0 {
		t.Error("a start whose session is already bound must not launch again")
	}
	if r := only(t, srv); r.outcome != Succeeded {
		t.Errorf("%+v", r)
	}
}

func TestWorkerStartFailsWhenNothingBindsInTheGrantLifetime(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.notDone["s1"] = 1 << 20
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "no session was bound") {
		t.Errorf("%+v", r)
	}
	// #1474: a start that fails stops what it launched and leaves no agents
	// entry behind — the crash-looping wrapper of the incident must not
	// outlive the FAILED report.
	if host.agent == nil || host.agent.terminations() != 1 {
		t.Error("a start whose session never binds must terminate the process group it launched")
	}
	if st, err := LoadState(p.cfg.StateFile); err != nil {
		t.Fatal(err)
	} else if len(st.Agents) != 0 {
		t.Errorf("a failed start must leave no agents entry in the state file: %+v", st.Agents)
	}
}

func TestWorkerStartFailsWhenTheAgentExitsFirst(t *testing.T) {
	p, srv, host := testPoller(t)
	host.agent = &fakeAgent{done: make(chan struct{})}
	close(host.agent.done)
	srv.notDone["s1"] = 1 << 20
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "exited before binding") {
		t.Errorf("%+v", r)
	}
	// #1474: the failure still drops the state entry (the process is gone;
	// nothing may be signalled for it).
	if st, err := LoadState(p.cfg.StateFile); err != nil {
		t.Fatal(err)
	} else if len(st.Agents) != 0 {
		t.Errorf("a failed start must leave no agents entry in the state file: %+v", st.Agents)
	}
}

// #1474 review (Jane F2): the deadline must not act on a stale NOT_DONE
// sample. A bind that lands at the deadline — after the last pre-deadline
// NOT_DONE answer — is a success; the deadline only fails a start when the
// server, asked after the deadline, still says nothing bound.
func TestWorkerStartDeadlineHonoursABindThatLandsLate(t *testing.T) {
	p, srv, host := testPoller(t)
	p.BindPoll = time.Second
	// NOT_DONE through the last pre-deadline ask; the first ask at the
	// deadline finds the bind (the pre-launch ask is counted too).
	srv.notDone["s1"] = int(LaunchGrantTTL / time.Second)
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Succeeded {
		t.Errorf("a bind landing at the deadline is a success: %+v", r)
	}
	if host.agent == nil || host.agent.terminations() != 0 {
		t.Error("a bound start is never terminated")
	}
	st, err := LoadState(p.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Agents["id-iris"]; !ok {
		t.Error("a successful start keeps its agents entry")
	}
}

// #1474 review (Jane F2), the agent-exit half: an exited agent's bind
// report can be delayed behind an unclear answer. The exit alone must stop
// nothing; the server's answer decides.
func TestWorkerStartExpiringAgentHonoursABindReportedLate(t *testing.T) {
	p, srv, host := testPoller(t)
	host.agent = &fakeAgent{done: make(chan struct{})}
	close(host.agent.done)
	// The pre-launch ask answers NOT_DONE (so the start launches); the
	// loop's first ask fails unclear; the next carries the bind.
	srv.notDone["s1"] = 1
	p.server = &flakyServer{fakeServer: srv, fails: 1}
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Succeeded {
		t.Errorf("a bind reported behind an unclear answer is a success: %+v", r)
	}
	if host.agent == nil || host.agent.terminations() != 0 {
		t.Error("an exited agent is not stopped before the answer is authoritative")
	}
	st, err := LoadState(p.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Agents["id-iris"]; !ok {
		t.Error("a successful start keeps its agents entry")
	}
}

// flakyServer answers the first `fails` post-launch reports (every call but
// the pre-launch ask) with a transport error, then delegates: a delayed but
// valid answer.
type flakyServer struct {
	*fakeServer
	fails int
	calls int
}

func (f *flakyServer) Report(ctx context.Context, id, outcome, worktree, reason string) (Operation, error) {
	f.calls++
	if f.calls > 1 && f.fails > 0 {
		f.fails--
		return Operation{}, context.DeadlineExceeded
	}
	return f.fakeServer.Report(ctx, id, outcome, worktree, reason)
}

// A start into a worktree another process is already working in is refused
// before anything launches, naming the process (#1474: the /proc check
// worktree.remove uses, which sees agents an older wrapper runs in a
// terminal — invisible to this poller's own records).
func TestWorkerStartRefusesAnOccupiedTreeBeforeLaunching(t *testing.T) {
	t.Run("occupied", func(t *testing.T) {
		p, srv, host := testPoller(t)
		host.occupant = "pid 4242, worker-supervise --wrapper cedric"
		srv.notDone["s1"] = 1
		srv.batches = [][]Operation{{startOp()}}
		step(t, p)
		r := only(t, srv)
		if r.outcome != Failed || !strings.Contains(r.reason, "4242") || !strings.Contains(r.reason, "worker-supervise --wrapper cedric") {
			t.Errorf("the refusal must name the pid and the argv: %+v", r)
		}
		if len(host.launches) != 0 {
			t.Error("an occupied worktree must launch nothing")
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		p, srv, host := testPoller(t)
		host.occupantErr = fmt.Errorf("cannot read /proc")
		srv.notDone["s1"] = 1
		srv.batches = [][]Operation{{startOp()}}
		step(t, p)
		r := only(t, srv)
		if r.outcome != Failed || !strings.Contains(r.reason, "could not tell") {
			t.Errorf("an unreadable scan fails closed: %+v", r)
		}
		if len(host.launches) != 0 {
			t.Error("an unreadable scan must launch nothing")
		}
	})
}

// The cleanup belongs to failure only: a start that binds leaves its agent
// running and tracked.
func TestWorkerStartSuccessKeepsTheAgentRunning(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.notDone["s1"] = 1 // the pre-launch ask only
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Succeeded {
		t.Fatalf("%+v", r)
	}
	if host.agent == nil || host.agent.terminations() != 0 {
		t.Error("a start whose session binds must not be terminated")
	}
	st, err := LoadState(p.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Agents["id-iris"]; !ok {
		t.Error("a successful start keeps its agents entry")
	}
}

func TestWorkerStartRefusesAWrongFamilyOrUnregisteredTree(t *testing.T) {
	for name, mut := range map[string]func(*Operation){
		"family":   func(o *Operation) { o.Args["family"] = "codex" },
		"worktree": func(o *Operation) { o.Args["worktree"] = "repo/other" },
		"grant":    func(o *Operation) { o.LaunchGrant = "" },
	} {
		t.Run(name, func(t *testing.T) {
			p, srv, host := testPoller(t)
			srv.notDone["s1"] = 1
			op := startOp()
			mut(&op)
			srv.batches = [][]Operation{{op}}
			step(t, p)
			if r := only(t, srv); r.outcome != Failed {
				t.Errorf("%+v", r)
			}
			if len(host.launches) != 0 {
				t.Error("nothing may launch")
			}
		})
	}
}

// ---- worker.end ----

func endOp() Operation {
	return Operation{ID: "e1", Type: OpWorkerEnd, WorkerID: "id-iris", Args: map[string]any{"workerId": "id-iris", "sessionId": "sess1"}}
}

func TestWorkerEndNeverReportsSucceeded(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.endedOnce["e1"] = true // the worker did end its session
	srv.batches = [][]Operation{{endOp()}}
	step(t, p)
	r := only(t, srv)
	if r.outcome != Failed {
		t.Fatalf("worker.end must report FAILED and let the server record the end: a SUCCEEDED force-ends the session (%+v)", r)
	}
	if len(host.prompts) != 1 || host.prompts[0][0] != "orca-send" {
		t.Errorf("the configured end prompt must run: %v", host.prompts)
	}
	if _, kept := p.state.keepReason("repo/iris"); kept {
		t.Error("an end the server recorded as success keeps nothing")
	}
}

func TestFailedWorkerEndKeepsTheWorktree(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.batches = [][]Operation{{endOp()}, {{ID: "r1", Type: OpWorktreeRemove, WorkerID: "id-iris", Args: map[string]any{"worktree": "repo/iris"}}}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "did not end within") {
		t.Fatalf("%+v", r)
	}
	step(t, p)
	rs := srv.finalReports()
	if len(rs) != 2 || rs[1].outcome != Failed || !strings.Contains(rs[1].reason, "kept") {
		t.Fatalf("a remove after a failed end must be refused: %+v", rs)
	}
	if len(host.removed) != 0 {
		t.Error("the worktree must survive a failed end")
	}
	// It survives a restart: the keep is in the state file.
	st, err := LoadState(p.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := st.keepReason("repo/iris"); !kept {
		t.Error("the keep must be durable")
	}
}

// ---- config ----

func TestConfigRefusesWhatTheServerWould(t *testing.T) {
	base := func() Config {
		return Config{App: "a", Label: "l", WorktreesRoot: "/w",
			Workers: map[string]WorkerConfig{"iris": {Family: "claude", Launch: []string{"x"}}}}
	}
	cases := map[string]func(*Config){
		"no app":        func(c *Config) { c.App = "" },
		"relative root": func(c *Config) { c.WorktreesRoot = "w" },
		"bad family":    func(c *Config) { c.Workers["iris"] = WorkerConfig{Family: "Claude Code", Launch: []string{"x"}} },
		"no launch":     func(c *Config) { c.Workers["iris"] = WorkerConfig{Family: "claude"} },
		"bad worktree": func(c *Config) {
			c.Workers["iris"] = WorkerConfig{Family: "claude", Launch: []string{"x"}, Worktrees: []string{"../up"}}
		},
		"bad interval":  func(c *Config) { c.PollIntervalSeconds = 90000 },
		"no workers":    func(c *Config) { c.Workers = nil },
		"relative repo": func(c *Config) { c.Repos = map[string]string{"a/b": "rel"} },
		"bad end_wait": func(c *Config) {
			c.Workers["iris"] = WorkerConfig{Family: "claude", Launch: []string{"x"}, EndWait: "soon"}
		},
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			c := base()
			mut(&c)
			if err := c.validate(); err == nil {
				t.Error("must refuse")
			}
		})
	}
	c := base()
	if err := c.validate(); err != nil {
		t.Errorf("base config must validate: %v", err)
	}
}

func TestHostKeyNeverComesFromTheTokenChannel(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "person_token")
	t.Setenv(HostKeyEnv, "")
	c := &Config{}
	if _, err := c.ReadHostKey(); err == nil {
		t.Error("HADRON_TOKEN must never serve as the host key")
	}
}

func TestHostKeyFileMustNotBeReadableByOthers(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte("hk_x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &Config{KeyFile: key}
	if _, err := c.ReadHostKey(); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("a world-readable key file must be refused: %v", err)
	}
	if err := os.Chmod(key, 0o600); err != nil {
		t.Fatal(err)
	}
	if k, err := c.ReadHostKey(); err != nil || k != "hk_x" {
		t.Errorf("a 0600 key file is read: %q %v", k, err)
	}
}

func TestHostKeyIsNeverReadFromTheEnvironment(t *testing.T) {
	t.Setenv(HostKeyEnv, "hk_from_env")
	if _, err := (&Config{}).ReadHostKey(); err == nil {
		t.Error("HADRON_HOST_KEY must not serve as the host key: a same-user agent can read the poller's environment")
	}
}

// ---- review-walk fixes ----

func TestWorktreeRemoveRefusesATreeNotRegisteredForItsWorker(t *testing.T) {
	p, srv, host := testPoller(t)
	other := p.cfg.WorktreePath("repo/someone-else")
	host.trees[other] = WorktreeState{Exists: true, IsWorktree: true, Branch: "b", Upstream: "origin/b"}
	srv.batches = [][]Operation{{{ID: "r1", Type: OpWorktreeRemove, WorkerID: "id-iris", Args: map[string]any{"worktree": "repo/someone-else"}}}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "not registered for this worker") {
		t.Errorf("%+v", r)
	}
	if len(host.removed) != 0 {
		t.Error("another worker's tree must never be removed")
	}
}

func TestWorkerStartRefusesATreeWithALiveAgent(t *testing.T) {
	p, srv, host := testPoller(t)
	p.trackAgent("id-other", "repo/iris", &fakeAgent{done: make(chan struct{})})
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "still running") {
		t.Errorf("%+v", r)
	}
	if len(host.launches) != 0 {
		t.Error("one agent per worktree")
	}
}

// #1474 review P2-3: the stop happens only on the outcome the refusal NAMES.
// A recorded FAILED stops what was launched; a recorded SUCCEEDED — the agent
// IS the bound start — and a refusal naming nothing both leave it running,
// because a kill there would rest on an inferred server contract.
func TestWorkerStartActsOnTheOutcomeTheServerNamed(t *testing.T) {
	for name, tc := range map[string]struct {
		recorded string // the outcome the server's refusal names ("" = none)
		stops    bool
	}{
		"recorded failed":    {Failed, true},
		"recorded succeeded": {Succeeded, false},
		"outcome not named":  {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			p, srv, host := testPoller(t)
			p.server = &finalServer{fakeServer: srv, recorded: tc.recorded}
			srv.batches = [][]Operation{{startOp()}}
			step(t, p)
			if host.agent == nil {
				t.Fatal("the agent was launched")
			}
			if got, want := host.agent.terminations() != 0, tc.stops; got != want {
				t.Errorf("terminations=%d, want a stop: %v", host.agent.terminations(), tc.stops)
			}
			st, err := LoadState(p.cfg.StateFile)
			if err != nil {
				t.Fatal(err)
			}
			if tc.stops && len(st.Agents) != 0 {
				t.Errorf("a confirmed stop leaves no agents entry: %+v", st.Agents)
			}
			if !tc.stops {
				if _, ok := st.Agents["id-iris"]; !ok {
					t.Error("an un-named or successful outcome keeps the agents entry")
				}
				if !p.agentRunningIn("repo/iris") {
					t.Error("a kept agent must stay visible to the one-agent guard")
				}
			}
		})
	}
}

// The refusal a real server sends carries what it recorded as message text;
// ErrFinalWithRecorded reads exactly that and nothing else.
func TestErrFinalWithRecorded(t *testing.T) {
	for name, tc := range map[string]struct {
		msg      string
		recorded string
	}{
		"failed":      {"This operation was already reported failed.", Failed},
		"succeeded":   {"This operation was already reported succeeded.", Succeeded},
		"other words": {"the operation is closed", ""},
		"empty":       {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			e := ErrFinalWithRecorded(tc.msg)
			if e.Recorded != tc.recorded {
				t.Errorf("Recorded=%q, want %q", e.Recorded, tc.recorded)
			}
			if !errors.Is(e, ErrFinal) {
				t.Error("a FinalError is ErrFinal")
			}
			var fin *FinalError
			if !errors.As(error(e), &fin) || fin.Recorded != tc.recorded {
				t.Errorf("errors.As must recover the recorded outcome: %+v", fin)
			}
		})
	}
}

// A stop that could not be confirmed keeps every record (#1474 review P2-2):
// the agent stays in memory and in the state file, so the
// one-agent-per-worktree guard can still see it.
func TestWorkerStartKeepsTheAgentWhenTheStopIsUnconfirmed(t *testing.T) {
	p, srv, host := testPoller(t)
	host.agent = &fakeAgent{done: make(chan struct{}), stopFails: true}
	srv.notDone["s1"] = 1 << 20
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "no session was bound") {
		t.Errorf("%+v", r)
	}
	if host.agent.terminations() != 1 {
		t.Fatal("the failed start still tries the stop")
	}
	st, err := LoadState(p.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Agents["id-iris"]; !ok {
		t.Error("an unconfirmed stop keeps the state-file entry")
	}
	if !p.agentRunningIn("repo/iris") {
		t.Error("an unconfirmed stop keeps the in-memory record")
	}
}

// finalServer answers NOT_DONE to the first SUCCEEDED and a final refusal
// after, shaped as the server's own answer: the refusal carries what the
// server recorded ("This operation was already reported <status>." — the
// real handler; same-outcome re-asks are answered with the operation, never
// refused). recorded "" names nothing.
type finalServer struct {
	*fakeServer
	asks     int
	recorded string
}

func (f *finalServer) Report(ctx context.Context, id, outcome, worktree, reason string) (Operation, error) {
	f.asks++
	if f.asks == 1 {
		return Operation{}, ErrNotDone
	}
	msg := "This operation was already reported."
	if f.recorded != "" {
		msg = "This operation was already reported " + strings.ToLower(f.recorded) + "."
	}
	return Operation{}, ErrFinalWithRecorded(msg)
}

func TestWorkerEndIgnoresAnAgentThatExitedLongAgo(t *testing.T) {
	p, srv, _ := testPoller(t)
	gone := &fakeAgent{done: make(chan struct{})}
	close(gone.done)
	p.trackAgent("id-iris", "repo/iris", gone)
	srv.batches = [][]Operation{{endOp()}}
	step(t, p)
	if r := only(t, srv); !strings.Contains(r.reason, "did not end within") {
		t.Errorf("a long-exited agent must not cut the wait short: %+v", r)
	}
}

func TestRegistrationSavesTheHostID(t *testing.T) {
	p, _, _ := testPoller(t)
	step(t, p)
	st, err := LoadState(p.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if st.RegisteredHostID() != "h1" {
		t.Errorf("host id: %q", st.RegisteredHostID())
	}
}

// A stop signal lets a worktree operation finish and report.
func TestStoppingFinishesAWorktreeOperation(t *testing.T) {
	p, srv, host := testPoller(t)
	ctx, cancel := context.WithCancel(context.Background())
	srv.batches = [][]Operation{{{ID: "o1", Type: OpWorktreeCreate, WorkerID: "id-iris",
		Args: map[string]any{"repo": "acme/repo", "baseBranch": "main", "branchName": "iris/x", "label": "x"}}}}
	if err := p.register(ctx); err != nil {
		t.Fatal(err)
	}
	ops, _ := srv.Collect(ctx)
	cancel() // the stop arrives before the operation runs
	p.carryOut(ctx, ops[0])
	if len(host.created) != 1 {
		t.Error("the worktree operation must run to the end")
	}
	if r := only(t, srv); r.outcome != Succeeded {
		t.Errorf("and report: %+v", r)
	}
}

// hadron-server#1929: a start whose grant was consumed comes back to a
// restarted host with launchGrant null. Its session is bound, so it is
// reported SUCCEEDED, never failed for the missing grant.
func TestConsumedStartWithNullGrantIsReportedSucceeded(t *testing.T) {
	p, srv, host := testPoller(t)
	op := startOp()
	op.LaunchGrant = ""
	srv.batches = [][]Operation{{op}}
	step(t, p)
	if r := only(t, srv); r.outcome != Succeeded {
		t.Errorf("%+v", r)
	}
	if len(host.launches) != 0 {
		t.Error("nothing launches for a bound start")
	}
}

// ---- blind-review fixes (Anne, #11490) ----

func removeOp() Operation {
	return Operation{ID: "r1", Type: OpWorktreeRemove, WorkerID: "id-iris", Args: map[string]any{"workerId": "id-iris", "worktree": "repo/iris"}}
}

// P2-1: an agent launched before a restart is still in the state file, and
// a live one keeps its tree.
func TestRestartKeepsALiveAgentsTree(t *testing.T) {
	p, srv, host := testPoller(t)
	if err := p.state.recordAgent("id-iris", "repo/iris", 4242, ""); err != nil {
		t.Fatal(err)
	}
	// A fresh poller over the same state file: nothing in memory.
	st, err := LoadState(p.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	host.alive = map[int]bool{4242: true}
	q := New(p.cfg, srv, host, st, hostKey, nil)
	srv.batches = [][]Operation{{removeOp()}}
	step(t, q)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "still running") {
		t.Errorf("%+v", r)
	}
	if len(host.removed) != 0 {
		t.Error("a live agent's tree must survive a poller restart")
	}
}

// P2-1, the agents nobody recorded: any process working in the tree refuses.
func TestRemoveRefusesATreeAProcessIsWorkingIn(t *testing.T) {
	p, srv, host := testPoller(t)
	host.occupant = "pid 9, bash"
	srv.batches = [][]Operation{{removeOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "pid 9") {
		t.Errorf("%+v", r)
	}
	if len(host.removed) != 0 {
		t.Error("an occupied tree must not be removed")
	}
}

// P2-2: a remove collected with a start for the same worker waits for the
// start, then sees its agent. The launch is held at a barrier: were the
// remove not ordered behind it, it would run (and remove the clean tree)
// while the start is blocked.
func TestRemoveWaitsForAStartOnTheSameWorker(t *testing.T) {
	p, srv, host := testPoller(t)
	host.gate = make(chan struct{})
	host.entered = make(chan struct{})
	srv.notDone["s1"] = 1 // the pre-check; the first poll after launch binds
	srv.batches = [][]Operation{{startOp(), removeOp()}}
	if err := p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The start is inside Launch, between its checks and its agent record:
	// the window a concurrent remove would take the tree in.
	<-host.entered
	if len(host.removed) != 0 || len(srv.finalReports()) != 0 {
		t.Fatal("nothing for this worker may run while its start is launching")
	}
	close(host.gate)
	p.Wait()
	if len(host.removed) != 0 {
		t.Fatal("the remove must not take the tree a start is launching into")
	}
	rs := srv.finalReports()
	if len(rs) != 2 || rs[0].id != "s1" || rs[1].id != "r1" || rs[1].outcome != Failed {
		t.Errorf("start first, then a refused remove: %+v", rs)
	}
}

// P2-2: a remove queued behind a worker.end sees the keep the failed end
// recorded, instead of racing the end's grace wait.
func TestRemoveWaitsForAnEndOnTheSameWorker(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.batches = [][]Operation{{endOp(), removeOp()}}
	step(t, p)
	rs := srv.finalReports()
	if len(rs) != 2 || rs[0].id != "e1" || rs[1].id != "r1" {
		t.Fatalf("end first, then remove: %+v", rs)
	}
	if rs[1].outcome != Failed || !strings.Contains(rs[1].reason, "kept") {
		t.Errorf("the remove must see the failed end's keep: %+v", rs[1])
	}
	if len(host.removed) != 0 {
		t.Error("the tree must survive")
	}
}

// P3: only NOT_DONE launches; a finished or unclear answer does not.
func TestWorkerStartLaunchesOnlyOnNotDone(t *testing.T) {
	for name, answer := range map[string]error{"final": ErrFinal, "unclear": context.DeadlineExceeded} {
		t.Run(name, func(t *testing.T) {
			p, srv, host := testPoller(t)
			p.server = &answerServer{fakeServer: srv, err: answer}
			srv.batches = [][]Operation{{startOp()}}
			step(t, p)
			if len(host.launches) != 0 {
				t.Errorf("a %s first answer must not launch", name)
			}
		})
	}
}

type answerServer struct {
	*fakeServer
	err error
}

func (a *answerServer) Report(context.Context, string, string, string, string) (Operation, error) {
	return Operation{}, a.err
}

func TestConfigRefusesAWorktreeListedForTwoWorkers(t *testing.T) {
	c := Config{App: "a", Label: "l", WorktreesRoot: "/w", Workers: map[string]WorkerConfig{
		"iris": {Family: "claude", Launch: []string{"x"}, Worktrees: []string{"repo/shared"}},
		"dara": {Family: "codex", Launch: []string{"x"}, Worktrees: []string{"repo/shared"}},
	}}
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "belongs to one worker") {
		t.Errorf("%v", err)
	}
}

func TestWorktreeCreateRefusesAnotherWorkersToken(t *testing.T) {
	p, srv, host := testPoller(t)
	p.cfg.Workers["dara"] = WorkerConfig{Family: "codex", Launch: []string{"x"}, Worktrees: []string{"repo/x"}}
	srv.batches = [][]Operation{{{ID: "c1", Type: OpWorktreeCreate, WorkerID: "id-iris",
		Args: map[string]any{"repo": "acme/repo", "baseBranch": "main", "branchName": "iris/x", "label": "x"}}}}
	step(t, p)
	var mine report
	for _, r := range srv.finalReports() {
		if r.id == "c1" {
			mine = r
		}
	}
	if mine.outcome != Failed || !strings.Contains(mine.reason, "another worker") {
		t.Errorf("%+v", mine)
	}
	if len(host.created) != 0 {
		t.Error("nothing created")
	}
}

// cli#968: a Step collects exactly once, even when the page comes back full.
// A full page is not proof of progress or exhaustion (the oldest in-flight
// page can refill it), so there is no same-step re-collect.
func TestStepCollectsOncePerStepEvenOnAFullPage(t *testing.T) {
	p, srv, host := testPoller(t)
	full := make([]Operation, 50)
	for i := range full {
		full[i] = Operation{ID: fmt.Sprintf("o%d", i), Type: "shell.exec", WorkerID: "id-iris"}
	}
	srv.batches = [][]Operation{full, {{ID: "next", Type: "shell.exec", WorkerID: "id-iris"}}}
	step(t, p)
	srv.mu.Lock()
	collects := srv.collects
	srv.mu.Unlock()
	if collects != 1 {
		t.Fatalf("a Step collected %d times on a full page, want exactly 1", collects)
	}
	if len(host.created) != 0 {
		t.Errorf("unexpected host work: %v", host.created)
	}
	for _, r := range srv.finalReports() {
		if r.id == "next" {
			t.Error("the second page must wait for the next Step")
		}
	}
}
