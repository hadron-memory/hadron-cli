package hostpoller

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostfacts"
)

// hadron-cli#3237 (C3 of hadron-server#5209) at the poller: a host that runs from the platform builds its
// workers each poll from its assignments and what hostfacts finds; a start's own settings win, its account name
// before the assignment's, resolved from the accounts file at the moment of use, with no fallback; only names
// leave the machine.

type platformFixture3237 struct {
	t       *testing.T
	p       *Poller
	srv     *fakeServer
	host    *fakeHost
	mu      sync.Mutex
	set     PlatformSettings
	assign  []Assignment
	facts   PlatformFacts
	err     error
	accts   hostfacts.Accounts
	acctErr error
	execs   map[string]bool
	dirs    map[string]bool
	logs    []Event
}

func newPlatform3237(t *testing.T, assign ...Assignment) *platformFixture3237 {
	t.Helper()
	p, srv, host := testPoller(t)
	root := t.TempDir()
	fx := &platformFixture3237{t: t, p: p, srv: srv, host: host, assign: assign,
		set:   PlatformSettings{HostID: "orca1", Label: "orca1"},
		accts: hostfacts.Accounts{Path: "/home/u/.config/hadron/accounts.yaml", Present: true, Entries: map[string]hostfacts.Entry{}},
		execs: map[string]bool{}, dirs: map[string]bool{},
	}
	fx.facts = PlatformFacts{HostDir: filepath.Join(root, "hosts", "orca1"), OrcaBin: "/usr/bin/orca",
		Repos: []hostfacts.Repo{{ID: "r1", Path: "/src/hadron-cli", DisplayName: "hadron-cli"}, {ID: "r2", Path: "/src/hadron-server", DisplayName: "hadron-server"}}}
	p.emit = func(e Event) { fx.mu.Lock(); defer fx.mu.Unlock(); fx.logs = append(fx.logs, e) }
	p.SetPlatformRefresh(func(context.Context) (*Config, error) {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		if fx.err != nil {
			return nil, fx.err
		}
		return BuildPlatformConfig("hrn:app:acme.com:team", fx.set, append([]Assignment{}, fx.assign...), fx.facts), nil
	})
	p.EnableOrcaLaunch(host.orcaLaunch, AssignmentProfiles{Get: p.Current, Lookup: AccountLookup{
		Program: func(fam string) (string, *hostfacts.Missing) {
			if fam == "kimi" {
				return "", &hostfacts.Missing{Fact: "agent program (kimi)", Reason: "`claude-kimi` is not on PATH", Fix: "install `claude-kimi`"}
			}
			return "/usr/bin/" + hostfacts.DefaultProgramName(fam), nil
		},
		Load:         func() (hostfacts.Accounts, error) { return fx.accts, fx.acctErr },
		IsExecutable: func(p string) bool { return fx.execs[p] },
		IsDir:        func(p string) bool { return fx.dirs[p] },
	}})
	return fx
}

// tree adds an Orca worktree of repo (r1 hadron-cli, r2 hadron-server) at path, existing on the fake host.
func (fx *platformFixture3237) tree(repoID, path string) {
	fx.facts.Worktrees = append(fx.facts.Worktrees, hostfacts.Worktree{ID: path, RepoID: repoID, Path: path, Branch: "b"})
	fx.host.mu.Lock()
	fx.host.trees[path] = WorktreeState{Exists: true, IsWorktree: true, Branch: "b", Upstream: "origin/b"}
	fx.host.mu.Unlock()
}

func (fx *platformFixture3237) account(name string, e hostfacts.Entry) {
	fx.accts.Entries[name] = e
	fx.accts.Names = append(fx.accts.Names, name)
	if e.Program != "" {
		fx.execs[e.Program] = true
	}
	if e.ConfigDir != "" {
		fx.dirs[e.ConfigDir] = true
	}
}

func (fx *platformFixture3237) start(workerID, workdir string, args map[string]any) report {
	fx.t.Helper()
	op := Operation{ID: "s-" + workerID, Type: OpWorkerStart, WorkerID: "id-" + workerID, LaunchGrant: "hlg_grant",
		Args: map[string]any{"workerId": "id-" + workerID, "worktree": workdir, "taskId": "t1"}}
	for k, v := range args {
		op.Args[k] = v
	}
	fx.srv.mu.Lock()
	fx.srv.reports = nil
	fx.srv.notDone[op.ID] = 1
	fx.srv.batches = [][]Operation{{op}}
	fx.srv.mu.Unlock()
	step(fx.t, fx.p)
	return only(fx.t, fx.srv)
}

func (fx *platformFixture3237) lastLaunch() OrcaLaunchSpec {
	fx.t.Helper()
	fx.host.mu.Lock()
	defer fx.host.mu.Unlock()
	if len(fx.host.launches) == 0 {
		fx.t.Fatal("nothing was launched")
	}
	return fx.host.launches[len(fx.host.launches)-1]
}

func (fx *platformFixture3237) launched() int {
	fx.host.mu.Lock()
	defer fx.host.mu.Unlock()
	return len(fx.host.launches)
}

func (fx *platformFixture3237) lastReg() Registration {
	fx.srv.mu.Lock()
	defer fx.srv.mu.Unlock()
	if len(fx.srv.regs) == 0 {
		fx.t.Fatal("nothing was registered")
	}
	return fx.srv.regs[len(fx.srv.regs)-1]
}

func iris3237() Assignment {
	return Assignment{WorkerID: "w-iris", WorkerName: "Iris", Workdir: "hadron-cli/iris", Family: "claude", Model: "opus", Account: "work"}
}

func TestTwoAssignmentsRegisterWithTheirResolvedWorktrees3237(t *testing.T) {
	fx := newPlatform3237(t, iris3237(), Assignment{WorkerID: "w-otto", WorkerName: "Otto", Workdir: "hadron-server/otto", Family: "codex"})
	fx.tree("r1", "/orca/hadron-cli/iris")
	fx.tree("r2", "/orca/hadron-server/otto")
	step(t, fx.p)
	r := fx.lastReg()
	got := map[string][]string{}
	for _, w := range r.Workers {
		got[w.Worker+"/"+w.Family] = w.Worktrees
	}
	want := map[string][]string{"w-iris/claude": {"hadron-cli/iris"}, "w-otto/codex": {"hadron-server/otto"}}
	if !reflect.DeepEqual(got, want) || r.Label != "orca1" || r.PollIntervalSeconds != 30 {
		t.Fatalf("registration %+v, workers %v", r, got)
	}
	if p := fx.p.config().WorktreePath("hadron-cli/iris"); p != "/orca/hadron-cli/iris" {
		t.Fatalf("a token is Orca's resolved path, not one under a root: %q", p)
	}
}

func TestAStartNamingNothingLaunchesTheAssignmentsValues3237(t *testing.T) {
	fx := newPlatform3237(t, iris3237())
	fx.tree("r1", "/orca/hadron-cli/iris")
	fx.account("work", hostfacts.Entry{Family: "claude", Program: "/opt/bin/claude-work", ConfigDir: "/home/u/.claude-work"})
	step(t, fx.p)
	if r := fx.start("w-iris", "hadron-cli/iris", map[string]any{"family": "claude"}); r.outcome != Succeeded {
		t.Fatalf("report %+v", r)
	}
	s := fx.lastLaunch()
	if want := []string{"/opt/bin/claude-work", "--dangerously-skip-permissions", "--model", "opus", PromptArg}; !reflect.DeepEqual(s.Argv, want) {
		t.Fatalf("argv %q", s.Argv)
	}
	if s.Dir != "/orca/hadron-cli/iris" {
		t.Fatalf("dir %q", s.Dir)
	}
	if !contains(s.Env, "CLAUDE_CONFIG_DIR=/home/u/.claude-work") {
		t.Fatalf("a claude account's configDir is exported as CLAUDE_CONFIG_DIR: %q", s.Env)
	}
	for _, kv := range s.Env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			t.Fatalf("a claude account sets no CODEX_HOME: %q", s.Env)
		}
	}
}

func TestTheStartsModelWinsOverTheAssignments3237(t *testing.T) {
	fx := newPlatform3237(t, iris3237())
	fx.tree("r1", "/orca/hadron-cli/iris")
	fx.account("work", hostfacts.Entry{Family: "claude"})
	step(t, fx.p)
	fx.start("w-iris", "hadron-cli/iris", map[string]any{"family": "claude", "model": "sonnet"})
	if s := fx.lastLaunch(); !reflect.DeepEqual(s.Argv, []string{"/usr/bin/claude", "--dangerously-skip-permissions", "--model", "sonnet", PromptArg}) {
		t.Fatalf("the start's model B launches: %q", s.Argv)
	}
}

func TestAccountOrderTheStartsNameThenTheAssignmentsNoFallback3237(t *testing.T) {
	// One fixture per start: a launched agent keeps its worktree, so a second start there is refused.
	fresh := func() *platformFixture3237 {
		fx := newPlatform3237(t, iris3237())
		fx.tree("r1", "/orca/hadron-cli/iris")
		fx.account("work", hostfacts.Entry{Family: "claude", Program: "/opt/bin/claude-work"})
		fx.account("review", hostfacts.Entry{Family: "claude", Program: "/opt/bin/claude-review"})
		step(t, fx.p)
		return fx
	}
	fx := fresh()
	fx.start("w-iris", "hadron-cli/iris", map[string]any{"family": "claude", "accountProfile": "review"})
	if s := fx.lastLaunch(); s.Argv[0] != "/opt/bin/claude-review" {
		t.Fatalf("a start naming account X launches X: %q", s.Argv)
	}
	fx = fresh()
	fx.start("w-iris", "hadron-cli/iris", map[string]any{"family": "claude"})
	if s := fx.lastLaunch(); s.Argv[0] != "/opt/bin/claude-work" {
		t.Fatalf("a start naming none launches the assignment's account Y: %q", s.Argv)
	}
	fx = fresh()
	before := fx.launched()
	r := fx.start("w-iris", "hadron-cli/iris", map[string]any{"family": "claude", "accountProfile": "nope"})
	if r.outcome != Failed || !strings.HasPrefix(r.reason, hostfacts.CodeAccountNotOnHost+":") || !strings.Contains(r.reason, `"nope"`) || !strings.Contains(r.reason, "family claude") {
		t.Fatalf("an unknown start account fails naming the family and the account: %+v", r)
	}
	if fx.launched() != before {
		t.Fatal("an unknown start account fell back to the assignment's and launched")
	}
}

func TestEveryAccountRefusalIsCodedAndLaunchesNothing3237(t *testing.T) {
	for name, tc := range map[string]struct {
		assign Assignment
		setup  func(fx *platformFixture3237)
		code   string
		says   []string
	}{
		"unknown account": {iris3237(), func(*platformFixture3237) {}, hostfacts.CodeAccountNotOnHost, []string{"family claude", `"work"`}},
		"absent file": {iris3237(), func(fx *platformFixture3237) {
			fx.accts = hostfacts.Accounts{Path: fx.accts.Path, Entries: map[string]hostfacts.Entry{}}
		},
			hostfacts.CodeAccountNotOnHost, []string{"does not exist"}},
		"refused file": {iris3237(), func(fx *platformFixture3237) {
			fx.acctErr = &hostfacts.AccountsError{Path: fx.accts.Path, Msg: "the accounts file is a symlink"}
		}, hostfacts.CodeAccountNotOnHost, []string{"cannot be used", "hadron team host doctor"}},
		"family mismatch": {iris3237(), func(fx *platformFixture3237) { fx.account("work", hostfacts.Entry{Family: "codex"}) },
			hostfacts.CodeAccountNotOnHost, []string{"for family codex, not claude"}},
		"glm entry for a claude launch": {iris3237(), func(fx *platformFixture3237) { fx.account("work", hostfacts.Entry{Family: "glm"}) },
			hostfacts.CodeAccountNotOnHost, []string{"for family glm, not claude"}},
		"program missing": {iris3237(), func(fx *platformFixture3237) {
			fx.account("work", hostfacts.Entry{Family: "claude", Program: "/opt/bin/gone"})
			delete(fx.execs, "/opt/bin/gone")
		}, hostfacts.CodeProgramNotOnHost, []string{"not an executable"}},
		"configDir not a directory": {iris3237(), func(fx *platformFixture3237) {
			fx.account("work", hostfacts.Entry{Family: "claude", ConfigDir: "/home/u/.claude-gone"})
			delete(fx.dirs, "/home/u/.claude-gone")
		}, hostfacts.CodeAccountNotOnHost, []string{"configDir is not a directory"}},
		"no account, no family program": {Assignment{WorkerID: "w-iris", Workdir: "hadron-cli/iris", Family: "kimi"}, func(*platformFixture3237) {},
			hostfacts.CodeProgramNotOnHost, []string{"family kimi", "claude-kimi"}},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newPlatform3237(t, tc.assign)
			fx.tree("r1", "/orca/hadron-cli/iris")
			tc.setup(fx)
			step(t, fx.p)
			r := fx.start("w-iris", "hadron-cli/iris", map[string]any{"family": tc.assign.Family})
			if r.outcome != Failed || !strings.HasPrefix(r.reason, tc.code+":") {
				t.Fatalf("want %s: %+v", tc.code, r)
			}
			for _, s := range tc.says {
				if !strings.Contains(r.reason, s) {
					t.Errorf("the reason must say %q: %s", s, r.reason)
				}
			}
			if fx.launched() != 0 {
				t.Fatal("a refused account launched")
			}
			for _, local := range []string{"/opt/bin", "/home/u", "accounts.yaml\"", fx.accts.Path} {
				if strings.Contains(r.reason, local) {
					t.Errorf("the reported reason carries a local path (%q): %s", local, r.reason)
				}
			}
		})
	}
}

func TestACodexAccountsConfigDirIsCodexHome3237(t *testing.T) {
	fx := newPlatform3237(t, Assignment{WorkerID: "w-otto", Workdir: "hadron-server/otto", Family: "codex", Account: "c2"})
	fx.tree("r2", "/orca/hadron-server/otto")
	fx.account("c2", hostfacts.Entry{Family: "codex", ConfigDir: "/home/u/.codex-two"})
	step(t, fx.p)
	fx.start("w-otto", "hadron-server/otto", map[string]any{"family": "codex"})
	s := fx.lastLaunch()
	if !contains(s.Env, "CODEX_HOME=/home/u/.codex-two") || s.Argv[0] != "/usr/bin/codex" {
		t.Fatalf("env %q argv %q", s.Env, s.Argv)
	}
	for _, kv := range s.Env {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			t.Fatalf("a codex account sets no CLAUDE_CONFIG_DIR: %q", s.Env)
		}
	}
}

func TestNoAccountLaunchesTheFamilyProgramWithNoAccountEnv3237(t *testing.T) {
	a := iris3237()
	a.Account = ""
	fx := newPlatform3237(t, a)
	fx.tree("r1", "/orca/hadron-cli/iris")
	fx.accts = hostfacts.Accounts{Path: fx.accts.Path, Entries: map[string]hostfacts.Entry{}} // first run: no file needed
	step(t, fx.p)
	fx.start("w-iris", "hadron-cli/iris", map[string]any{"family": "claude"})
	s := fx.lastLaunch()
	if s.Argv[0] != "/usr/bin/claude" {
		t.Fatalf("argv %q", s.Argv)
	}
	for _, kv := range s.Env {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") || strings.HasPrefix(kv, "CODEX_HOME=") {
			t.Fatalf("no account, no account env: %q", s.Env)
		}
	}
}

func TestAnAmbiguousOrMissingWorkdirIsNotRegisteredAndFailsTheStart3237(t *testing.T) {
	fx := newPlatform3237(t, iris3237())
	fx.account("work", hostfacts.Entry{Family: "claude"})
	fx.tree("r1", "/orca/a/iris")
	fx.tree("r1", "/orca/b/iris")
	step(t, fx.p)
	if w := fx.lastReg().Workers; len(w) != 1 || len(w[0].Worktrees) != 0 {
		t.Fatalf("an ambiguous workdir is not registered: %+v", w)
	}
	r := fx.start("w-iris", "hadron-cli/iris", map[string]any{"family": "claude"})
	if r.outcome != Failed || !strings.HasPrefix(r.reason, hostfacts.CodeWorkdirAmbiguousOnHost+":") ||
		!strings.Contains(r.reason, "/orca/a/iris") || !strings.Contains(r.reason, "/orca/b/iris") {
		t.Fatalf("the ambiguity names both candidates: %+v", r)
	}
	if fx.launched() != 0 {
		t.Fatal("an ambiguous workdir launched (it picked one)")
	}
	fx2 := newPlatform3237(t, iris3237())
	fx2.account("work", hostfacts.Entry{Family: "claude"})
	step(t, fx2.p)
	if r := fx2.start("w-iris", "hadron-cli/iris", map[string]any{"family": "claude"}); r.outcome != Failed || !strings.HasPrefix(r.reason, hostfacts.CodeWorkdirNotOnHost+":") {
		t.Fatalf("a workdir not on the host: %+v", r)
	}
}

func TestAnUnassignDropsTheWorkerAtTheNextRegistrationWithoutARestart3237(t *testing.T) {
	fx := newPlatform3237(t, iris3237(), Assignment{WorkerID: "w-otto", Workdir: "hadron-server/otto", Family: "codex"})
	fx.tree("r1", "/orca/hadron-cli/iris")
	fx.tree("r2", "/orca/hadron-server/otto")
	step(t, fx.p)
	fx.mu.Lock()
	fx.assign = fx.assign[:1]
	fx.mu.Unlock()
	step(t, fx.p)
	if w := fx.lastReg().Workers; len(w) != 1 || w[0].Worker != "w-iris" {
		t.Fatalf("the unassigned worker must be omitted: %+v", w)
	}
	for _, e := range fx.logs {
		if e.Kind == "config-restart-needed" {
			t.Fatalf("the platform path never asks for a restart: %+v", e)
		}
	}
	if _, ok := fx.p.removedWorkerFor(Operation{WorkerID: "id-w-otto"}); !ok {
		t.Fatal("an unassigned worker's entry is kept so its worker.end still runs")
	}
}

func TestAChangedPollIntervalIsUsedAtTheNextCycle3237(t *testing.T) {
	fx := newPlatform3237(t, iris3237())
	fx.tree("r1", "/orca/hadron-cli/iris")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var slept []time.Duration
	fx.p.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		fx.mu.Lock()
		n := 7
		fx.set.PollIntervalSeconds = &n
		fx.mu.Unlock()
		if len(slept) == 2 {
			cancel()
			return context.Canceled
		}
		return nil
	}
	_ = fx.p.Run(ctx)
	if len(slept) != 2 || slept[0] != 30*time.Second || slept[1] != 7*time.Second {
		t.Fatalf("slept %v; want 30s (null) then the changed 7s", slept)
	}
}

func TestDeliveryEnabledFollowsThePlatformRow3237(t *testing.T) {
	off := false
	c := BuildPlatformConfig("a", PlatformSettings{DeliveryEnabled: &off}, nil, PlatformFacts{})
	on := BuildPlatformConfig("a", PlatformSettings{}, nil, PlatformFacts{})
	if c.Delivery.Enabled || !on.Delivery.Enabled || !on.Delivery.AckMixed {
		t.Fatalf("deliveryEnabled false is off, null is on, ack_mixed is the product default: %+v / %+v", c.Delivery, on.Delivery)
	}
}

func TestASourceFlipToHOSTMidRunRegistersNothingAndWaits3237(t *testing.T) {
	fx := newPlatform3237(t, iris3237())
	fx.tree("r1", "/orca/hadron-cli/iris")
	step(t, fx.p)
	regs := len(fx.srv.regs)
	fx.mu.Lock()
	fx.err = ErrSetupSourceHost
	fx.mu.Unlock()
	step(t, fx.p)
	step(t, fx.p)
	if len(fx.srv.regs) != regs || fx.srv.collects != regs {
		t.Fatalf("under HOST nothing is registered or collected: %d regs, %d collects", len(fx.srv.regs), fx.srv.collects)
	}
	said := 0
	for _, e := range fx.logs {
		if e.Kind == "setup-source-host" {
			said++
			if !strings.Contains(e.Detail, "SERVER") {
				t.Errorf("the wait names SERVER: %s", e.Detail)
			}
		}
	}
	if said != 1 {
		t.Fatalf("said %d times, want once per flip", said)
	}
	fx.mu.Lock()
	fx.err = nil
	fx.mu.Unlock()
	step(t, fx.p)
	if len(fx.srv.regs) != regs+1 {
		t.Fatal("back on SERVER, the poller registers again without a restart")
	}
}

func TestAFailedPlatformReadEndsTheStepAndKeepsTheConfig3237(t *testing.T) {
	fx := newPlatform3237(t, iris3237())
	fx.tree("r1", "/orca/hadron-cli/iris")
	step(t, fx.p)
	held := fx.p.config()
	fx.mu.Lock()
	fx.err = errors.New("HOST_NOT_FOUND")
	fx.mu.Unlock()
	if err := fx.p.Step(context.Background()); err == nil || !strings.Contains(err.Error(), "HOST_NOT_FOUND") {
		t.Fatalf("err %v", err)
	}
	if fx.p.config() != held {
		t.Fatal("a failed read must not swap the configuration")
	}
}

func TestEveryStoredKeyIsScrubbedAndRedacted3237(t *testing.T) {
	other := "hak_other_host_key_0123456789"
	env := agentEnvKeys([]string{"A=1", "B=x" + hostKey, "C=y" + other + "z"}, []string{hostKey, other}, "D=4")
	if !reflect.DeepEqual(env, []string{"A=1", "D=4"}) {
		t.Fatalf("env %q", env)
	}
	p, _, _ := testPoller(t)
	p.SetScrubKeys([]string{other})
	if got := p.relaunchCause("boom " + other + " and " + hostKey); strings.Contains(got, other) || strings.Contains(got, hostKey) {
		t.Fatalf("a cause redacts every key the process holds: %q", got)
	}
}

func TestConfigProfilesIgnoreAStartsAccount3237(t *testing.T) {
	// Ruling Q3 (a): under --config the file path is unchanged; a start's accountProfile is not read.
	var _ ProfileSource = ConfigProfiles{}
	if _, ok := any(ConfigProfiles{}).(AccountResolving); ok {
		t.Fatal("ConfigProfiles must not resolve a start's account")
	}
	p, srv, host := testPoller(t)
	srv.notDone["s1"] = 1
	op := startOp()
	op.Args["accountProfile"] = "someone-else"
	srv.batches = [][]Operation{{op}}
	step(t, p)
	if r := only(t, srv); r.outcome != Succeeded || len(host.launches) != 1 || host.launches[0].Argv[0] != "claude-worker" {
		t.Fatalf("report %+v, launches %+v", r, host.launches)
	}
}
