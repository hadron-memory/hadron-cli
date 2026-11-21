package team

import (
	"context"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/spf13/cobra"
)

// managerInstructionMinimum is the team door's server minimum for
// --instruction (commit 4bac6127, merged 2026-11-04, hadron-server#3398),
// stated as the date the shared instruction text on every page already uses,
// so a page says it once (cli#2617). It is display text only:
// instruction.FlagUsageForServer prints it after "Needs server" and nothing
// compares it with a server build. Today it is the same date as the app door's
// (both are instruction.ServerFloorInstructionManagerWrites); each door keeps its OWN constant name
// so a floor that later differs for one door changes in one place.
const managerInstructionMinimum = instruction.ServerFloorInstructionManagerWrites

// deliverySettingsInstructionMinimum is the same requirement, kept as its own
// name for delivery-settings set.
const deliverySettingsInstructionMinimum = managerInstructionMinimum

func addManagerInstruction(cmd *cobra.Command) {
	instruction.AddManagerFlag(cmd, managerInstructionMinimum)
}

func managerInstruction(cmd *cobra.Command, f *cmdutil.Factory) (*gen.ActInstructionInput, InstructedWrite, error) {
	raw, err := cmd.Flags().GetString("instruction")
	if err != nil {
		return nil, nil, err
	}
	return staffingInstruction(cmd, f, raw)
}
func managerWriteContext(cmd *cobra.Command, ref string, claim *gen.ActInstructionInput, instructed InstructedWrite) (context.Context, error) {
	if claim == nil {
		return cmd.Context(), nil
	}
	// The target-id argument is "": an App write names the App by ref only,
	// so there is no separate App id to pin in the at-send-time check.
	ctx, _, err := instructed(cmd.Context(), "", ref)
	return ctx, err
}
func setManagerDeliverySettings(ctx context.Context, client graphql.Client, ref string, batch, busy, stuck, check, dark, failed *int, claim *gen.ActInstructionInput) (*gen.TeamDeliverySettingsSetResponse, error) {
	if claim == nil {
		return gen.TeamDeliverySettingsSet(ctx, client, ref, batch, busy, stuck, check, dark, failed)
	}
	r, err := gen.TeamDeliverySettingsSetInstructed(ctx, client, ref, batch, busy, stuck, check, dark, failed, claim)
	if r == nil {
		return nil, err
	}
	return &gen.TeamDeliverySettingsSetResponse{SetAppDeliverySettings: (*gen.TeamDeliverySettingsSetSetAppDeliverySettings)(r.SetAppDeliverySettings)}, err
}
func setManagerTickerSettings(ctx context.Context, client graphql.Client, ref string, cadence, liveCap, self, budget *int, clearBudget *bool, claim *gen.ActInstructionInput) (*gen.TeamTickerSettingsSetResponse, error) {
	if claim == nil {
		return gen.TeamTickerSettingsSet(ctx, client, ref, cadence, liveCap, self, budget, clearBudget)
	}
	r, err := gen.TeamTickerSettingsSetInstructed(ctx, client, ref, cadence, liveCap, self, budget, clearBudget, claim)
	if r == nil {
		return nil, err
	}
	return &gen.TeamTickerSettingsSetResponse{SetAppTickerSettings: (*gen.TeamTickerSettingsSetSetAppTickerSettings)(r.SetAppTickerSettings)}, err
}
func revokeManagerAppGrant(ctx context.Context, client graphql.Client, ref, worker string, name gen.AppGrantName, claim *gen.ActInstructionInput) (*gen.TeamAppGrantRmResponse, error) {
	if claim == nil {
		return gen.TeamAppGrantRm(ctx, client, ref, worker, name)
	}
	r, err := gen.TeamAppGrantRmInstructed(ctx, client, ref, worker, name, claim)
	if r == nil {
		return nil, err
	}
	return &gen.TeamAppGrantRmResponse{RevokeAppGrant: (*gen.TeamAppGrantRmRevokeAppGrant)(r.RevokeAppGrant)}, err
}
