package app

import (
	"context"
	"encoding/json"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/spf13/cobra"
)

func addManagerInstruction(cmd *cobra.Command) {
	cmd.Flags().String("instruction", "", instruction.FlagUsageForServer("2026-11-04"))
	cmd.Long += "\n\n" + instruction.ManagerHelp
}

// Manager writes use the same App-target-bound snapshot/recheck as members.
// memberInstruction is called at entry; memberWriteContext runs after local
// validation, confirmation and client setup, immediately before the mutation.
func setManagerProperties(ctx context.Context, client graphql.Client, ref string, props *json.RawMessage, revision *int, claim *gen.ActInstructionInput) (*gen.SetAppPropertiesResponse, error) {
	if claim == nil {
		return gen.SetAppProperties(ctx, client, ref, props, revision)
	}
	r, err := gen.SetAppPropertiesInstructed(ctx, client, ref, props, revision, claim)
	if r == nil {
		return nil, err
	}
	return &gen.SetAppPropertiesResponse{SetAppProperties: (*gen.SetAppPropertiesSetAppPropertiesApp)(r.SetAppProperties)}, err
}
func setManagerModelGroup(ctx context.Context, client graphql.Client, ref, name string, patterns []string, claim *gen.ActInstructionInput) (*gen.SetAppModelGroupResponse, error) {
	if claim == nil {
		return gen.SetAppModelGroup(ctx, client, ref, name, patterns)
	}
	r, err := gen.SetAppModelGroupInstructed(ctx, client, ref, name, patterns, claim)
	if r == nil {
		return nil, err
	}
	return &gen.SetAppModelGroupResponse{SetAppModelGroup: (*gen.SetAppModelGroupSetAppModelGroup)(r.SetAppModelGroup)}, err
}
func clearManagerModelGroup(ctx context.Context, client graphql.Client, ref, name string, claim *gen.ActInstructionInput) (*gen.DeleteAppModelGroupResponse, error) {
	if claim == nil {
		return gen.DeleteAppModelGroup(ctx, client, ref, name)
	}
	r, err := gen.DeleteAppModelGroupInstructed(ctx, client, ref, name, claim)
	if r == nil {
		return nil, err
	}
	return &gen.DeleteAppModelGroupResponse{DeleteAppModelGroup: r.DeleteAppModelGroup}, err
}
