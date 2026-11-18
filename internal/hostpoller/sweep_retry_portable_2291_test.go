package hostpoller

// cli#2291 — a portable companion to TestSweepS10UnavailableKeepsTokenPendingAndRetries2153.
//
// That pin runs through newLoopFixture2060, which is //go:build linux (it needs
// the typed Orca launcher and Linux process proofs), so the S10_UNAVAILABLE retry
// ran nowhere else. The retry itself does not need any of that: it is the
// outage branch of relaunchPass, which answers before any launch, process proof
// or journal record is touched. This file has no build constraint and uses no
// Linux-only helper, so it compiles and runs for every release target (`make
// cross-test-build` compiles it for Windows and Darwin). The Linux pin and its
// fixture are unchanged and keep covering the wired path through Step.
//
// The binding constraint (cor:agt:070:09: a run's identity is its host plus its
// token, and reporting it again changes nothing) is asserted exactly as the
// Linux pin does: every retry of a pending outage report sends the SAME token it
// first tried, and the episode is acknowledged only once a report is recorded.

import (
	"context"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

type portableSource2291 struct{ snapshot RelaunchSnapshot }

func (s *portableSource2291) RelaunchSnapshot(context.Context, string) (RelaunchSnapshot, error) {
	return s.snapshot, nil
}

type portableJournal2291 map[string]hostrelaunch.Record

func (j portableJournal2291) Load(k string) (hostrelaunch.Record, error) { return j[k], nil }
func (j portableJournal2291) Save(k string, r hostrelaunch.Record) error { j[k] = r; return nil }

// portableOutagePoller2291 builds a poller whose relaunch dependencies are set
// directly (EnableRelaunch refuses to run off Linux, by design), with the
// daemon permanently down and a clock the test advances. The caller drives
// p.relaunchPass.
func portableOutagePoller2291(t *testing.T, report func(context.Context, SweepReport) error) (*Poller, *time.Time) {
	t.Helper()
	p, _, _ := testPoller(t)
	if err := p.state.setHostID("h1"); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }
	journal := portableJournal2291{}
	generation := uint64(0)
	p.relaunch = &RelaunchDependencies{
		Source: &portableSource2291{snapshot: RelaunchSnapshot{
			AppID: "app-id", HostID: "h1", Server: "https://private.invalid", SweepMode: "APPLY",
			Workers: []RelaunchWorker{{WorkerID: "id-iris", Family: "claude", Desired: "RUNNING", Reason: "HOLDS_TASK", SessionID: "still-open", Worktrees: []string{"repo/iris"}}},
		}},
		OpenJournal: func() (hostrelaunch.Journal, uint64, func(), error) {
			generation++
			return journal, generation, func() {}, nil
		},
		Ready:       func(context.Context) error { return ErrOrcaDown },
		ReportSweep: report,
	}
	return p, &clock
}

func TestSweepS10UnavailableKeepsTokenPendingAndRetriesPortable2291(t *testing.T) {
	var tokens []string
	p, clock := portableOutagePoller2291(t, func(_ context.Context, r SweepReport) error {
		tokens = append(tokens, r.RunID)
		if len(tokens) < 3 {
			return ErrSweepReportingUnavailable
		}
		return nil
	})
	// #2156 F9: a pending outage backs off while S10 is unavailable, so each
	// retry happens after the 30-minute ceiling has passed.
	for i := 0; i < 3; i++ {
		*clock = (*clock).Add(31 * time.Minute)
		p.relaunchPass(context.Background())
		state, err := LoadState(p.state.path)
		if err != nil {
			t.Fatal(err)
		}
		p.state = state
		if len(tokens) != i+1 || state.SweepOutage == nil || state.SweepOutage.Reported != (i == 2) {
			t.Fatalf("attempt %d: tokens=%v state=%+v", i+1, tokens, state.SweepOutage)
		}
	}
	if tokens[0] == "" || tokens[0] != tokens[1] || tokens[1] != tokens[2] {
		t.Fatalf("a retry minted a new run: the pending token must be reused: %v", tokens)
	}
	// Acknowledged: the episode is over, nothing is re-reported.
	*clock = (*clock).Add(31 * time.Minute)
	p.relaunchPass(context.Background())
	if len(tokens) != 3 {
		t.Fatalf("an acknowledged outage was reported again: %v", tokens)
	}
}
