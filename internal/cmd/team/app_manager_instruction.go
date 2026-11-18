package team

import (
	"context"
	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/spf13/cobra"
)

func addManagerInstruction(cmd *cobra.Command) {
	cmd.Flags().String("instruction", "", instruction.FlagUsageForServer("4bac6127"))
	cmd.Long += "\n\n" + instruction.ManagerHelp
}
func managerInstruction(cmd *cobra.Command, f *cmdutil.Factory) (*gen.ActInstructionInput, InstructedWrite, error) {
	raw, _ := cmd.Flags().GetString("instruction")
	return staffingInstruction(cmd, f, raw)
}
func managerWriteContext(cmd *cobra.Command, ref string, claim *gen.ActInstructionInput, instructed InstructedWrite) (context.Context, error) {
	if claim == nil {
		return cmd.Context(), nil
	}
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
func setManagerTickerSettings(ctx context.Context, client graphql.Client, ref string, cadence, cap, self, budget *int, clear *bool, claim *gen.ActInstructionInput) (*gen.TeamTickerSettingsSetResponse, error) {
	if claim == nil {
		return gen.TeamTickerSettingsSet(ctx, client, ref, cadence, cap, self, budget, clear)
	}
	r, err := gen.TeamTickerSettingsSetInstructed(ctx, client, ref, cadence, cap, self, budget, clear, claim)
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
