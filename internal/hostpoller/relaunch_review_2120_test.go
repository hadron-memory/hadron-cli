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

// collectServer2120 preserves the real Server interface while controlling the
// next collection failure. The recovery source is separate, as in production.
type collectServer2120 struct {
	Server
	collect func(context.Context) ([]Operation, error)
}

func (s *collectServer2120) Collect(ctx context.Context) ([]Operation, error) { return s.collect(ctx) }

func TestFatalCheckInCancelsAndJoinsRecovery2120(t *testing.T) {
	f := newLoopFixture2060(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.p.Step(ctx); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
	started, stopped := make(chan struct{}), make(chan struct{})
	f.p.orcaLaunch = func(jobCtx context.Context, _ OrcaLaunchSpec) (Agent, error) {
		close(started)
		<-jobCtx.Done()
		close(stopped)
		return nil, jobCtx.Err()
	}
	fatal := errors.New("host key no longer authorized")
	calls := 0
	base := f.p.server
	f.p.server = &collectServer2120{Server: base, collect: func(ctx context.Context) ([]Operation, error) {
		calls++
		if calls == 2 {
			return nil, fatal
		}
		return base.Collect(ctx)
	}}
	f.p.Fatal = func(err error) bool { return errors.Is(err, fatal) }
	f.p.sleep = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-started:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err := f.p.Run(ctx)
	if !errors.Is(err, fatal) || ctx.Err() != nil {
		t.Fatalf("fatal exit failed to cancel/join job before parent deadline: %v parent=%v", err, ctx.Err())
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Run returned with its recovery launch still active")
	}
}

type journalHook2120 struct {
	hostrelaunch.Journal
	afterSave func(hostrelaunch.Record)
}

func (j journalHook2120) Save(worker string, record hostrelaunch.Record) error {
	if err := j.Journal.Save(worker, record); err != nil {
		return err
	}
	j.afterSave(record)
	return nil
}

func TestCancelledRecoveryPassBreaksObservationContinuity2120(t *testing.T) {
	f := newLoopFixture2060(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	open := f.p.relaunch.OpenJournal
	f.p.relaunch.OpenJournal = func() (hostrelaunch.Journal, uint64, func(), error) {
		j, g, close, err := open()
		return journalHook2120{Journal: j, afterSave: func(r hostrelaunch.Record) {
			if r.AbsentFor != "" && len(r.Attempts) == 0 {
				cancel()
			}
		}}, g, close, err
	}
	if err := f.p.Step(ctx); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
	if f.journal["id-iris"].AbsentFor == "" {
		t.Fatal("fixture never persisted the interrupted observation")
	}
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
	if len(f.launches) != 0 || len(f.journal["id-iris"].Attempts) != 0 {
		t.Fatal("cancelled observation bridged into a new launch")
	}
}

func TestUntrackedRecognizedAgentPreventsRecovery2120(t *testing.T) {
	f := newLoopFixture2060(t)
	proc := t.TempDir()
	censusFixture2060(t, proc, f.p.cfg.WorktreePath("repo/iris"), 31337)
	f.p.relaunch.Probe = func(ctx context.Context, target hostrelaunch.Target) (hostrelaunch.Observation, error) {
		rows, err := strictRelaunchCensus(ctx, proc, target.Worktree, func(ctx context.Context) error { return ctx.Err() })
		out := hostrelaunch.Observation{Proven: err == nil, PIDs: []int{}}
		for _, row := range rows {
			out.PIDs = append(out.PIDs, row.PID)
		}
		return out, err
	}
	for i := 0; i < 3; i++ {
		if err := f.p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.p.Wait()
	}
	if len(f.p.agents) != 0 || len(f.launches) != 0 || len(f.journal["id-iris"].Attempts) != 0 {
		t.Fatal("empty tracking was treated as absence despite a recognized live census process")
	}
}

func TestRelaunchTargetRequiresFamilyRegistrationAndRoot2120(t *testing.T) {
	for _, name := range []string{"family", "registration", "root", "multiple"} {
		t.Run(name, func(t *testing.T) {
			f := newLoopFixture2060(t)
			if err := f.p.register(context.Background()); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "family":
				f.source.snapshot.Workers[0].Family = "codex"
			case "registration":
				token := "repo/not-registered"
				if err := os.MkdirAll(f.p.cfg.WorktreePath(token), 0700); err != nil {
					t.Fatal(err)
				}
				f.source.snapshot.Workers[0].Worktrees = []string{token}
			case "root":
				dir := f.p.cfg.WorktreePath("repo/iris")
				if err := os.Remove(dir); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), dir); err != nil {
					t.Fatal(err)
				}
			case "multiple":
				f.source.snapshot.Workers[0].Worktrees = []string{"repo/iris", "repo/other"}
			}
			f.p.relaunchStep(context.Background())
			f.p.Wait()
			if len(f.journal) != 0 || len(f.launches) != 0 {
				t.Fatalf("%s bypassed target derivation", name)
			}
		})
	}
}
