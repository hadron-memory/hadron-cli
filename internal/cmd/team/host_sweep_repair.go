package team

import (
	"context"
	"errors"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

type sweepRecordFailure struct{ code string }

func (e sweepRecordFailure) Error() string     { return e.code }
func (e sweepRecordFailure) SweepCode() string { return e.code }

// RecordRecoverySweep consumes C5's existing owner reports. It never calls a
// launcher or posts chat; S10 owns summary creation. This server uses only the
// configured host key, with session headers explicitly removed.
func (s *hostServer) RecordRecoverySweep(ctx context.Context, r hostpoller.SweepReport) error {
	if r.Mode != "APPLY" && r.Mode != "OBSERVE" {
		return errors.New("invalid sweep report mode")
	}
	reportCtx, cancel := context.WithTimeout(api.WithoutSession(ctx), 10*time.Second)
	defer cancel()
	in := &gen.RecordHostSweepRunInput{AppRef: r.AppID, RunId: r.RunID, StartedAt: r.StartedAt.UTC().Format(time.RFC3339Nano), EndedAt: r.EndedAt.UTC().Format(time.RFC3339Nano), Mode: gen.SweepMode(r.Mode), Planned: r.Planned, Applied: r.Applied, Skipped: r.Skipped, Failed: r.Failed, Lines: []*gen.HostSweepRunLineInput{}}
	for _, l := range r.Lines {
		if len(in.Lines) == 50 {
			break
		}
		if !sweepWorkerToken.MatchString(l.Worker) {
			continue
		}
		reason := l.Reason
		if !sweepReasonCode.MatchString(reason) {
			reason = "UNKNOWN_REASON"
		}
		outcome := gen.SweepRunOutcome(l.Outcome)
		if outcome != gen.SweepRunOutcomeApplied && outcome != gen.SweepRunOutcomeFailed && outcome != gen.SweepRunOutcomeSkipped {
			return errors.New("invalid sweep report outcome")
		}
		in.Lines = append(in.Lines, &gen.HostSweepRunLineInput{Worker: l.Worker, Outcome: outcome, Reason: reason})
	}
	reply, err := gen.RecordHostSweepRun(reportCtx, s.client, in)
	if err != nil {
		if api.IsFrameworkValidationOnlyFor(err, "recordHostSweepRun") {
			return hostpoller.ErrSweepReportingUnavailable
		}
		code := api.ServerCodeOf(err)
		if !sweepReasonCode.MatchString(code) {
			code = "RECORD_FAILED"
		}
		return sweepRecordFailure{code}
	}
	if reply == nil || reply.RecordHostSweepRun == nil || reply.RecordHostSweepRun.Id == "" || reply.RecordHostSweepRun.RunId != r.RunID {
		return sweepRecordFailure{"RECORD_UNCONFIRMED"}
	}
	return nil
}
