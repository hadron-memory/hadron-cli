//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

type countingSource2120 struct {
	source RelaunchSource
	calls  int
}

func (s *countingSource2120) RelaunchSnapshot(ctx context.Context, host string) (RelaunchSnapshot, error) {
	s.calls++
	return s.source.RelaunchSnapshot(ctx, host)
}

func TestHealthyWorkersUseOneDiscoverySnapshot2120(t *testing.T) {
	f := newLoopFixture2060(t)
	if err := f.p.state.setHostID("h1"); err != nil {
		t.Fatal(err)
	}
	template := f.p.cfg.Workers["iris"]
	f.p.cfg.Workers = map[string]WorkerConfig{}
	f.p.workerRefs = map[string]string{}
	f.source.snapshot.Workers = nil
	for i := 0; i < 100; i++ {
		ref, id, token := fmt.Sprintf("worker%d", i), fmt.Sprintf("id%d", i), fmt.Sprintf("repo/worker%d", i)
		if err := os.MkdirAll(f.p.cfg.WorktreePath(token), 0700); err != nil {
			t.Fatal(err)
		}
		w := template
		w.Worktrees = []string{token}
		f.p.cfg.Workers[ref] = w
		f.p.workerRefs[id] = ref
		f.p.agents[id] = &fakeAgent{done: make(chan struct{}), pid: 1000 + i}
		f.source.snapshot.Workers = append(f.source.snapshot.Workers, RelaunchWorker{WorkerID: id, Family: "claude", Desired: "RUNNING", Reason: "LONG_LIVED", SessionID: "sid-" + id, Worktrees: []string{token}})
	}
	f.p.relaunch.Binding = func(target hostrelaunch.Target) (hostrelaunch.Binding, error) {
		return hostrelaunch.Binding{AppID: target.AppID, WorkerID: target.WorkerID, SessionID: target.SessionID, Server: target.Server, Usable: true}, nil
	}
	source := &countingSource2120{source: f.source}
	f.p.relaunch.Source = source
	f.p.relaunchStep(context.Background())
	f.p.Wait()
	if source.calls != 1 || len(f.launches) != 0 {
		t.Fatalf("100 healthy workers: snapshot reads=%d want1, launches=%d", source.calls, len(f.launches))
	}
}

func TestRunContinuesCollectingDuringBlockedRecovery2120(t *testing.T) {
	f := newLoopFixture2060(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.p.Step(ctx); err != nil {
		t.Fatal(err)
	}
	f.p.Wait() // first absent observation
	started := make(chan struct{})
	progress := make(chan struct{})
	done := make(chan error, 1)
	f.p.orcaLaunch = func(ctx context.Context, _ OrcaLaunchSpec) (Agent, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ticks := 0
	f.p.sleep = func(ctx context.Context, _ time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		ticks++
		if ticks == 1 {
			select {
			case <-started:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		close(progress) // a second register/collect/dispatch/delivery Step returned while launch is still blocked
		<-ctx.Done()
		return ctx.Err()
	}
	go func() { done <- f.p.Run(ctx) }()
	select {
	case <-progress:
		cancel()
	case <-ctx.Done():
		<-done
		t.Fatal("Run blocked in Step instead of collecting during the recovery launch")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if f.generation != 2 {
		t.Fatalf("overlapping recovery pass opened journal: generations=%d want2", f.generation)
	}
}

func TestC5CannotBypassConcurrentC3Admission2120(t *testing.T) {
	f := newLoopFixture2060(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.p.Step(ctx); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
	srv := f.p.server.(*fakeServer)
	srv.notDone["s1"] = 1
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	f.p.orcaLaunch = func(ctx context.Context, _ OrcaLaunchSpec) (Agent, error) {
		if calls.Add(1) != 1 {
			return nil, errors.New("second launch bypassed admission")
		}
		close(started)
		select {
		case <-release:
			return &loopAgent2060{done: make(chan struct{})}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	go func() { defer close(done); f.p.workerStart(ctx, startOp()) }()
	select {
	case <-started:
	case <-ctx.Done():
		<-done
		t.Fatal("C3 did not reach launch")
	}
	f.p.relaunchStep(ctx)
	f.p.Wait()
	got, attempts := calls.Load(), len(f.journal["id-iris"].Attempts)
	close(release)
	<-done
	if got != 1 || attempts != 0 {
		t.Fatalf("C5 bypassed C3: launches=%d attempts=%d", got, attempts)
	}
}

func TestC5RequiresTheBindingLock2120(t *testing.T) {
	f := newLoopFixture2060(t)
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
	release, err := delivery.LockBinding(f.p.cfg.WorktreePath("repo/iris"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	f.p.relaunchStep(context.Background())
	f.p.Wait()
	if len(f.launches) != 0 || len(f.journal["id-iris"].Attempts) != 0 {
		t.Fatal("relaunch acted without the binding lock")
	}
	if free, err := f.p.reserveWorktreeLaunch("repo/iris"); err != nil {
		t.Fatal("failed binding lock leaked admission")
	} else {
		free()
	}
}

func TestProductionVerifyRequiresOwnUniqueLiveIdentity2120(t *testing.T) {
	pid := os.Getpid()
	handle, err := (LinuxProcessControl{}).Observe(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	a := &OrcaAgent{pid: pid, h: handle, done: make(chan struct{})}
	target := hostrelaunch.Target{}
	if err := verifyRelaunchProcess(context.Background(), a, target, hostrelaunch.Observation{Proven: true, PIDs: []int{pid}}); err != nil {
		t.Fatalf("positive control: %v", err)
	}
	for _, pids := range [][]int{{os.Getppid()}, {pid, os.Getppid()}, nil} {
		if err := verifyRelaunchProcess(context.Background(), a, target, hostrelaunch.Observation{Proven: true, PIDs: pids}); err == nil {
			t.Fatalf("accepted foreign/nonunique PID evidence %v", pids)
		}
	}
	wrong := handle.Identity()
	wrong.StartTime += "-stale"
	a.h = &fakeHandle{p: &fakeProc{}, id: wrong}
	if err := verifyRelaunchProcess(context.Background(), a, target, hostrelaunch.Observation{Proven: true, PIDs: []int{pid}}); err == nil {
		t.Fatal("accepted stale pinned identity")
	}
}
