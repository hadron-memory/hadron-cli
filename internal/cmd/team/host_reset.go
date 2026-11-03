package team

import (
	"context"
	"time"

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
	out := hostpoller.ResetStep{Replayed: f.Replayed, Reset: resetFrom(&f.Reset.ContextResetFields)}
	if f.Refused != nil {
		out.Refused = *f.Refused
	}
	return out, nil
}

func resetFrom(r *gen.ContextResetFields) hostpoller.ContextReset {
	out := hostpoller.ContextReset{
		ID: r.Id, Epoch: r.Epoch, Phase: r.Phase, WorkerID: r.WorkerId, SessionID: r.SessionId, TaskID: r.TaskId,
		StopGranted: r.StopGrantedAt != nil,
	}
	if r.Reason != nil {
		out.Reason = *r.Reason
	}
	if r.StopLeaseMs != nil {
		out.StopLeaseMs = *r.StopLeaseMs
	}
	if r.CheckpointNode != nil {
		out.CheckpointNode = *r.CheckpointNode
	}
	return out
}

// ReadReset is the #3273 restart read: the current state of one reset this
// host collected, ended ones included. It changes nothing server-side; a
// refusal is typed like a step's (CONTEXT_RESET_NOT_FOUND,
// HOST_OPERATION_NOT_COLLECTED).
func (s *hostServer) ReadReset(ctx context.Context, resetID string) (hostpoller.ContextReset, error) {
	resp, err := gen.ContextReset(ctx, s.client, s.app, resetID)
	if err != nil {
		return hostpoller.ContextReset{}, resetStepErr(err)
	}
	if resp.ContextReset == nil || resp.ContextReset.Id == "" {
		return hostpoller.ContextReset{}, exitcode.Newf(exitcode.Error, "the server returned no context reset")
	}
	return resetFrom(&resp.ContextReset.ContextResetFields), nil
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

func (s *hostServer) RegisterResetRecovery(ctx context.Context, resetID string, epoch int, cp hostpoller.ResetCheckpoint, sessionID, taskID string) (hostpoller.ResetStep, error) {
	resp, err := gen.RegisterContextResetRecovery(ctx, s.client, &gen.RegisterContextResetRecoveryInput{
		AppRef: s.app, ResetId: resetID, Epoch: epoch, SessionId: sessionID, TaskId: taskID,
		Checkpoint: &gen.ContextResetCheckpointInput{Title: cp.Title, Content: cp.Content},
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

func (s *hostServer) ReportResetAgentStopped(ctx context.Context, resetID string, epoch int, late time.Duration) (hostpoller.ResetStep, error) {
	resp, err := gen.ReportContextResetAgentStopped(ctx, s.client, &gen.ContextResetStepInput{AppRef: s.app, ResetId: resetID, Epoch: epoch, LateStopMs: lateStopMs(late)})
	if err != nil {
		return hostpoller.ResetStep{}, resetStepErr(err)
	}
	if resp.ReportContextResetAgentStopped == nil {
		return resetStepFrom(nil)
	}
	return resetStepFrom(&resp.ReportContextResetAgentStopped.ContextResetStepFields)
}

// lateStopMs is a late stop as the server's lateStopMs (#3255): nil when on
// time, so no late-stop event; otherwise whole milliseconds rounded UP, so a
// late stop is never reported as on time, and clamped to the 86400000 bound
// the server accepts.
func lateStopMs(late time.Duration) *int {
	if late <= 0 {
		return nil
	}
	ms := min((late+time.Millisecond-1)/time.Millisecond, maxLateStopMs)
	n := int(ms)
	return &n
}

const maxLateStopMs = 86400000

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
