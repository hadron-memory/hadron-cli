//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

type loopSource2060 struct {
	snapshot RelaunchSnapshot
	err      error
}

func (s *loopSource2060) RelaunchSnapshot(context.Context, string) (RelaunchSnapshot, error) {
	return s.snapshot, s.err
}

type loopJournal2060 map[string]hostrelaunch.Record

func (j loopJournal2060) Load(k string) (hostrelaunch.Record, error) { return j[k], nil }
func (j loopJournal2060) Save(k string, r hostrelaunch.Record) error { j[k] = r; return nil }

type loopAgent2060 struct {
	done  chan struct{}
	stops int
}

func (a *loopAgent2060) Done() <-chan struct{} { return a.done }
func (a *loopAgent2060) PID() int              { return 77 }
func (a *loopAgent2060) ExitDetail() string    { return "private helper" }
func (a *loopAgent2060) Terminal() (string, int, string) {
	return "private-terminal", 77, "/dev/pts/private"
}
func (a *loopAgent2060) Terminate() (bool, string) {
	select {
	case <-a.done:
	default:
		close(a.done)
	}
	a.stops++
	return true, ""
}

type loopFixture2060 struct {
	p          *Poller
	source     *loopSource2060
	journal    loopJournal2060
	generation uint64
	agent      *loopAgent2060
	launches   []OrcaLaunchSpec
	ready      bool
	disk       string
}

func newLoopFixture2060(t *testing.T) *loopFixture2060 {
	t.Helper()
	p, _, _ := testPoller(t)
	f := &loopFixture2060{p: p, journal: loopJournal2060{}, ready: true, disk: filepath.Join(t.TempDir(), "disk")}
	if err := os.MkdirAll(p.cfg.WorktreePath("repo/iris"), 0700); err != nil {
		t.Fatal(err)
	}
	orcaWorker(p, f.disk, LauncherProfile{Family: "claude", Mode: ModeLong, Account: "a"}, AccountProfile{Bin: "claude"})
	p.EnableOrcaLaunch(func(_ context.Context, s OrcaLaunchSpec) (Agent, error) {
		f.launches = append(f.launches, s)
		f.agent = &loopAgent2060{done: make(chan struct{})}
		return f.agent, nil
	}, ConfigProfiles{Cfg: p.cfg})
	f.source = &loopSource2060{snapshot: RelaunchSnapshot{AppID: "app-id", HostID: "h1", Server: "https://private.invalid", SweepMode: "APPLY", Workers: []RelaunchWorker{{WorkerID: "id-iris", Name: "iris", Family: "claude", Desired: "RUNNING", Reason: "HOLDS_TASK", SessionID: "still-open", Worktrees: []string{"repo/iris"}}}}}
	deps := RelaunchDependencies{Source: f.source,
		OpenJournal: func() (hostrelaunch.Journal, uint64, func(), error) {
			f.generation++
			return f.journal, f.generation, func() {}, nil
		},
		Ready: func(context.Context) error {
			if !f.ready {
				return errors.New("daemon unavailable")
			}
			return nil
		},
		Binding: func(hostrelaunch.Target) (hostrelaunch.Binding, error) {
			return hostrelaunch.Binding{AppID: "app-id", WorkerID: "id-iris", SessionID: "still-open", Server: "https://private.invalid", Usable: true}, nil
		},
		Probe: func(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error) {
			o := hostrelaunch.Observation{Proven: true}
			if f.agent != nil {
				select {
				case <-f.agent.done:
				default:
					o.PIDs = []int{77}
				}
			}
			return o, nil
		},
		Verify: func(_ context.Context, a Agent, _ hostrelaunch.Target, o hostrelaunch.Observation) error {
			if a != f.agent || len(o.PIDs) != 1 || o.PIDs[0] != 77 {
				return errors.New("not own process")
			}
			return nil
		},
	}
	if err := p.EnableRelaunch(deps); err != nil {
		t.Fatal(err)
	}
	return f
}
func TestPeriodicRelaunchUsesTwoSweepsFreshPromptAndTracksOpenSession2060(t *testing.T) {
	f := newLoopFixture2060(t)
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.launches) != 0 {
		t.Fatal("first observation launched")
	}
	if _, err := os.Stat(f.disk); !os.IsNotExist(err) {
		t.Fatal("preparation wrote disk_tmp")
	}
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.launches) != 1 {
		t.Fatalf("launches=%d", len(f.launches))
	}
	s := f.launches[0]
	if !strings.Contains(s.Prompt, "session is still open") || !strings.Contains(s.Prompt, "session recover") || strings.Contains(s.Prompt, "session start") || strings.Contains(strings.Join(s.Argv, " "), "--resume") {
		t.Fatalf("unsafe prompt/argv: %+v", s)
	}
	for _, v := range s.Env {
		if strings.HasPrefix(v, "HADRON_LAUNCH_GRANT=") {
			t.Fatal("relaunch got a grant")
		}
	}
	if f.p.agents["id-iris"] != f.agent || f.p.agentSessions["id-iris"] != "still-open" {
		t.Fatal("launch lost tracking or open session")
	}
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.launches) != 1 {
		t.Fatal("live tracked agent relaunched")
	}
}
func TestPeriodicRelaunchRefusesPauseBetweenAbsenceSweeps2060(t *testing.T) {
	f := newLoopFixture2060(t)
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.source.snapshot.Workers[0].Reason = "PAUSED"
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.launches) != 0 {
		t.Fatal("paused worker launched")
	}
}
func TestDaemonUnavailableDoesNotSpendLaunchBudgetAndRecoversAfterReturn2060(t *testing.T) {
	f := newLoopFixture2060(t)
	f.ready = false
	for i := 0; i < 4; i++ {
		if err := f.p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.launches) != 0 || len(f.journal["id-iris"].Attempts) != 0 {
		t.Fatal("daemon outage consumed launch budget")
	}
	f.ready = true
	for i := 0; i < 2; i++ {
		if err := f.p.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.launches) != 1 {
		t.Fatalf("daemon return did not recover: %d", len(f.launches))
	}
}

func TestPollerRestartRequiresTwoFreshAbsenceObservations2060(t *testing.T) {
	f := newLoopFixture2060(t)
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := f.p
	clone := New(old.cfg, old.server, old.host, old.state, old.key, nil)
	clone.now = old.now
	clone.EnableOrcaLaunch(old.orcaLaunch, old.profiles)
	if err := clone.EnableRelaunch(*old.relaunch); err != nil {
		t.Fatal(err)
	}
	f.p = clone
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.launches) != 0 {
		t.Fatal("restart reused an old absence proof")
	}
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.launches) != 1 {
		t.Fatal("two fresh post-restart observations did not recover")
	}
}
func TestUnknownSourceReadBreaksAbsenceContinuity2060(t *testing.T) {
	f := newLoopFixture2060(t)
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.source.err = errors.New("source unavailable")
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.source.err = nil
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.launches) != 0 {
		t.Fatal("unknown source pass bridged absence observations")
	}
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.launches) != 1 {
		t.Fatal("fresh continuity did not recover")
	}
}

func TestDaemonReadinessRecheckedBeforeSpendingAttempt2060(t *testing.T) {
	f := newLoopFixture2060(t)
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	checks := 0
	f.p.relaunch.Ready = func(context.Context) error {
		checks++
		if checks > 1 {
			return errors.New("daemon disappeared during target proofs")
		}
		return nil
	}
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.launches) != 0 || len(f.journal["id-iris"].Attempts) != 0 {
		t.Fatalf("lost daemon spent budget: launches=%d attempts=%d", len(f.launches), len(f.journal["id-iris"].Attempts))
	}
}
