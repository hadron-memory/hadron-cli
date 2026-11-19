package app

import (
	"context"
	"encoding/json"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/spf13/cobra"
)

// managerInstructionMinimum is the app door's server minimum for --instruction.
const managerInstructionMinimum = "2026-11-04"

func addManagerInstruction(cmd *cobra.Command) {
	instruction.AddManagerFlag(cmd, managerInstructionMinimum)
}

// The app manager writes reuse the member-write helpers on purpose: the flag,
// the parse and the App-target-bound snapshot/recheck are the same contract
// as `app member add|remove` (cli#1594), so there is no manager-specific copy.
// memberInstruction is called at entry; memberWriteContext runs after local
// validation, confirmation and client setup, immediately before the mutation.
// Its target-id argument is "" because an App write names the App by ref only
// (the ref is passed as targetAppRef); there is no separate App id to pin.
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
