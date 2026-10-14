package hostpoller

import (
	"context"
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
	done chan struct{}
}

func (a *fakeAgent) Done() <-chan struct{} { return a.done }
func (a *fakeAgent) ExitDetail() string    { return "exit status 1" }

type fakeHost struct {
	mu       sync.Mutex
	trees    map[string]WorktreeState
	created  []string
	removed  []string
	launches []LaunchSpec
	prompts  [][]string
	agent    *fakeAgent
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
	h.mu.Lock()
	defer h.mu.Unlock()
	h.launches = append(h.launches, spec)
	if h.agent == nil {
		h.agent = &fakeAgent{done: make(chan struct{})}
	}
	return h.agent, nil
}

func (h *fakeHost) Prompt(_ context.Context, argv []string, _ string, _ []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prompts = append(h.prompts, argv)
	return nil
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
				End: []string{"orca-send", "iris", "end"}, EndWait: "20ms", Worktrees: []string{"repo/iris"}},
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
	p, srv, _ := testPoller(t)
	srv.notDone["s1"] = 1 << 20
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "no session was bound") {
		t.Errorf("%+v", r)
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
