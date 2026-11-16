//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func stepSweep2056(t *testing.T, f *loopFixture2060) {
	t.Helper()
	if err := f.p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.p.Wait()
}

func TestSweepReportsOutageEpisodeAndPollerOwnedRecovery2056(t *testing.T) {
	f := newLoopFixture2060(t)
	f.p.relaunch.Ready = func(context.Context) error {
		if !f.ready {
			return ErrOrcaDown
		}
		return nil
	}
	var reports []SweepReport
	f.p.relaunch.ReportSweep = func(_ context.Context, r SweepReport) error { reports = append(reports, r); return nil }
	f.ready = false
	stepSweep2056(t, f)
	stepSweep2056(t, f)
	if len(reports) != 1 || reports[0].Failed != 1 {
		t.Fatalf("outage must announce once then leave quiet records to C1: %+v", reports)
	}
	for _, r := range reports {
		if r.Mode != "APPLY" || len(r.Lines) != 1 || r.Lines[0].Reason != "ORCA_DOWN" {
			t.Fatalf("wrong outage report: %+v", r)
		}
	}
	if len(f.launches) != 0 {
		t.Fatal("outage launched an agent")
	}
	f.ready = true
	stepSweep2056(t, f)
	stepSweep2056(t, f)
	stepSweep2056(t, f)
	applied := 0
	for _, r := range reports {
		applied += r.Applied
	}
	if len(f.launches) != 1 || applied != 1 {
		t.Fatalf("recovery must be owned and reported once: launches=%d applied=%d", len(f.launches), applied)
	}
}

// S10 validates each incoming report before returning the first row for a
// duplicate (app, host, runId). A replay need not have the original payload.
// scripts/check-s10-replay-contract.mjs probes those actual server helpers.
func TestSweepOutageReportReplaysUnknownOutcome2056(t *testing.T) {
	f := newLoopFixture2060(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.p.now = func() time.Time { return clock }
	f.p.relaunch.Ready = func(context.Context) error { return ErrOrcaDown }
	var reports []SweepReport
	var stored SweepReport
	writes := 0
	f.p.relaunch.ReportSweep = func(_ context.Context, r SweepReport) error {
		reports = append(reports, r)
		if r.StartedAt.After(r.EndedAt) || r.EndedAt.Sub(r.StartedAt) > time.Hour || r.EndedAt.After(clock.Add(2*time.Minute)) {
			t.Fatalf("invalid replay timestamps: %+v", r)
		}
		if stored.RunID == "" {
			stored = r
			writes++
			return errors.New("committed, answer lost")
		}
		if r.RunID != stored.RunID || r.AppID != stored.AppID || r.HostID != stored.HostID {
			t.Fatalf("retry changed S10 row identity: original=%+v retry=%+v", stored, r)
		}
		return nil // server returns original row, not the new counts/lines
	}
	stepSweep2056(t, f)
	original := stored
	state, err := LoadState(f.p.state.path)
	if err != nil {
		t.Fatal(err)
	}
	f.p.state = state
	if state.SweepOutage == nil || state.SweepOutage.Reported {
		t.Fatal("unknown result must stay pending on disk")
	}
	// A restarted poller retries much later, with another eligible worker.
	// The new report is independently valid; its token still selects the first row.
	clock = clock.Add(2 * time.Hour)
	worker := f.source.snapshot.Workers[0]
	worker.WorkerID = "id-second"
	f.source.snapshot.Workers = append(f.source.snapshot.Workers, worker)
	stepSweep2056(t, f)
	state, err = LoadState(f.p.state.path)
	if err != nil {
		t.Fatal(err)
	}
	f.p.state = state
	if state.SweepOutage == nil || !state.SweepOutage.Reported {
		t.Fatal("confirmed replay must persist acknowledgement")
	}
	stepSweep2056(t, f)
	if len(reports) != 2 || writes != 1 || reports[0].RunID == "" || reports[0].RunID != reports[1].RunID {
		t.Fatalf("outage must replay until confirmed, then quiet: writes=%d reports=%+v", writes, reports)
	}
	if reports[0].Failed != 1 || reports[1].Failed != 2 || len(reports[1].Lines) != 2 || !reports[1].StartedAt.Equal(clock) {
		t.Fatalf("fixture did not change retry payload: %+v", reports)
	}
	if !reflect.DeepEqual(stored, original) || len(f.launches) != 0 {
		t.Fatal("replay replaced original row or launched during outage")
	}
}

func TestSweepReportingFailureDoesNotTakeRecoveryOwnership2056(t *testing.T) {
	f := newLoopFixture2060(t)
	f.p.relaunch.ReportSweep = func(context.Context, SweepReport) error { return errors.New("report transport lost") }
	stepSweep2056(t, f)
	stepSweep2056(t, f)
	stepSweep2056(t, f)
	if len(f.launches) != 1 {
		t.Fatalf("report failure changed recovery launches: %d", len(f.launches))
	}
}

func TestSweepUnknownReadinessIsNotCalledDaemonDown2056(t *testing.T) {
	f := newLoopFixture2060(t)
	var reports []SweepReport
	f.p.relaunch.Ready = func(context.Context) error { return errors.New("systemd proof unavailable") }
	f.p.relaunch.ReportSweep = func(_ context.Context, r SweepReport) error { reports = append(reports, r); return nil }
	stepSweep2056(t, f)
	if len(reports) != 0 || f.p.state.SweepOutage != nil || len(f.launches) != 0 {
		t.Fatalf("unknown readiness forged outage or action: %+v", reports)
	}
}

func TestSweepObserveLeavesRecordingToTimer2056(t *testing.T) {
	f := newLoopFixture2060(t)
	f.source.snapshot.SweepMode = "OBSERVE"
	var reports []SweepReport
	f.p.relaunch.ReportSweep = func(_ context.Context, r SweepReport) error { reports = append(reports, r); return nil }
	stepSweep2056(t, f)
	if len(reports) != 0 || len(f.launches) != 0 || f.p.state.SweepOutage != nil {
		t.Fatalf("observe acted or forged sweep freshness: %+v", reports)
	}
}
