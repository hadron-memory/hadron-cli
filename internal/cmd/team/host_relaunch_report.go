package team

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// ReportRelaunch reports the action taken about a proven-dark observation.
// It never fabricates a repair for an unknown process proof or coerces an
// unsupported outcome to CAP_REACHED. The timestamp belongs to the absence
// observation, not to a new claim that the successfully relaunched agent is dark.
func (s *hostServer) ReportRelaunch(ctx context.Context, t hostrelaunch.Target, r hostrelaunch.Result) error {
	repair, reason, known := relaunchRepair(r)
	if !known {
		if r.Status == hostrelaunch.Failed || r.Attempt > 0 {
			return errors.New("repair failure has no truthful server reason; retained in local events")
		}
		return nil
	}
	if r.AbsentObservedAt.IsZero() {
		return errors.New("repair has no proven-dark observation")
	}

	if t.AppID == "" || t.HostID == "" || t.WorkerID == "" || t.SessionID == "" || strings.TrimRight(t.Server, "/") != strings.TrimRight(s.serverURL, "/") {
		return errors.New("repair target identity is unavailable")
	}
	// Like operation outcome reports, finish this bounded metadata report after
	// a launch's cancellation/timeout. It authorizes no further launch or stop.
	reportCtx, cancel := context.WithTimeout(api.WithoutSession(context.WithoutCancel(ctx)), 30*time.Second)
	defer cancel()
	fresh, err := s.RelaunchSnapshot(reportCtx, t.HostID)
	if err != nil {
		return err
	}
	if fresh.AppID != t.AppID || fresh.HostID != t.HostID || fresh.Server != strings.TrimRight(t.Server, "/") {
		return errors.New("repair target identity changed")
	}
	same := false
	for _, worker := range fresh.Workers {
		if worker.WorkerID == t.WorkerID && worker.SessionID == t.SessionID {
			same = true
		}
	}
	if !same {
		return errors.New("repair target session changed")
	}
	noAgent := gen.WorkerTerminalReasonNoAgent
	input := &gen.WorkerTerminalReportInput{Worker: t.WorkerID, Status: gen.WorkerTerminalStateDark, Prompt: gen.WorkerTerminalPromptUnknown, Reason: &noAgent, CheckedAt: r.AbsentObservedAt.UTC().Format(time.RFC3339Nano), Repair: &repair}
	if reason != "" {
		code := gen.WorkerTerminalRepairReason(reason)
		input.RepairReason = &code
	}
	if repair != gen.WorkerTerminalRepairCapReached {
		if r.Attempt < 1 {
			return errors.New("repair has no candidate attempt number")
		}
		attempt := r.Attempt
		input.Attempt = &attempt
	}
	if reason == hostrelaunch.FailureSetupFailed || reason == hostrelaunch.FailureVerifyFailed {
		return s.reportNewRelaunchFailure(reportCtx, t, r, reason)
	}
	response, err := gen.HealthSweepSetTerminals(reportCtx, s.client, &gen.SetHostWorkerTerminalsInput{AppRef: t.AppID, Reports: []*gen.WorkerTerminalReportInput{input}})
	if err != nil {
		return api.MapError(err)
	}
	if response == nil || response.SetHostWorkerTerminals == nil || len(response.SetHostWorkerTerminals.Refused) != 0 || len(response.SetHostWorkerTerminals.Stored) != 1 || response.SetHostWorkerTerminals.Stored[0] == nil || response.SetHostWorkerTerminals.Stored[0].Id != t.WorkerID {
		return errors.New("repair report was not confirmed stored")
	}
	return nil
}
func relaunchRepair(r hostrelaunch.Result) (gen.WorkerTerminalRepair, string, bool) {
	if r.Status == hostrelaunch.Applied {
		return gen.WorkerTerminalRepairRelaunched, "", true
	}
	if r.Reason == hostrelaunch.RateLimited {
		return gen.WorkerTerminalRepairCapReached, "", true
	}
	refused := ""
	switch r.Reason {
	case hostrelaunch.IdentityChanged:
		refused = "IDENTITY_MISMATCH"
	case hostrelaunch.DuplicateAgents:
		refused = "DUPLICATE_AGENT"
	case "PAUSED", "HALTED", "LEAVE_ALONE":
		refused = r.Reason
	case hostrelaunch.NoProfile:
		refused = "NO_LAUNCHER_PROFILE"
	}
	if refused != "" {
		return gen.WorkerTerminalRepairRelaunchRefused, refused, true
	}
	if r.Status == hostrelaunch.Failed {
		switch r.FailureReason {
		case hostrelaunch.FailureTimeout, hostrelaunch.FailureTerminalCreateRefused, hostrelaunch.FailureLaunchExitNonzero, hostrelaunch.FailureSetupFailed, hostrelaunch.FailureVerifyFailed:
			return gen.WorkerTerminalRepairRelaunchFailed, r.FailureReason, true
		}
	}
	return "", "", false
}

// Static enum literals make an old provider reject the document as schema
// skew before execution. Do not retry the failure under another wire code.
func (s *hostServer) reportNewRelaunchFailure(ctx context.Context, t hostrelaunch.Target, r hostrelaunch.Result, reason string) error {
	var stored *gen.HostRelaunchFailureStored
	var err error
	stamp := r.AbsentObservedAt.UTC().Format(time.RFC3339Nano)
	switch reason {
	case hostrelaunch.FailureSetupFailed:
		response, e := gen.HostRelaunchSetupFailure(ctx, s.client, t.AppID, t.WorkerID, stamp, r.Attempt)
		err = e
		if response != nil && response.SetHostWorkerTerminals != nil {
			stored = &response.SetHostWorkerTerminals.HostRelaunchFailureStored
		}
	case hostrelaunch.FailureVerifyFailed:
		response, e := gen.HostRelaunchVerifyFailure(ctx, s.client, t.AppID, t.WorkerID, stamp, r.Attempt)
		err = e
		if response != nil && response.SetHostWorkerTerminals != nil {
			stored = &response.SetHostWorkerTerminals.HostRelaunchFailureStored
		}
	default:
		return errors.New("unsupported relaunch failure reason")
	}
	if err != nil {
		return api.MapError(err)
	}
	if stored == nil || len(stored.Refused) != 0 || len(stored.Stored) != 1 || stored.Stored[0] == nil || stored.Stored[0].Id != t.WorkerID {
		return errors.New("repair report was not confirmed stored")
	}
	return nil
}
