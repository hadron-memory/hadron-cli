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
// --instruction; it is deliberately independent of the app door's.
const managerInstructionMinimum = "4bac6127"

// deliverySettingsInstructionMinimum is the same requirement as
// managerInstructionMinimum (commit 4bac6127, merged 2026-11-04, hadron-server#3398)
// stated as the date the page body already uses, so one page says it once.
// It is display text only: instruction.FlagUsageForServer prints it after
// "Needs server" and nothing compares it with a server build (the app door
// already passes a date, "2026-11-03", the same way). Only delivery-settings
// set and grant rm use it; the other team doors still print the commit (a follow-up would
// move them together).
const deliverySettingsInstructionMinimum = "2026-11-04"

func addManagerInstruction(cmd *cobra.Command) {
	instruction.AddManagerFlag(cmd, managerInstructionMinimum)
}

func managerInstruction(cmd *cobra.Command, f *cmdutil.Factory) (*gen.ActInstructionInput, InstructedWrite, error) {
	raw, _ := cmd.Flags().GetString("instruction")
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
