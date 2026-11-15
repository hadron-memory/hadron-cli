package hostpoller

import "time"

// SweepReport is code-only telemetry from the poller's existing C5 owner.
// C1 consumes it; it grants no launch authority and carries no diagnostics.
type SweepReport struct {
	AppID, HostID, RunID, Mode        string
	StartedAt, EndedAt                time.Time
	Planned, Applied, Skipped, Failed int
	Lines                             []SweepReportLine
}
type SweepReportLine struct{ Worker, Outcome, Reason string }
