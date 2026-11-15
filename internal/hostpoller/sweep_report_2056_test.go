//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"testing"
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
	var reports []SweepReport
	f.p.relaunch.ReportSweep = func(_ context.Context, r SweepReport) error { reports = append(reports, r); return nil }
	f.ready = false
	stepSweep2056(t, f)
	stepSweep2056(t, f)
	if len(reports) != 2 || reports[0].Failed != 1 || reports[1].Failed != 0 || reports[1].Skipped != 1 {
		t.Fatalf("outage must announce once then record quiet skips: %+v", reports)
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

func TestSweepOutageReportReplaysUnknownOutcome2056(t *testing.T) {
	f := newLoopFixture2060(t)
	f.ready = false
	var reports []SweepReport
	f.p.relaunch.ReportSweep = func(_ context.Context, r SweepReport) error {
		reports = append(reports, r)
		if len(reports) == 1 {
			return errors.New("answer lost")
		}
		return nil
	}
	stepSweep2056(t, f)
	stepSweep2056(t, f)
	stepSweep2056(t, f)
	if len(reports) != 3 || reports[0].RunID == "" || reports[0].RunID != reports[1].RunID || reports[2].RunID == reports[1].RunID || reports[2].Failed != 0 {
		t.Fatalf("outage token must replay until confirmed, then quiet: %+v", reports)
	}
}
