package hostpoller

import (
	"errors"
	"time"
)

var ErrSweepReportingUnavailable = errors.New("S10_UNAVAILABLE")

// ErrOrcaDown means the managed daemon is absent or its terminal list is
// unreachable. Other readiness failures remain unproven, never called down.
var ErrOrcaDown = errors.New("ORCA_DOWN")

// SweepReport is code-only telemetry from the poller's existing C5 owner.
// C1 consumes it; it grants no launch authority and carries no diagnostics.
type SweepReport struct {
	AppID, HostID, RunID, Mode        string
	StartedAt, EndedAt                time.Time
	Planned, Applied, Skipped, Failed int
	Lines                             []SweepReportLine
}
type SweepReportLine struct{ Worker, Outcome, Reason string }

// The daemon episode is telemetry only. Persist its report token before the
// request so a lost response can replay the same S10 row after a restart.
type SweepOutageRecord struct {
	AppID    string `json:"appId"`
	HostID   string `json:"hostId"`
	RunID    string `json:"runId"`
	Reported bool   `json:"reported"`
}

func (s *State) sweepOutageToken(app, host string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.SweepOutage; r != nil && r.AppID == app && r.HostID == host {
		return r.RunID, r.Reported, nil
	}
	id, err := newRelaunchEpoch()
	if err != nil {
		return "", false, err
	}
	old := s.SweepOutage
	s.SweepOutage = &SweepOutageRecord{AppID: app, HostID: host, RunID: "orca-down:" + id}
	if err := s.save(); err != nil {
		s.SweepOutage = old
		return "", false, err
	}
	return s.SweepOutage.RunID, false, nil
}
func (s *State) markSweepOutage(runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.SweepOutage == nil || s.SweepOutage.RunID != runID {
		return nil
	}
	old := s.SweepOutage.Reported
	s.SweepOutage.Reported = true
	if err := s.save(); err != nil {
		s.SweepOutage.Reported = old
		return err
	}
	return nil
}
func (s *State) clearSweepOutage() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.SweepOutage
	if old == nil {
		return nil
	}
	s.SweepOutage = nil
	if err := s.save(); err != nil {
		s.SweepOutage = old
		return err
	}
	return nil
}

func (r *SweepReport) add(worker, outcome, reason string) {
	if r == nil {
		return
	}
	switch outcome {
	case "APPLIED":
		r.Applied++
	case "FAILED":
		r.Failed++
	case "PLANNED":
		r.Planned++
		outcome = "SKIPPED"
	default:
		r.Skipped++
		outcome = "SKIPPED"
	}
	if len(r.Lines) < 50 {
		r.Lines = append(r.Lines, SweepReportLine{worker, outcome, reason})
	}
}
