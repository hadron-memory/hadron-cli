package hostpoller

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostfacts"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// hadron-cli#3237 (U3 of hadron-server#5210): on a host that runs from the platform, worktree.create goes
// through `orca worktree create --repo name:<displayName>`, never `git worktree add`, and is judged on Orca's
// worktree list read afterwards.

type orcaTrees3237 struct {
	mu    sync.Mutex
	repos []hostfacts.Repo
	trees []hostfacts.Worktree
	down  bool
	// listDown makes the worktree list fail once a create has run.
	listDown bool
	calls    [][]string
	// onCreate is what Orca does for a create: by default it adds the tree on the asked branch.
	onCreate func(argv []string) orcaterm.Result
}

func (o *orcaTrees3237) Repos(context.Context) ([]hostfacts.Repo, *hostfacts.Missing) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.down {
		return nil, &hostfacts.Missing{Fact: "orca repos", Reason: "Orca did not answer"}
	}
	return append([]hostfacts.Repo{}, o.repos...), nil
}

func (o *orcaTrees3237) Worktrees(context.Context) ([]hostfacts.Worktree, *hostfacts.Missing) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.listDown && len(o.calls) > 0 {
		return nil, &hostfacts.Missing{Fact: "orca worktrees", Reason: "Orca did not answer"}
	}
	return append([]hostfacts.Worktree{}, o.trees...), nil
}

func (o *orcaTrees3237) run(_ context.Context, argv []string) orcaterm.Result {
	o.mu.Lock()
	o.calls = append(o.calls, append([]string{}, argv...))
	f := o.onCreate
	o.mu.Unlock()
	if f != nil {
		return f(argv)
	}
	return o.add(argv, flag3237(argv, "--branch"))
}

// add is Orca making the tree <workspace>/<repo>/<name> on branch.
func (o *orcaTrees3237) add(argv []string, branch string) orcaterm.Result {
	o.mu.Lock()
	defer o.mu.Unlock()
	repo := strings.TrimPrefix(flag3237(argv, "--repo"), "name:")
	for _, r := range o.repos {
		if r.DisplayName == repo {
			path := "/home/u/orca/workspaces/" + repo + "/" + flag3237(argv, "--name")
			o.trees = append(o.trees, hostfacts.Worktree{ID: path, RepoID: r.ID, Path: path, Branch: "refs/heads/" + branch})
		}
	}
	return orcaterm.Result{Stdout: `{"id":"req","ok":true,"result":{"worktree":{"path":"ignored"}}}`}
}

func flag3237(argv []string, name string) string {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == name {
			return argv[i+1]
		}
	}
	return ""
}

func newOrcaCreate3237(t *testing.T) (*platformFixture3237, *orcaTrees3237) {
	t.Helper()
	fx := newPlatform3237(t, iris3237())
	o := &orcaTrees3237{repos: []hostfacts.Repo{
		{ID: "r1", Path: "/src/hadron-cli", DisplayName: "hadron-cli"},
		{ID: "r2", Path: "/src/hadron-server", DisplayName: "hadron-server"},
	}}
	fx.p.SetOrcaWorktrees(o, o.run)
	return fx, o
}

func (fx *platformFixture3237) create(args map[string]any) report {
	fx.t.Helper()
	op := Operation{ID: "c1", Type: OpWorktreeCreate, WorkerID: "id-w-iris", Args: map[string]any{
		"repo": "hadron-memory/hadron-cli", "baseBranch": "main", "branchName": "iris/x", "label": "x"}}
	for k, v := range args {
		if v == nil {
			delete(op.Args, k)
			continue
		}
		op.Args[k] = v
	}
	fx.srv.mu.Lock()
	fx.srv.reports = nil
	fx.srv.batches = [][]Operation{{op}}
	fx.srv.mu.Unlock()
	step(fx.t, fx.p)
	return only(fx.t, fx.srv)
}

func (fx *platformFixture3237) noGit() {
	fx.t.Helper()
	fx.host.mu.Lock()
	defer fx.host.mu.Unlock()
	if len(fx.host.created) != 0 {
		fx.t.Errorf("a platform host never runs `git worktree add`: %v", fx.host.created)
	}
}

func TestPlatformWorktreeCreateGoesThroughOrca3237(t *testing.T) {
	fx, o := newOrcaCreate3237(t)
	r := fx.create(nil)
	if r.outcome != Succeeded || r.worktree != "hadron-cli/x" {
		t.Fatalf("create: %+v", r)
	}
	want := "worktree create --repo name:hadron-cli --name x --branch iris/x --base-branch main --no-parent --setup skip --json"
	if len(o.calls) != 1 || strings.Join(o.calls[0], " ") != want {
		t.Errorf("Orca was asked %q, want exactly %q", o.calls, want)
	}
	fx.noGit()
}

func TestPlatformWorktreeCreateOmitsAnEmptyBase3237(t *testing.T) {
	fx, o := newOrcaCreate3237(t)
	if r := fx.create(map[string]any{"baseBranch": nil}); r.outcome != Succeeded {
		t.Fatalf("create: %+v", r)
	}
	if len(o.calls) != 1 || strings.Contains(strings.Join(o.calls[0], " "), "--base-branch") {
		t.Errorf("no base names Orca's repo default, so --base-branch is not sent: %q", o.calls)
	}
}

func TestPlatformWorktreeCreateIsIdempotentOnRedelivery3237(t *testing.T) {
	fx, o := newOrcaCreate3237(t)
	o.trees = []hostfacts.Worktree{{ID: "t", RepoID: "r1", Path: "/home/u/orca/workspaces/hadron-cli/x", Branch: "refs/heads/iris/x"}}
	if r := fx.create(nil); r.outcome != Succeeded || r.worktree != "hadron-cli/x" {
		t.Fatalf("a re-delivered create whose tree is there on the branch succeeds: %+v", r)
	}
	if len(o.calls) != 0 {
		t.Errorf("an existing tree is not created again: %q", o.calls)
	}
}

func TestPlatformWorktreeCreateRefusals3237(t *testing.T) {
	cases := []struct {
		name  string
		setup func(o *orcaTrees3237)
		args  map[string]any
		want  string
		calls int
	}{
		{"unknown repo", nil, map[string]any{"repo": "acme/unknown"}, "`orca repo add`", 0},
		{"repo named twice", func(o *orcaTrees3237) {
			o.repos = append(o.repos, hostfacts.Repo{ID: "r3", Path: "/other/hadron-cli", DisplayName: "hadron-cli"})
		}, nil, "names 2 Orca repositories", 0},
		{"Orca down", func(o *orcaTrees3237) { o.down = true }, nil, "could not be read", 0},
		{"no branch", nil, map[string]any{"branchName": nil}, "names no branch", 0},
		{"tree there on another branch", func(o *orcaTrees3237) {
			o.trees = []hostfacts.Worktree{{ID: "t", RepoID: "r1", Path: "/w/hadron-cli/x", Branch: "refs/heads/other"}}
		}, nil, "is not a worktree on iris/x", 0},
		{"token ambiguous", func(o *orcaTrees3237) {
			o.trees = []hostfacts.Worktree{{ID: "a", RepoID: "r1", Path: "/a/x", Branch: "refs/heads/iris/x"}, {ID: "b", RepoID: "r1", Path: "/b/x", Branch: "refs/heads/iris/x"}}
		}, nil, hostfacts.CodeWorkdirAmbiguousOnHost, 0},
		{"create refused", func(o *orcaTrees3237) {
			o.onCreate = func([]string) orcaterm.Result { return orcaterm.Result{Code: 1, Stderr: "secret /home/u path"} }
		}, nil, "whether the tree exists is unknown", 1},
		{"create not ok", func(o *orcaTrees3237) {
			o.onCreate = func([]string) orcaterm.Result { return orcaterm.Result{Stdout: `{"ok":false}`} }
		}, nil, "whether the tree exists is unknown", 1},
		{"created tree not listed", func(o *orcaTrees3237) {
			o.onCreate = func([]string) orcaterm.Result { return orcaterm.Result{Stdout: `{"ok":true,"result":{}}`} }
		}, nil, "is not in its worktree list", 1},
		{"list unreadable after the create", func(o *orcaTrees3237) { o.listDown = true }, nil, "the tree may exist", 1},
		{"branch name taken, Orca suffixed it", func(o *orcaTrees3237) {
			o.onCreate = func(argv []string) orcaterm.Result { return o.add(argv, "iris/x-2") }
		}, nil, "on iris/x-2, not on iris/x", 1},
		{"another worker's token", nil, map[string]any{"label": "iris"}, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx, o := newOrcaCreate3237(t)
			if tc.name == "another worker's token" {
				// hadron-cli/iris is Iris's own assigned tree; Juno asks for it.
				fx.assign = append(fx.assign, Assignment{WorkerID: "w-juno", Workdir: "hadron-server/juno", Family: "codex"})
				fx.tree("r1", "/home/u/orca/workspaces/hadron-cli/iris")
				step(t, fx.p) // the poll resolves Iris's tree
				fx.srv.mu.Lock()
				fx.srv.reports = nil
				fx.srv.mu.Unlock()
				op := Operation{ID: "c2", Type: OpWorktreeCreate, WorkerID: "id-w-juno", Args: map[string]any{
					"repo": "hadron-cli", "baseBranch": "main", "branchName": "juno/x", "label": "iris"}}
				fx.srv.mu.Lock()
				fx.srv.batches = [][]Operation{{op}}
				fx.srv.mu.Unlock()
				step(t, fx.p)
				r := only(t, fx.srv)
				if r.outcome != Failed || !strings.Contains(r.reason, "registered for another worker") {
					t.Errorf("%+v", r)
				}
				if len(o.calls) != 0 {
					t.Errorf("Orca was asked %q", o.calls)
				}
				return
			}
			if tc.setup != nil {
				tc.setup(o)
			}
			r := fx.create(tc.args)
			if r.outcome != Failed || !strings.Contains(r.reason, tc.want) {
				t.Errorf("want FAILED naming %q, got %+v", tc.want, r)
			}
			if strings.Contains(r.reason, "/home/u") || strings.Contains(r.reason, "secret") {
				t.Errorf("a failure carries names only, never a path or Orca's text: %q", r.reason)
			}
			if r.worktree != "" {
				t.Errorf("a failed create reports no worktree: %+v", r)
			}
			if tc.calls > 0 && strings.Contains(r.reason, "nothing was created") {
				t.Errorf("once Orca was asked to create, the report must not claim nothing was: %q", r.reason)
			}
			if len(o.calls) != tc.calls {
				t.Errorf("Orca creates: %q, want %d", o.calls, tc.calls)
			}
			fx.noGit()
		})
	}
}

func TestPlatformWorktreeCreateWithoutOrcaCreatesNothing3237(t *testing.T) {
	o := &orcaTrees3237{}
	for name, wire := range map[string]func(p *Poller){
		"never wired": func(*Poller) {},
		"no runner":   func(p *Poller) { p.SetOrcaWorktrees(o, nil) },
		"no facts":    func(p *Poller) { p.SetOrcaWorktrees(nil, o.run) },
	} {
		t.Run(name, func(t *testing.T) {
			fx := newPlatform3237(t, iris3237())
			wire(fx.p)
			r := fx.create(nil)
			if r.outcome != Failed || !strings.Contains(r.reason, "Orca is not wired") {
				t.Errorf("%+v", r)
			}
			fx.noGit()
		})
	}
	if len(o.calls) != 0 {
		t.Errorf("Orca was asked %q", o.calls)
	}
}

func TestTheCreateRunnerIsClosedOverWorktreeCreate3237(t *testing.T) {
	fx, o := newOrcaCreate3237(t)
	for _, argv := range [][]string{{"worktree", "rm", "--worktree", "path:/x"}, {"terminal", "create"}, {"repo", "add"}, {"worktree"}} {
		if res := fx.p.orcaCreate(context.Background(), argv); res.Code == 0 {
			t.Errorf("%q ran through the create runner", argv)
		}
	}
	if len(o.calls) != 0 {
		t.Errorf("a refused call reached Orca: %q", o.calls)
	}
}
