package hostpoller

// hadron-cli#3301 LOCAL at 8bc735a4 (Joran): relaunch is contained to the resolved Orca tree on a platform host
// (F1), worktree.remove finds a tree no assignment resolves on Orca's list (F2), a workdir assigned twice
// registers for neither worker (F5), and the guards F6 found unpinned.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostfacts"
)

// realTree3301 makes <tmp>/<repo>/<name> on disk and adds it as an Orca worktree of repoID, so EvalSymlinks
// can resolve it.
func (fx *platformFixture3237) realTree3301(repoID, repo, name string) string {
	fx.t.Helper()
	dir := filepath.Join(fx.t.TempDir(), repo, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fx.t.Fatal(err)
	}
	fx.tree(repoID, dir)
	return dir
}

func TestRelaunchTargetOnAPlatformHostIsTheResolvedOrcaTree3301(t *testing.T) {
	fx := newPlatform3237(t, iris3237())
	dir := fx.realTree3301("r1", "hadron-cli", "iris")
	step(t, fx.p)
	fx.p.mu.Lock()
	fx.p.workerRefs["id-w-iris"] = "w-iris"
	fx.p.mu.Unlock()
	snap := RelaunchSnapshot{AppID: "app", HostID: "orca1", Server: "https://h.example/"}
	row := RelaunchWorker{WorkerID: "id-w-iris", Family: "claude", Worktrees: []string{"hadron-cli/iris"}}
	if fx.p.config().WorktreesRoot != "" {
		t.Fatalf("a platform Config has no worktrees root; the pin needs it empty")
	}
	target, _, _, token, ok := fx.p.relaunchTarget(snap, row)
	want, _ := filepath.EvalSymlinks(dir)
	if !ok || target.Worktree != want || token != "hadron-cli/iris" {
		t.Fatalf("a platform host relaunches in the resolved Orca tree: ok %v, worktree %q (want %q), token %q", ok, target.Worktree, want, token)
	}
	// The tree goes away from Orca's list: no directory, no relaunch.
	fx.facts.Worktrees = nil
	step(t, fx.p)
	if _, _, _, _, ok := fx.p.relaunchTarget(snap, row); ok {
		t.Error("a token that did not resolve at this poll has no relaunch target")
	}
}

func TestContainedWorktreeUnderConfigStaysInsideTheRoot3301(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repo", "iris"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "repo", "escape")); err != nil {
		t.Fatal(err)
	}
	c := &Config{WorktreesRoot: root}
	if _, ok := containedWorktree(c, "repo/iris"); !ok {
		t.Error("a tree under the root is contained")
	}
	if _, ok := containedWorktree(c, "repo/escape"); ok {
		t.Error("a symlink out of the root is not contained")
	}
	if _, ok := containedWorktree(&Config{platform: true, paths: map[string]string{}}, "repo/iris"); ok {
		t.Error("an unresolved platform token is not contained (its WorktreePath is \"\", never the cwd)")
	}
}

// removeFixture3301 is a platform poller with Orca wired, Iris assigned hadron-cli/iris (resolved).
func removeFixture3301(t *testing.T) (*platformFixture3237, *orcaTrees3237) {
	t.Helper()
	fx, o := newOrcaCreate3237(t)
	fx.tree("r1", "/home/u/orca/workspaces/hadron-cli/iris")
	o.trees = append(o.trees, fx.facts.Worktrees...)
	step(t, fx.p)
	return fx, o
}

func (fx *platformFixture3237) remove(token string) report {
	fx.t.Helper()
	fx.srv.mu.Lock()
	fx.srv.reports = nil
	fx.srv.batches = [][]Operation{{{ID: "rm1", Type: OpWorktreeRemove, WorkerID: "id-w-iris", Args: map[string]any{"worktree": token}}}}
	fx.srv.mu.Unlock()
	step(fx.t, fx.p)
	return only(fx.t, fx.srv)
}

func (fx *platformFixture3237) removedPaths() []string {
	fx.host.mu.Lock()
	defer fx.host.mu.Unlock()
	return append([]string{}, fx.host.removed...)
}

// F2: a tree this worker created through Orca is never an assignment's, so no poll resolves it; it is found on
// Orca's list at the remove and removed with every safety check.
func TestPlatformRemoveFindsATreeTheWorkerCreated3301(t *testing.T) {
	fx, _ := removeFixture3301(t)
	if r := fx.create(nil); r.outcome != Succeeded {
		t.Fatalf("create: %+v", r)
	}
	path := "/home/u/orca/workspaces/hadron-cli/x"
	fx.host.mu.Lock()
	fx.host.trees[path] = WorktreeState{Exists: true, IsWorktree: true, Branch: "iris/x", Upstream: "origin/iris/x"}
	fx.host.mu.Unlock()
	if fx.p.config().WorktreePath("hadron-cli/x") != "" {
		t.Fatal("the pin needs the created tree unresolved by the poll")
	}
	if r := fx.remove("hadron-cli/x"); r.outcome != Succeeded {
		t.Fatalf("the worker's own created tree is removed: %+v", r)
	}
	if got := fx.removedPaths(); len(got) != 1 || got[0] != path {
		t.Fatalf("removed %v, want [%s]", got, path)
	}
	if fx.p.state.createdBy("w-iris", "hadron-cli/x") {
		t.Error("a removed tree is no longer recorded as created")
	}
}

// F2: a dirty created tree is still refused: the Orca lookup only supplies the path.
func TestPlatformRemoveKeepsTheSafetyChecks3301(t *testing.T) {
	fx, _ := removeFixture3301(t)
	if r := fx.create(nil); r.outcome != Succeeded {
		t.Fatalf("create: %+v", r)
	}
	fx.host.mu.Lock()
	fx.host.trees["/home/u/orca/workspaces/hadron-cli/x"] = WorktreeState{Exists: true, IsWorktree: true, Dirty: true, Branch: "iris/x", Upstream: "origin/iris/x"}
	fx.host.mu.Unlock()
	if r := fx.remove("hadron-cli/x"); r.outcome != Failed || !strings.Contains(r.reason, "uncommitted") {
		t.Fatalf("a dirty tree is refused: %+v", r)
	}
	if got := fx.removedPaths(); len(got) != 0 {
		t.Fatalf("nothing is removed: %v", got)
	}
}

func TestPlatformRemoveRefusals3301(t *testing.T) {
	t.Run("a tree on Orca's list the worker neither is assigned nor created", func(t *testing.T) {
		fx, o := removeFixture3301(t)
		o.trees = append(o.trees, hostfacts.Worktree{ID: "o", RepoID: "r1", Path: "/home/u/orca/workspaces/hadron-cli/other", Branch: "refs/heads/o"})
		fx.host.mu.Lock()
		fx.host.trees["/home/u/orca/workspaces/hadron-cli/other"] = WorktreeState{Exists: true, IsWorktree: true, Branch: "o", Upstream: "origin/o"}
		fx.host.mu.Unlock()
		r := fx.remove("hadron-cli/other")
		if r.outcome != Failed || !strings.Contains(r.reason, "not this worker's to remove") || !strings.Contains(r.reason, "nothing was removed") {
			t.Fatalf("refused, saying why: %+v", r)
		}
		if strings.Contains(r.reason, "nothing was launched") {
			t.Errorf("a remove's refusal is not a start's: %q", r.reason)
		}
		if got := fx.removedPaths(); len(got) != 0 {
			t.Fatalf("nothing is removed: %v", got)
		}
	})
	t.Run("a tree another worker is assigned", func(t *testing.T) {
		fx, o := newOrcaCreate3237(t)
		fx.assign = append(fx.assign, Assignment{WorkerID: "w-bob", Workdir: "hadron-cli/bob", Family: "claude"})
		fx.tree("r1", "/home/u/orca/workspaces/hadron-cli/bob")
		o.trees = append(o.trees, fx.facts.Worktrees...)
		step(t, fx.p)
		r := fx.remove("hadron-cli/bob")
		if r.outcome != Failed || !strings.Contains(r.reason, "another worker") {
			t.Fatalf("another worker's tree is refused: %+v", r)
		}
		if got := fx.removedPaths(); len(got) != 0 {
			t.Fatalf("nothing is removed: %v", got)
		}
	})
	t.Run("Orca's list cannot be read", func(t *testing.T) {
		fx, o := removeFixture3301(t)
		o.down = true
		r := fx.remove("hadron-cli/x")
		if r.outcome != Failed || !strings.Contains(r.reason, "could not be read") || !strings.Contains(r.reason, "nothing was removed") {
			t.Fatalf("an unreadable Orca refuses: %+v", r)
		}
	})
	t.Run("Orca not wired", func(t *testing.T) {
		fx, _ := removeFixture3301(t)
		fx.p.SetOrcaWorktrees(nil, nil)
		r := fx.remove("hadron-cli/x")
		if r.outcome != Failed || !strings.Contains(r.reason, hostfacts.CodeWorkdirNotOnHost) {
			t.Fatalf("without Orca an unresolved token is refused with its code: %+v", r)
		}
	})
}

// F6: an unresolved token never reaches Inspect with "" (git would inspect the poller's own directory).
func TestPlatformRemoveNeverInspectsAnEmptyPath3301(t *testing.T) {
	fx, _ := removeFixture3301(t)
	fx.p.SetOrcaWorktrees(nil, nil)
	fx.host.mu.Lock()
	fx.host.trees[""] = WorktreeState{Exists: true, IsWorktree: true, Branch: "b", Upstream: "origin/b"}
	fx.host.mu.Unlock()
	_ = fx.p.state.addCreated("w-iris", "hadron-cli/x")
	if r := fx.remove("hadron-cli/x"); r.outcome != Failed {
		t.Fatalf("an unresolved token is refused: %+v", r)
	}
	if got := fx.removedPaths(); len(got) != 0 {
		t.Fatalf("nothing is removed (an empty path is the poller's own directory): %v", got)
	}
}

// F2: a token Orca does not list is already removed: SUCCEEDED, nothing touched (a lost success re-delivered).
func TestPlatformRemoveOfATreeOrcaDoesNotListSucceeds3301(t *testing.T) {
	fx, _ := removeFixture3301(t)
	_ = fx.p.state.addCreated("w-iris", "hadron-cli/gone")
	// "" is the poller's own directory: were the not-listed case to fall through, it would be inspected there.
	fx.host.mu.Lock()
	fx.host.trees[""] = WorktreeState{Exists: true, IsWorktree: true, Branch: "b", Upstream: "origin/b"}
	fx.host.mu.Unlock()
	if r := fx.remove("hadron-cli/gone"); r.outcome != Succeeded {
		t.Fatalf("already removed: %+v", r)
	}
	if got := fx.removedPaths(); len(got) != 0 {
		t.Fatalf("nothing is removed: %v", got)
	}
	if fx.p.state.createdBy("w-iris", "hadron-cli/gone") {
		t.Error("the record of a tree that is gone is dropped")
	}
}

// F5: two assignments naming one workdir register it for neither, and a start for it names both.
func TestAWorkdirAssignedTwiceRegistersForNeither3301(t *testing.T) {
	fx := newPlatform3237(t, iris3237(), Assignment{WorkerID: "w-bob", WorkerName: "Bob", Workdir: "hadron-cli/iris", Family: "claude"})
	fx.account("work", hostfacts.Entry{Family: "claude"})
	fx.tree("r1", "/home/u/orca/workspaces/hadron-cli/iris")
	step(t, fx.p)
	for _, w := range fx.lastReg().Workers {
		if len(w.Worktrees) != 0 {
			t.Errorf("worker %s registered %v; a workdir assigned twice is no one's", w.Worker, w.Worktrees)
		}
	}
	if err := fx.p.config().CheckWorktreeAliases(fx.p.state); err != nil {
		t.Errorf("the platform Config keeps a worktree to one worker: %v", err)
	}
	r := fx.start("w-iris", "hadron-cli/iris", map[string]any{"family": "claude"})
	if r.outcome != Failed || !strings.HasPrefix(r.reason, hostfacts.CodeWorkdirAssignedTwice+":") || !strings.Contains(r.reason, "w-bob") || !strings.Contains(r.reason, "w-iris") {
		t.Fatalf("the start fails naming both workers: %+v", r)
	}
	if fx.launched() != 0 {
		t.Error("nothing is launched")
	}
}

// F6: SetFromPlatform keeps a created tree out of a worker's registered trees.
func TestAPlatformRegistrationNamesOnlyTheResolvedTrees3301(t *testing.T) {
	fx, _ := removeFixture3301(t)
	if r := fx.create(nil); r.outcome != Succeeded {
		t.Fatalf("create: %+v", r)
	}
	step(t, fx.p)
	for _, w := range fx.lastReg().Workers {
		for _, tok := range w.Worktrees {
			if tok == "hadron-cli/x" {
				t.Fatalf("a created tree has no resolved path, so it is not registered: %v", w.Worktrees)
			}
		}
	}
}

// F6: the platform's poll interval is used only inside 1..86400; null or out of range is 30.
func TestPlatformPollIntervalBounds3301(t *testing.T) {
	for in, want := range map[int]int{0: 30, 1: 1, 86400: 86400, 86401: 30, -5: 30} {
		n := in
		c := BuildPlatformConfig("app", PlatformSettings{PollIntervalSeconds: &n}, nil, PlatformFacts{})
		if c.PollIntervalSeconds != want {
			t.Errorf("interval %d: got %d, want %d", in, c.PollIntervalSeconds, want)
		}
	}
	if c := BuildPlatformConfig("app", PlatformSettings{}, nil, PlatformFacts{}); c.PollIntervalSeconds != 30 {
		t.Errorf("null interval: got %d, want 30", c.PollIntervalSeconds)
	}
}

// F6: an archived Orca worktree adds no repo.
func TestAnArchivedWorktreeAddsNoRepo3301(t *testing.T) {
	f := PlatformFacts{
		Repos:     []hostfacts.Repo{{ID: "r1", Path: "/src/live", DisplayName: "live"}, {ID: "r2", Path: "/src/old", DisplayName: "old"}},
		Worktrees: []hostfacts.Worktree{{RepoID: "r1", Path: "/w/live/a"}, {RepoID: "r2", Path: "/w/old/a", Archived: true}},
	}
	c := BuildPlatformConfig("app", PlatformSettings{}, nil, f)
	if _, ok := c.Repos["old"]; ok {
		t.Errorf("a repo whose only worktree is archived is not a repo of this host: %v", c.Repos)
	}
	if c.Repos["live"] != "/src/live" {
		t.Errorf("a live worktree's repo is: %v", c.Repos)
	}
}

// F6: worker.end's prompt env has every stored key scrubbed, not only the poller's own.
func TestAnEndPromptScrubsEveryStoredKey3301(t *testing.T) {
	fx := newPlatform3237(t, iris3237())
	fx.tree("r1", "/home/u/orca/workspaces/hadron-cli/iris")
	step(t, fx.p)
	other := "hk_other_hosts_key_3301"
	fx.p.SetScrubKeys([]string{hostKey, other})
	t.Setenv("HADRON_SCRUB_PIN_3301", other)
	c := *fx.p.config()
	ws := map[string]WorkerConfig{}
	for k, v := range c.Workers {
		ws[k] = v
	}
	w := ws["w-iris"]
	w.End, w.EndWait = []string{"/usr/bin/end-session"}, "1ms"
	ws["w-iris"] = w
	c.Workers = ws
	fx.p.live.Store(&c)
	fx.p.mu.Lock()
	fx.p.workerRefs["id-w-iris"] = "w-iris"
	fx.p.agentTrees["id-w-iris"] = "hadron-cli/iris"
	fx.p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	op := Operation{ID: "e1", Type: OpWorkerEnd, WorkerID: "id-w-iris", Args: map[string]any{"sessionId": "sess1"}}
	fx.srv.mu.Lock()
	fx.srv.notDone[op.ID] = 1
	fx.srv.mu.Unlock()
	fx.p.workerEnd(ctx, op)
	fx.host.mu.Lock()
	envs := append([][]string{}, fx.host.promptEnvs...)
	fx.host.mu.Unlock()
	if len(envs) != 1 {
		t.Fatalf("one end prompt, got %d", len(envs))
	}
	for _, kv := range envs[0] {
		if strings.Contains(kv, other) {
			t.Fatalf("another host's key reached the end prompt's env: %q", kv)
		}
	}
}

// exitedAgent3301 is an agent the poller tracks; portable (loopAgent2060 is Linux-only).
type exitedAgent3301 struct{ done chan struct{} }

func (a *exitedAgent3301) Done() <-chan struct{}     { return a.done }
func (a *exitedAgent3301) ExitDetail() string        { return "" }
func (a *exitedAgent3301) PID() int                  { return 0 }
func (a *exitedAgent3301) Terminate() (bool, string) { return true, "" }

type countingSource3301 struct{ worker atomic.Int32 }

func (s *countingSource3301) RelaunchSnapshot(context.Context, string) (RelaunchSnapshot, error) {
	return RelaunchSnapshot{}, nil
}
func (s *countingSource3301) RelaunchWorker(context.Context, string, string) (RelaunchSnapshot, error) {
	s.worker.Add(1)
	return RelaunchSnapshot{}, nil
}

// F6: an exit in a token that did not resolve never reads a stamp at "" (the poller's own directory).
func TestAnExitInAnUnresolvedTreeReadsNoStamp3301(t *testing.T) {
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(delivery.RelaunchStamp{Version: 1, WorkerID: "id-w-iris", SessionID: "s", Checkpoint: "c", RequestedAt: time.Now()})
	if err := os.WriteFile(filepath.Join(cwd, ".git", delivery.RelaunchStampFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	if !delivery.HasRelaunchStamp("") {
		t.Skip("this platform reads no stamp at \"\"; the guard is moot here")
	}
	fx := newPlatform3237(t, iris3237())
	step(t, fx.p)
	src := &countingSource3301{}
	fx.p.relaunch = &RelaunchDependencies{Source: src}
	old := &exitedAgent3301{done: make(chan struct{})}
	fx.p.mu.Lock()
	fx.p.workerRefs["id-w-iris"] = "w-iris"
	fx.p.mu.Unlock()
	fx.p.trackAgent("id-w-iris", "hadron-cli/iris", old)
	fx.p.relaunchExit(context.Background(), "id-w-iris", old)
	if n := src.worker.Load(); n != 0 {
		t.Fatalf("an unresolved tree's exit asked the server %d time(s): it read a stamp at \"\"", n)
	}
}

var _ RelaunchSource = (*countingSource3301)(nil)

// BLIND N2: a stop reads one Config: a poll that swaps in a Config where the tree no longer resolves, between the
// check that allows the stop and the scan, never sends the scan to "" (the poller's own directory).
func TestAStopUsesOneConfigForItsPath3301(t *testing.T) {
	fx := newPlatform3237(t, iris3237())
	fx.tree("r1", "/home/u/orca/workspaces/hadron-cli/iris")
	step(t, fx.p)
	fx.p.mu.Lock()
	fx.p.workerRefs["id-w-iris"] = "w-iris"
	fx.p.mu.Unlock()
	gone := BuildPlatformConfig("hrn:app:acme.com:team", fx.set, fx.assign, PlatformFacts{HostDir: fx.facts.HostDir, Repos: fx.facts.Repos})
	if gone.WorktreePath("hadron-cli/iris") != "" {
		t.Fatal("the swapped-in Config must leave the tree unresolved")
	}
	fx.p.bound = func(string) delivery.Binding {
		fx.p.live.Store(gone) // a platform poll lands mid-stop
		return delivery.Binding{WorkerID: "id-w-iris", SessionID: "sess1"}
	}
	w, _ := fx.p.config().Worker("w-iris")
	op := Operation{ID: "e1", Type: OpWorkerEnd, WorkerID: "id-w-iris", Args: map[string]any{"sessionId": "sess1"}}
	_ = fx.p.stopWorkerProcess(context.Background(), op, "w-iris", w, "sess1", nil)
	fx.host.mu.Lock()
	stopped := append([]string{}, fx.host.stopped...)
	fx.host.mu.Unlock()
	for _, d := range stopped {
		if d == "" {
			t.Fatalf("the scan went to \"\" after a mid-stop Config swap: %q", stopped)
		}
	}
	if len(stopped) != 1 || stopped[0] != "/home/u/orca/workspaces/hadron-cli/iris" {
		t.Errorf("the scan used the path the stop was allowed on: %q", stopped)
	}
}
