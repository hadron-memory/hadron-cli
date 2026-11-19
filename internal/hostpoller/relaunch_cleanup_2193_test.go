//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

func TestOnlyVerifiedRelaunchCleansOldTabs2193(t *testing.T) {
	f := newLoopFixture2060(t)
	calls, notices := 0, 0
	f.p.relaunch.Notice = func(_ context.Context, _ hostrelaunch.Target, _ hostrelaunch.Result) error {
		notices++
		if release, err := f.p.reserveWorktreeLaunch("repo/iris"); err != nil {
			t.Error("notice retained launch admission")
		} else {
			release()
		}
		return nil
	}
	f.p.relaunch.Cleanup = func(_ context.Context, a Agent, target hostrelaunch.Target) {
		if notices != 1 {
			t.Error("cleanup preceded relaunch notice")
		}
		calls++
		if release, err := f.p.reserveWorktreeLaunch("repo/iris"); err == nil {
			release()
			t.Error("cleanup ran after launch admission was released")
		}
		if a != f.agent || target.WorkerID != "id-iris" {
			t.Error("cleanup not linked to own launch")
		}
	}
	for i := 0; i < 2; i++ {
		if err := f.p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.p.Wait()
		if calls != i {
			t.Fatalf("cleanup calls=%d after pass %d, want %d", calls, i, i)
		}
	}
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
	if calls != 1 {
		t.Fatal("cleanup repeated for healthy worker")
	}
}

func TestFailedRelaunchDoesNotCleanTabs2193(t *testing.T) {
	f := newLoopFixture2060(t)
	calls := 0
	f.p.relaunch.Cleanup = func(context.Context, Agent, hostrelaunch.Target) { calls++ }
	f.p.relaunch.Verify = func(context.Context, Agent, hostrelaunch.Target, hostrelaunch.Observation) error {
		return errors.New("unproven launched identity")
	}
	for i := 0; i < 2; i++ {
		if err := f.p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.p.Wait()
	}
	if len(f.launches) != 1 || f.agent.stops != 1 || calls != 0 {
		t.Fatalf("launches=%d rollback=%d cleanup=%d", len(f.launches), f.agent.stops, calls)
	}
}

func TestStalledTabCleanupDoesNotBlockNextCollection2193(t *testing.T) {
	f := newLoopFixture2060(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.p.Step(ctx); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
	entered := make(chan struct{})
	f.p.relaunch.Cleanup = func(ctx context.Context, _ Agent, _ hostrelaunch.Target) { close(entered); <-ctx.Done() }
	if err := f.p.Step(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		f.p.Wait()
		t.Fatal("cleanup was never reached")
	}
	next := make(chan error, 1)
	go func() { next <- f.p.Step(ctx) }()
	select {
	case err := <-next:
		if err != nil {
			t.Error(err)
		}
	case <-ctx.Done():
		t.Error("next collection blocked on tab cleanup")
	}
	cancel()
	f.p.Wait()
}

func TestProductionCleanupProofClosure2193(t *testing.T) {
	for _, tc := range []struct {
		name                string
		pids                []int
		proven              bool
		probeErr, verifyErr error
		wantClose           bool
	}{
		{name: "only pinned PID", pids: []int{77}, proven: true, wantClose: true},
		{name: "another PID", pids: []int{78}, proven: true},
		{name: "extra agent", pids: []int{77, 78}, proven: true},
		{name: "nested match", pids: []int{77, 79}, proven: true},
		{name: "probe error invalidates positive evidence", pids: []int{77}, proven: true, probeErr: errors.New("unreadable")},
		{name: "unproven", pids: []int{77}},
		{name: "pinned process exited", pids: []int{77}, proven: true, verifyErr: errors.New("exited")},
		{name: "pinned identity changed", pids: []int{77}, proven: true, verifyErr: errors.New("changed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, _ := testPoller(t)
			closes, probes, verifies := 0, 0, 0
			a := &OrcaAgent{pid: 77, handle: "new", done: make(chan struct{}), l: OrcaLauncher{Run: func(_ context.Context, argv []string) (int, string, string) {
				if argv[1] == "close" {
					closes++
					return 0, `{"ok":true,"result":{}}`, ""
				}
				return 0, `{"ok":true,"result":{"terminals":[{"handle":"new","tabId":"new-tab","worktreePath":"/work","agentIdentity":"claude"},{"handle":"old","tabId":"old-tab","worktreePath":"/work","agentIdentity":null,"connected":false}]}}`, ""
			}}}
			target := hostrelaunch.Target{Worktree: "/work"}
			p.cleanupRelaunchTerminalsWith(context.Background(), a, target, func(ctx context.Context, got hostrelaunch.Target) (hostrelaunch.Observation, error) {
				probes++
				if got != target {
					t.Fatal("wrong census target")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 30*time.Second || time.Until(deadline) < 29*time.Second {
					t.Error("cleanup deadline not 30 seconds")
				}
				return hostrelaunch.Observation{Proven: tc.proven, PIDs: tc.pids}, tc.probeErr
			}, func(_ context.Context, got Agent, tgt hostrelaunch.Target, proof hostrelaunch.Observation) error {
				verifies++
				if got != a || tgt != target {
					t.Fatal("verify lost pinned agent or target")
				}
				if len(proof.PIDs) != 1 || proof.PIDs[0] != a.PID() {
					return errors.New("not sole pinned process")
				}
				return tc.verifyErr
			})
			want := 0
			if tc.wantClose {
				want = 1
			}
			if closes != want || probes != 1 {
				t.Fatalf("closes=%d want %d probes=%d verifies=%d", closes, want, probes, verifies)
			}
			if tc.probeErr == nil && tc.proven && verifies != 1 {
				t.Fatal("production proof omitted verification")
			}
			if (tc.probeErr != nil || !tc.proven) && verifies != 0 {
				t.Fatal("invalid census reached verification")
			}
		})
	}
}

func TestCleanupPanicCannotSuppressRelaunchNotice2193(t *testing.T) {
	f := newLoopFixture2060(t)
	notices, panics := 0, 0
	f.p.relaunch.Notice = func(context.Context, hostrelaunch.Target, hostrelaunch.Result) error { notices++; return nil }
	f.p.emit = func(e Event) {
		if e.Reason == "CLEANUP_PANIC" {
			panics++
		}
	}
	f.p.relaunch.Cleanup = func(context.Context, Agent, hostrelaunch.Target) {
		if notices != 1 {
			t.Error("notice did not run before cleanup panic")
		}
		panic("private panic text")
	}
	for i := 0; i < 2; i++ {
		if err := f.p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.p.Wait()
	}
	if notices != 1 || panics != 1 || len(f.launches) != 1 || f.agent.stops != 0 {
		t.Fatalf("notices=%d panics=%d launches=%d rollback=%d", notices, panics, len(f.launches), f.agent.stops)
	}
}

func TestCleanupRechecksAfterNotice2193(t *testing.T) {
	for _, which := range []string{"binding", "desired", "admission"} {
		t.Run(which, func(t *testing.T) {
			f := newLoopFixture2060(t)
			calls := 0
			f.p.relaunch.Cleanup = func(context.Context, Agent, hostrelaunch.Target) { calls++ }
			var held func()
			f.p.relaunch.Notice = func(context.Context, hostrelaunch.Target, hostrelaunch.Result) error {
				switch which {
				case "binding":
					f.p.relaunch.Binding = func(hostrelaunch.Target) (hostrelaunch.Binding, error) {
						return hostrelaunch.Binding{}, errors.New("changed")
					}
				case "desired":
					f.source.snapshot.Workers[0].Desired = "STOPPED"
				case "admission":
					var err error
					held, err = f.p.reserveWorktreeLaunch("repo/iris")
					if err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			for i := 0; i < 2; i++ {
				if err := f.p.Step(context.Background()); err != nil {
					t.Fatal(err)
				}
				f.p.Wait()
			}
			if held != nil {
				held()
			}
			if calls != 0 {
				t.Fatal("cleanup acted on pre-notice authority")
			}
		})
	}
}

func TestDefaultCleanupRequiresOptIn2193(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default off", true: "explicit opt-in"}[enabled], func(t *testing.T) {
			f := newLoopFixture2060(t)
			f.p.startCfg.OrcaLaunch.CleanupDeadTabs = enabled
			deps := *f.p.relaunch
			deps.Cleanup = nil
			deps.Verify = func(_ context.Context, a Agent, _ hostrelaunch.Target, o hostrelaunch.Observation) error {
				if a.PID() != 77 || len(o.PIDs) != 1 || o.PIDs[0] != 77 {
					return errors.New("wrong launch")
				}
				return nil
			}
			if err := f.p.EnableRelaunch(deps); err != nil {
				t.Fatal(err)
			}
			lists := 0
			f.p.orcaLaunch = func(_ context.Context, s OrcaLaunchSpec) (Agent, error) {
				f.launches = append(f.launches, s)
				f.agent = &loopAgent2060{done: make(chan struct{})}
				return &OrcaAgent{pid: 77, handle: "new", done: f.agent.done, l: OrcaLauncher{Run: func(context.Context, []string) (int, string, string) { lists++; return 1, "", "unavailable" }}}, nil
			}
			for i := 0; i < 2; i++ {
				if err := f.p.Step(context.Background()); err != nil {
					t.Fatal(err)
				}
				f.p.Wait()
			}
			want := 0
			if enabled {
				want = 1
			}
			if len(f.launches) != 1 || lists != want {
				t.Fatalf("launches=%d cleanup lists=%d want %d", len(f.launches), lists, want)
			}
		})
	}
}
