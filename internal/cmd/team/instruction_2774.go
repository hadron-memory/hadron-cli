package team

import (
	"context"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#2774 (child of #1617, over hadron-server#3398): the instructed twins
// of the four team-control writes, `team host issue` and the `team wakes
// due` read. Each wrapper sends the unchanged plain document without the
// flag — the no-flag wire bytes cannot move — and the Instructed one, in
// the context the caller built through the instructed-write helpers, with
// the claim exactly as the flag gave it (cor:dmo:050:12). The struct casts
// fail the build the moment a twin's selection drifts from its Instructed
// twin's; TeamStopSignalLift's result names a nested result type whose
// inner signal differs only by generated name, so its two fields are
// copied by hand where the cast cannot reach.

func sendTeamPause(ctx context.Context, client graphql.Client, appRef string, paused bool, claim *gen.ActInstructionInput) (*gen.SetTeamPauseResponse, error) {
	if claim == nil {
		return gen.SetTeamPause(ctx, client, appRef, paused)
	}
	r, err := gen.SetTeamPauseInstructed(ctx, client, appRef, paused, claim)
	if r == nil {
		return nil, err
	}
	return &gen.SetTeamPauseResponse{SetTeamPause: (*gen.SetTeamPauseSetTeamPause)(r.SetTeamPause)}, err
}

func sendTeamStopSignalSet(ctx context.Context, client graphql.Client, input *gen.SetTeamStopSignalInput, claim *gen.ActInstructionInput) (*gen.TeamStopSignalSetResponse, error) {
	if claim == nil {
		return gen.TeamStopSignalSet(ctx, client, input)
	}
	r, err := gen.TeamStopSignalSetInstructed(ctx, client, input, claim)
	if r == nil {
		return nil, err
	}
	return &gen.TeamStopSignalSetResponse{SetTeamStopSignal: (*gen.TeamStopSignalSetSetTeamStopSignal)(r.SetTeamStopSignal)}, err
}

func sendTeamStopSignalLift(ctx context.Context, client graphql.Client, appRef, id string, claim *gen.ActInstructionInput) (*gen.TeamStopSignalLiftResponse, error) {
	if claim == nil {
		return gen.TeamStopSignalLift(ctx, client, appRef, id)
	}
	r, err := gen.TeamStopSignalLiftInstructed(ctx, client, appRef, id, claim)
	if r == nil || r.LiftTeamStopSignal == nil {
		return nil, err
	}
	lift := &gen.TeamStopSignalLiftLiftTeamStopSignalLiftStopSignalResult{Changed: r.LiftTeamStopSignal.Changed}
	if r.LiftTeamStopSignal.Signal != nil {
		lift.Signal = (*gen.TeamStopSignalLiftLiftTeamStopSignalLiftStopSignalResultSignalStopSignal)(r.LiftTeamStopSignal.Signal)
	}
	return &gen.TeamStopSignalLiftResponse{LiftTeamStopSignal: lift}, err
}

func sendWorkerWakeSettings(ctx context.Context, client graphql.Client, workerRef string, input *gen.WorkerWakeSettingsInput, claim *gen.ActInstructionInput) (*gen.SetWorkerWakeSettingsResponse, error) {
	if claim == nil {
		return gen.SetWorkerWakeSettings(ctx, client, workerRef, input)
	}
	r, err := gen.SetWorkerWakeSettingsInstructed(ctx, client, workerRef, input, claim)
	if r == nil {
		return nil, err
	}
	return &gen.SetWorkerWakeSettingsResponse{SetWorkerWakeSettings: (*gen.SetWorkerWakeSettingsSetWorkerWakeSettings)(r.SetWorkerWakeSettings)}, err
}

func sendIssueHostOperation(ctx context.Context, client graphql.Client, input *gen.IssueHostOperationInput, claim *gen.ActInstructionInput) (*gen.IssueHostOperationResponse, error) {
	if claim == nil {
		return gen.IssueHostOperation(ctx, client, input)
	}
	r, err := gen.IssueHostOperationInstructed(ctx, client, input, claim)
	if r == nil {
		return nil, err
	}
	return &gen.IssueHostOperationResponse{IssueHostOperation: (*gen.IssueHostOperationIssueHostOperation)(r.IssueHostOperation)}, err
}

// sendTeamDueWakes and sendTeamDueWakesPreview carry the claim on the read
// itself (cor:dmo:050:12's manager scope of dueWakes). The instruction is
// bound wherever the flag is set — an empty page is still an admitted
// instructed read — and both previews and spending reads take it the same
// way (AT0.9372.1).

func sendTeamDueWakes(ctx context.Context, client graphql.Client, appRef string, workerRefs []string, first *int, after *string, accepts []gen.WakeKind, claim *gen.ActInstructionInput) (*gen.TeamDueWakesResponse, error) {
	if claim == nil {
		return gen.TeamDueWakes(ctx, client, appRef, workerRefs, first, after, accepts)
	}
	r, err := gen.TeamDueWakesInstructed(ctx, client, appRef, workerRefs, first, after, accepts, claim)
	if r == nil {
		return nil, err
	}
	return &gen.TeamDueWakesResponse{DueWakes: (*gen.TeamDueWakesDueWakesDueWakePage)(r.DueWakes)}, err
}

func sendTeamDueWakesPreview(ctx context.Context, client graphql.Client, appRef string, workerRefs []string, first *int, after *string, accepts []gen.WakeKind, claim *gen.ActInstructionInput) (*gen.TeamDueWakesPreviewResponse, error) {
	if claim == nil {
		return gen.TeamDueWakesPreview(ctx, client, appRef, workerRefs, first, after, accepts)
	}
	r, err := gen.TeamDueWakesPreviewInstructed(ctx, client, appRef, workerRefs, first, after, accepts, claim)
	if r == nil {
		return nil, err
	}
	return &gen.TeamDueWakesPreviewResponse{DueWakes: (*gen.TeamDueWakesPreviewDueWakesDueWakePage)(r.DueWakes)}, err
}
