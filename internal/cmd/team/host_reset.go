package team

import (
	"context"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// The hostServer's half of the worker.reset-context protocol (#1043): six
// typed steps over GraphQL with the host key. No remote value becomes a
// command or a path here; ids and tokens pass through as strings.

var _ hostpoller.ResetServer = (*hostServer)(nil)

func observed(p hostpoller.ProcessIdentity) *gen.ObservedProcessInput {
	return &gen.ObservedProcessInput{Pid: p.PID, StartTime: p.StartTime}
}

func resetStepFrom(f *gen.ContextResetStepFields) (hostpoller.ResetStep, error) {
	if f == nil || f.Reset == nil {
		return hostpoller.ResetStep{}, exitcode.Newf(exitcode.Error, "the server returned no context reset step")
	}
	r := f.Reset
	out := hostpoller.ResetStep{Replayed: f.Replayed, Reset: hostpoller.ContextReset{
		ID: r.Id, Epoch: r.Epoch, Phase: r.Phase, WorkerID: r.WorkerId, SessionID: r.SessionId, TaskID: r.TaskId,
		StopGranted: r.StopGrantedAt != nil,
	}}
	if f.Refused != nil {
		out.Refused = *f.Refused
	}
	if r.Reason != nil {
		out.Reset.Reason = *r.Reason
	}
	if r.StopLeaseMs != nil {
		out.Reset.StopLeaseMs = *r.StopLeaseMs
	}
	return out, nil
}

// resetStepErr types a step rejection by its code, so the handler stops on
// it without parsing messages; anything else is mapped as usual.
func resetStepErr(err error) error {
	for _, code := range hostpoller.ResetStepCodes {
		if api.HasErrorCode(err, code) {
			return &hostpoller.ResetStepError{Code: code, Err: err}
		}
	}
	return api.MapError(err)
}

func (s *hostServer) ClaimReset(ctx context.Context, resetID string, epoch int, p hostpoller.ProcessIdentity) (hostpoller.ResetStep, error) {
	resp, err := gen.ClaimContextReset(ctx, s.client, &gen.ClaimContextResetInput{AppRef: s.app, ResetId: resetID, Epoch: epoch, Process: observed(p)})
	if err != nil {
		return hostpoller.ResetStep{}, resetStepErr(err)
	}
	if resp.ClaimContextReset == nil {
		return resetStepFrom(nil)
	}
	return resetStepFrom(&resp.ClaimContextReset.ContextResetStepFields)
}

func (s *hostServer) RegisterResetRecovery(ctx context.Context, resetID string, epoch int, checkpointNode, sessionID, taskID string) (hostpoller.ResetStep, error) {
	resp, err := gen.RegisterContextResetRecovery(ctx, s.client, &gen.RegisterContextResetRecoveryInput{
		AppRef: s.app, ResetId: resetID, Epoch: epoch, CheckpointNode: checkpointNode, SessionId: sessionID, TaskId: taskID,
	})
	if err != nil {
		return hostpoller.ResetStep{}, resetStepErr(err)
	}
	if resp.RegisterContextResetRecovery == nil {
		return resetStepFrom(nil)
	}
	return resetStepFrom(&resp.RegisterContextResetRecovery.ContextResetStepFields)
}

func (s *hostServer) AuthorizeResetStop(ctx context.Context, resetID string, epoch int, p hostpoller.ProcessIdentity) (hostpoller.ResetStep, error) {
	resp, err := gen.AuthorizeContextResetStop(ctx, s.client, &gen.AuthorizeContextResetStopInput{AppRef: s.app, ResetId: resetID, Epoch: epoch, Process: observed(p)})
	if err != nil {
		return hostpoller.ResetStep{}, resetStepErr(err)
	}
	if resp.AuthorizeContextResetStop == nil {
		return resetStepFrom(nil)
	}
	return resetStepFrom(&resp.AuthorizeContextResetStop.ContextResetStepFields)
}

func (s *hostServer) ReportResetAgentStopped(ctx context.Context, resetID string, epoch int) (hostpoller.ResetStep, error) {
	resp, err := gen.ReportContextResetAgentStopped(ctx, s.client, &gen.ContextResetStepInput{AppRef: s.app, ResetId: resetID, Epoch: epoch})
	if err != nil {
		return hostpoller.ResetStep{}, resetStepErr(err)
	}
	if resp.ReportContextResetAgentStopped == nil {
		return resetStepFrom(nil)
	}
	return resetStepFrom(&resp.ReportContextResetAgentStopped.ContextResetStepFields)
}

func (s *hostServer) ReportResetRelaunched(ctx context.Context, resetID string, epoch int, p hostpoller.ProcessIdentity) (hostpoller.ResetStep, error) {
	resp, err := gen.ReportContextResetRelaunched(ctx, s.client, &gen.ReportContextResetRelaunchedInput{AppRef: s.app, ResetId: resetID, Epoch: epoch, Process: observed(p)})
	if err != nil {
		return hostpoller.ResetStep{}, resetStepErr(err)
	}
	if resp.ReportContextResetRelaunched == nil {
		return resetStepFrom(nil)
	}
	return resetStepFrom(&resp.ReportContextResetRelaunched.ContextResetStepFields)
}

func (s *hostServer) ReportResetRelaunchFailed(ctx context.Context, resetID string, epoch int, reason string) (hostpoller.ResetStep, error) {
	resp, err := gen.ReportContextResetRelaunchFailed(ctx, s.client, &gen.ReportContextResetRelaunchFailedInput{AppRef: s.app, ResetId: resetID, Epoch: epoch, Reason: reason})
	if err != nil {
		return hostpoller.ResetStep{}, resetStepErr(err)
	}
	if resp.ReportContextResetRelaunchFailed == nil {
		return resetStepFrom(nil)
	}
	return resetStepFrom(&resp.ReportContextResetRelaunchFailed.ContextResetStepFields)
}
