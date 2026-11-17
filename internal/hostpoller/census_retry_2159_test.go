//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

func TestMultiWorkerCensusRetryKeepsAttemptAuthority2159(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprint("persistent=", persistent), func(t *testing.T) {
			f := newLoopFixture2060(t)
			if err := f.p.state.setHostID("h1"); err != nil {
				t.Fatal(err)
			}
			template := f.p.cfg.Workers["iris"]
			f.p.cfg.Workers = map[string]WorkerConfig{}
			f.p.workerRefs = map[string]string{}
			f.source.snapshot.Workers = nil
			agents := map[string]*loopAgent2060{}
			starts := map[string]int{}
			probes := map[string]int{}
			events := map[string]Event{}
			for i := 0; i < 4; i++ {
				ref, id, token := fmt.Sprintf("worker%d", i), fmt.Sprintf("id%d", i), fmt.Sprintf("repo/worker%d", i)
				if err := os.MkdirAll(f.p.cfg.WorktreePath(token), 0700); err != nil {
					t.Fatal(err)
				}
				w := template
				w.Worktrees = []string{token}
				f.p.cfg.Workers[ref] = w
				f.p.workerRefs[id] = ref
				f.source.snapshot.Workers = append(f.source.snapshot.Workers, RelaunchWorker{WorkerID: id, Family: "claude", Desired: "RUNNING", Reason: "LONG_LIVED", SessionID: "sid-" + id, Worktrees: []string{token}})
			}
			f.p.relaunch.Binding = func(x hostrelaunch.Target) (hostrelaunch.Binding, error) {
				return hostrelaunch.Binding{AppID: x.AppID, WorkerID: x.WorkerID, SessionID: x.SessionID, Server: x.Server, Usable: true}, nil
			}
			f.p.relaunch.Probe = func(_ context.Context, x hostrelaunch.Target) (hostrelaunch.Observation, error) {
				probes[x.WorkerID]++
				if x.WorkerID == "id0" && probes[x.WorkerID] >= 5 && agents[x.WorkerID] == nil && (persistent || probes[x.WorkerID] == 5) {
					// The final guard's fresh scan sees a sibling's transient unknown identity.
					return hostrelaunch.Observation{Proven: true}, errors.New("relaunch census identity unavailable for sibling pid")
				}
				obs := hostrelaunch.Observation{Proven: true}
				if agents[x.WorkerID] != nil {
					obs.PIDs = []int{77}
				}
				return obs, nil
			}
			f.p.orcaLaunch = func(_ context.Context, s OrcaLaunchSpec) (Agent, error) {
				for _, row := range f.source.snapshot.Workers {
					if s.Dir == f.p.cfg.WorktreePath(row.Worktrees[0]) {
						a := &loopAgent2060{done: make(chan struct{})}
						agents[row.WorkerID] = a
						starts[row.WorkerID]++
						return a, nil
					}
				}
				return nil, errors.New("unknown launch target")
			}
			f.p.relaunch.Verify = func(_ context.Context, a Agent, x hostrelaunch.Target, o hostrelaunch.Observation) error {
				if a != agents[x.WorkerID] || len(o.PIDs) != 1 || o.PIDs[0] != 77 {
					return errors.New("foreign process")
				}
				return nil
			}
			f.p.emit = func(e Event) {
				if e.Kind == "relaunch" {
					events[e.WorkerID] = e
				}
			}
			for range 2 {
				f.p.relaunchStep(context.Background())
				f.p.Wait()
			}
			for i := 0; i < 4; i++ {
				id := fmt.Sprintf("id%d", i)
				want := 1
				if persistent && i == 0 {
					want = 0
				}
				if starts[id] != want || len(f.journal[id].Attempts) != 1 {
					t.Fatalf("%s starts=%d want%d attempts=%v event=%+v", id, starts[id], want, f.journal[id].Attempts, events[id])
				}
			}
			if persistent {
				if events["id0"].Reason != "RELAUNCH_CENSUS_UNAVAILABLE" || events["id0"].Attempt != 1 || probes["id0"] != 7 {
					t.Fatalf("persistent scan not bounded/reported distinctly: probes=%d event=%+v", probes["id0"], events["id0"])
				}
			} else if events["id0"].Outcome != hostrelaunch.Applied || probes["id0"] != 7 {
				t.Fatalf("transient scan did not recover within same admission: probes=%d event=%+v", probes["id0"], events["id0"])
			}
		})
	}
}

func TestCensusRetryDiscardsPartialProofAndBoundsCalls2159(t *testing.T) {
	calls := 0
	d := RelaunchDependencies{Probe: func(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error) {
		calls++
		return hostrelaunch.Observation{Proven: true, PIDs: []int{555}}, errors.New("unavailable identity")
	}}
	out, err := postLaunchObservation(context.Background(), &d, nil, hostrelaunch.Target{})
	if err == nil || out.Proven || len(out.PIDs) != 0 || calls != 3 {
		t.Fatalf("partial proof/call bound: %+v %v calls=%d", out, err, calls)
	}
}
func TestCensusRetryUsesShortChildDeadline2159(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d := RelaunchDependencies{Probe: func(c context.Context, _ hostrelaunch.Target) (hostrelaunch.Observation, error) {
		<-c.Done()
		return hostrelaunch.Observation{}, c.Err()
	}}
	_, err := postLaunchObservation(ctx, &d, nil, hostrelaunch.Target{})
	if err == nil || ctx.Err() != nil {
		t.Fatalf("probe consumed parent deadline instead of bounded census window: %v parent=%v", err, ctx.Err())
	}
}
func TestCensusRetryReadsCompleteNewSnapshot2159(t *testing.T) {
	proc, work := t.TempDir(), t.TempDir()
	dir := censusFixture2060(t, proc, work, 500)
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	scans, authority := 0, 0
	d := RelaunchDependencies{Probe: func(ctx context.Context, _ hostrelaunch.Target) (hostrelaunch.Observation, error) {
		scans++
		found, err := strictRelaunchCensus(ctx, proc, work, func(context.Context) error { authority++; return nil })
		if scans == 1 {
			if e := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("claude\x00"), 0600); e != nil {
				t.Fatal(e)
			}
			censusFixture2060(t, proc, work, 501)
		}
		obs := hostrelaunch.Observation{Proven: err == nil}
		for _, a := range found {
			obs.PIDs = append(obs.PIDs, a.PID)
		}
		return obs, err
	}}
	out, err := postLaunchObservation(context.Background(), &d, nil, hostrelaunch.Target{})
	if err != nil || !out.Proven || len(out.PIDs) != 2 || out.PIDs[0] != 500 || out.PIDs[1] != 501 || scans != 2 || authority != 3 {
		t.Fatalf("incomplete retry: %+v %v scans=%d authority=%d", out, err, scans, authority)
	}
}

func TestCensusRetryCancellationStopsWithoutAnotherScan2159(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	d := RelaunchDependencies{Probe: func(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error) {
		calls++
		cancel()
		return hostrelaunch.Observation{Proven: true}, errors.New("unknown identity")
	}}
	out, err := postLaunchObservation(ctx, &d, nil, hostrelaunch.Target{})
	if !errors.Is(err, context.Canceled) || out.Proven || calls != 1 {
		t.Fatalf("cancellation lost or retried: %+v %v calls=%d", out, err, calls)
	}
}

func TestCensusRetryRejectsLateSuccess2159(t *testing.T) {
	d := RelaunchDependencies{Probe: func(ctx context.Context, _ hostrelaunch.Target) (hostrelaunch.Observation, error) {
		<-ctx.Done()
		return hostrelaunch.Observation{Proven: true}, nil
	}}
	out, err := postLaunchObservation(context.Background(), &d, nil, hostrelaunch.Target{})
	if !errors.Is(err, hostrelaunch.ErrCensusUnavailable) || out.Proven || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late success accepted or census deadline exposed as launch timeout: %+v %v", out, err)
	}
}

func TestSlowSuccessfulFinalCensusLaunchesOnce2159(t *testing.T) {
	f := newLoopFixture2060(t)
	raw := f.p.relaunch.Probe
	calls := 0
	f.p.relaunch.Probe = func(ctx context.Context, target hostrelaunch.Target) (hostrelaunch.Observation, error) {
		calls++
		if calls == 5 { // the final guard, after the persisted attempt claim
			if err := sleepCtx(ctx, 300*time.Millisecond); err != nil {
				return hostrelaunch.Observation{}, err
			}
		}
		return raw(ctx, target)
	}
	var result hostrelaunch.Result
	f.p.relaunch.Notice = func(_ context.Context, _ hostrelaunch.Target, r hostrelaunch.Result) error { result = r; return nil }
	for range 2 {
		if err := f.p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.p.Wait()
	}
	if len(f.launches) != 1 || len(f.journal["id-iris"].Attempts) != 1 || result.Status != hostrelaunch.Applied {
		t.Fatalf("slow complete proof refused: launches=%d attempts=%v result=%+v", len(f.launches), f.journal["id-iris"].Attempts, result)
	}
}

func TestCensusProbeTimeoutCanRetryFreshProof2159(t *testing.T) {
	calls := 0
	d := RelaunchDependencies{Probe: func(ctx context.Context, _ hostrelaunch.Target) (hostrelaunch.Observation, error) {
		calls++
		if calls == 1 {
			<-ctx.Done()
			return hostrelaunch.Observation{Proven: true, PIDs: []int{555}}, nil
		}
		return hostrelaunch.Observation{Proven: true, PIDs: []int{777}}, nil
	}}
	out, err := postLaunchObservation(context.Background(), &d, nil, hostrelaunch.Target{})
	if err != nil || !out.Proven || len(out.PIDs) != 1 || out.PIDs[0] != 777 || calls != 2 {
		t.Fatalf("expired probe was accepted or consumed the entire retry window: %+v err=%v calls=%d", out, err, calls)
	}
}
