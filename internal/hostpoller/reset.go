package hostpoller

import (
	"context"
	"errors"
)

// OpWorkerResetContext is the capability-gated operation (#2162,
// cor:agt:050:27): reset a worker's context after its task, keeping the
// binding. Args {workerId, sessionId, taskId, resetId}; no epoch, the claim
// is made at 0 and moves it (hadron-cli#1043).
const OpWorkerResetContext = "worker.reset-context"

// CapabilityResetContext is the registration capability for that operation.
// Declaring it is a separate, authorized activation; this package never adds
// it to a registration by itself.
const CapabilityResetContext = OpWorkerResetContext

// ProcessIdentity names one agent process: its pid and the kernel's start
// time for it, an opaque token, so a reused pid is a different process.
type ProcessIdentity struct {
	PID       int
	StartTime string
}

// ContextReset is the part of the server's reset record the handler reads.
type ContextReset struct {
	ID          string
	Epoch       int
	Phase       string
	Reason      string
	WorkerID    string
	SessionID   string
	TaskID      string
	StopGranted bool
	StopLeaseMs int
}

// ResetStep is one protocol step's answer. Refused is non-empty when the
// step refused or failed the reset (it has then ended server-side);
// Replayed means the step was already made and nothing changed.
type ResetStep struct {
	Refused  string
	Replayed bool
	Reset    ContextReset
}

// ResetServer is the reset protocol's six steps, made with the host key.
// Each names the reset and the epoch the host holds.
type ResetServer interface {
	ClaimReset(ctx context.Context, resetID string, epoch int, p ProcessIdentity) (ResetStep, error)
	RegisterResetRecovery(ctx context.Context, resetID string, epoch int, checkpointNode, sessionID, taskID string) (ResetStep, error)
	AuthorizeResetStop(ctx context.Context, resetID string, epoch int, p ProcessIdentity) (ResetStep, error)
	ReportResetAgentStopped(ctx context.Context, resetID string, epoch int) (ResetStep, error)
	ReportResetRelaunched(ctx context.Context, resetID string, epoch int, p ProcessIdentity) (ResetStep, error)
	ReportResetRelaunchFailed(ctx context.Context, resetID string, epoch int, reason string) (ResetStep, error)
}

// ResetStepError is a step the server rejected outright with a typed code
// (STALE_EPOCH, CONTEXT_RESET_WRONG_PHASE, CONTEXT_RESET_NOT_FOUND,
// HOST_OPERATION_NOT_COLLECTED). Every one of them stops the handler: none
// is retried and none is a reason to touch the process.
type ResetStepError struct {
	Code string
	Err  error
}

func (e *ResetStepError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *ResetStepError) Unwrap() error { return e.Err }

// ResetStepCode returns the typed code of a step rejection, or "".
func ResetStepCode(err error) string {
	var se *ResetStepError
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

// ResetStepCodes are the typed rejections a step can carry.
var ResetStepCodes = []string{"STALE_EPOCH", "CONTEXT_RESET_WRONG_PHASE", "CONTEXT_RESET_NOT_FOUND", "HOST_OPERATION_NOT_COLLECTED"}
