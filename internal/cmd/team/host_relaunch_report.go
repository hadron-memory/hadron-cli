package team

import (
	"context"
	"errors"
	"fmt"
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
	if response == nil || response.SetHostWorkerTerminals == nil {
		return errors.New("repair report was not confirmed stored: the server returned no result")
	}
	refused := make([]string, 0, len(response.SetHostWorkerTerminals.Refused))
	for _, row := range response.SetHostWorkerTerminals.Refused {
		code := ""
		if row != nil {
			code = row.Code
		}
		refused = append(refused, code)
	}
	stored := make([]string, 0, len(response.SetHostWorkerTerminals.Stored))
	for _, row := range response.SetHostWorkerTerminals.Stored {
		id := "" // a null row counts, and never names this worker
		if row != nil {
			id = row.Id
		}
		stored = append(stored, id)
	}
	return confirmRepairStored(t.WorkerID, refused, stored)
}

// confirmRepairStored is nil only for exactly one stored row naming this
// worker and no refusal. Otherwise the error says which of those failed, as
// LOCAL diagnostics: the server's refused codes (a closed set, shown only
// when they are plain codes) and the stored count or the mismatch, never an
// id and never anything that reaches the report (cor:agt:070:01: a repair
// outcome is never free text; cli#2172).
func confirmRepairStored(workerID string, refused, stored []string) error {
	if len(refused) == 0 && len(stored) == 1 && stored[0] == workerID {
		return nil
	}
	parts := []string{"repair report was not confirmed stored"}
	if len(refused) > 0 {
		codes := make([]string, 0, len(refused))
		for _, c := range refused {
			codes = append(codes, plainRefusalCode(c))
		}
		parts = append(parts, "refused "+strings.Join(codes, ", "))
	}
	parts = append(parts, fmt.Sprintf("stored %d, want 1", len(stored)))
	if len(stored) == 1 && stored[0] != workerID {
		parts = append(parts, "the stored row names another worker")
	}
	return errors.New(strings.Join(parts, ": "))
}

// refusalCodes is the closed set the server refuses a worker's terminal
// report with (#2771). Only these are shown in a local diagnostic: anything
// else, however plain it looks, is not copied (an id or a name in upper case
// would pass any character filter).
var refusalCodes = map[string]bool{
	"WORKER_NOT_FOUND": true, "TERMINAL_REPORT_STALE": true, "REPAIR_NOT_DARK": true,
	"REPAIR_ATTEMPT_INVALID": true, "REPAIR_REASON_INVALID": true,
}

// plainRefusalCode is the refusal code when it is one of the closed set, else
// a fixed placeholder, so an unexpected answer cannot put server text into a
// local error.
func plainRefusalCode(code string) string {
	if refusalCodes[code] {
		return code
	}
	return "(not a known refusal code)"
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
	if stored == nil {
		return errors.New("repair report was not confirmed stored: the server returned no result")
	}
	refused := make([]string, 0, len(stored.Refused))
	for _, row := range stored.Refused {
		code := ""
		if row != nil {
			code = row.Code
		}
		refused = append(refused, code)
	}
	ids := make([]string, 0, len(stored.Stored))
	for _, row := range stored.Stored {
		id := "" // a null row counts, and never names this worker
		if row != nil {
			id = row.Id
		}
		ids = append(ids, id)
	}
	return confirmRepairStored(t.WorkerID, refused, ids)
}
