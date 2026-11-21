package app

import (
	"context"
	"fmt"
	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/spf13/cobra"
)

const credentialInstructionHelp = `--instruction <kind>:<ref> is for an AI worker acting on its holder's say-so
(the holder is the person responsible for the worker). It claims that the
holder told the worker to make this call. kind is team-chat-message, task or
live-chat; ref points to where they said so (everything after the first colon,
sent unchanged, never the instruction's text). The holder answers for the claim
and must manage the App; the server checks the reference, never that an
instruction happened, and a host-launched session is refused whatever it
claims.

Without the flag the call acts as you. With it, the call goes out through the
worker session of the bound working directory; a missing, incomplete or
foreign-server binding refuses locally (exit 2) and never falls back to you;
nothing is retried or downgraded. Needs server ` + instruction.ServerFloorInstructionActs + ` or later. Minting,
rotating and granting stay person-only and have no flag. More: "hadron
agentic-usage", "Instructed credential acts".`

// What an instructed call records, said per act: a LIST paragraph on a revoke page
// is copy-paste drift (cli#1053 audit, finding C3).
const (
	credentialInstructionListRecord = `An admitted instructed list is NOT record-free: every call (each page) records
one app.credential-act event, with no dedupe. Events name worker, holder, App,
act kind and instruction, never a key, scope or grant id.`
	credentialInstructionRevokeRecord = `A changing instructed revoke records one app.credential-act event; a revoke
that changes nothing records none. Events name worker, holder, App, act kind
and instruction, never a key, scope or grant id.`
)

// addCredentialInstruction adds --instruction to a list page.
func addCredentialInstruction(cmd *cobra.Command) {
	addCredentialInstructionWith(cmd, credentialInstructionListRecord)
}

// addCredentialInstructionRevoke adds --instruction to a revoke page.
func addCredentialInstructionRevoke(cmd *cobra.Command) {
	addCredentialInstructionWith(cmd, credentialInstructionRevokeRecord)
}

// addCredentialInstructionWith is the one place the flag and its help are added;
// the two acts differ only in what an instructed call records.
func addCredentialInstructionWith(cmd *cobra.Command, record string) {
	cmd.Flags().String("instruction", "", instruction.FlagUsageForServer(instruction.ServerFloorInstructionActs))
	cmd.Long += "\n\n" + credentialInstructionHelp + "\n\n" + record
}

func credentialInstruction(cmd *cobra.Command, f *cmdutil.Factory) (*gen.ActInstructionInput, error) {
	if !cmd.Flags().Changed("instruction") {
		return nil, nil
	}
	raw, _ := cmd.Flags().GetString("instruction")
	claim, err := instruction.Parse(raw)
	if err != nil {
		return nil, err
	}
	if f.InstructedContext == nil {
		return nil, exitcode.Newf(exitcode.Usage, "worker-session verification unavailable; nothing was sent")
	}
	if f.InstructedContext != nil {
		ctx, err := f.InstructedContext(cmd.Context())
		if err != nil {
			return nil, err
		}
		cmd.SetContext(ctx)
	}
	return claim, nil
}

// List bridges keep ordinary and instructed documents separate on every page.
func keysMetadata(ctx context.Context, c graphql.Client, ref string, i *gen.ActInstructionInput) ([]*gen.AppKeysMetadataRows1595, error) {
	return api.CollectHasMore(func(offset int) ([]*gen.AppKeysMetadataRows1595, int, bool, error) {
		if i == nil {
			r, e := gen.AppKeysMetadata(ctx, c, ref, api.InventoryPageSize, offset)
			if e != nil {
				return nil, 0, false, e
			}
			if r == nil || r.AppKeysPage == nil {
				return nil, 0, false, fmt.Errorf("the server returned no App-key inventory page")
			}
			return r.AppKeysPage.Items, r.AppKeysPage.Total, r.AppKeysPage.HasMore, nil
		}
		r, e := gen.AppKeysMetadataInstructed(ctx, c, ref, i, api.InventoryPageSize, offset)
		if e != nil {
			return nil, 0, false, e
		}
		if r == nil || r.AppKeysPage == nil {
			return nil, 0, false, fmt.Errorf("the server returned no App-key inventory page")
		}
		return r.AppKeysPage.Items, r.AppKeysPage.Total, r.AppKeysPage.HasMore, nil
	}, func(row *gen.AppKeysMetadataRows1595) *string {
		if row == nil {
			return nil
		}
		return &row.Id
	})
}
func keysScopes(ctx context.Context, c graphql.Client, ref string, i *gen.ActInstructionInput) ([]*gen.AppKeysScopesRows1595, error) {
	return api.CollectHasMore(func(offset int) ([]*gen.AppKeysScopesRows1595, int, bool, error) {
		if i == nil {
			r, e := gen.AppKeysScopes(ctx, c, ref, api.InventoryPageSize, offset)
			if e != nil {
				return nil, 0, false, e
			}
			if r == nil || r.AppKeysPage == nil {
				return nil, 0, false, fmt.Errorf("the server returned no App-key inventory page")
			}
			return r.AppKeysPage.Items, r.AppKeysPage.Total, r.AppKeysPage.HasMore, nil
		}
		r, e := gen.AppKeysScopesInstructed(ctx, c, ref, i, api.InventoryPageSize, offset)
		if e != nil {
			return nil, 0, false, e
		}
		if r == nil || r.AppKeysPage == nil {
			return nil, 0, false, fmt.Errorf("the server returned no App-key inventory page")
		}
		return r.AppKeysPage.Items, r.AppKeysPage.Total, r.AppKeysPage.HasMore, nil
	}, func(row *gen.AppKeysScopesRows1595) *string {
		if row == nil {
			return nil
		}
		return &row.Id
	})
}
func keysHostUses(ctx context.Context, c graphql.Client, ref string, first int, i *gen.ActInstructionInput) ([]*gen.AppKeysScopesHostUsesRows1595, error) {
	return api.CollectHasMore(func(offset int) ([]*gen.AppKeysScopesHostUsesRows1595, int, bool, error) {
		if i == nil {
			r, e := gen.AppKeysScopesHostUses(ctx, c, ref, first, api.InventoryPageSize, offset)
			if e != nil {
				return nil, 0, false, e
			}
			if r == nil || r.AppKeysPage == nil {
				return nil, 0, false, fmt.Errorf("the server returned no App-key inventory page")
			}
			return r.AppKeysPage.Items, r.AppKeysPage.Total, r.AppKeysPage.HasMore, nil
		}
		r, e := gen.AppKeysScopesHostUsesInstructed(ctx, c, ref, first, i, api.InventoryPageSize, offset)
		if e != nil {
			return nil, 0, false, e
		}
		if r == nil || r.AppKeysPage == nil {
			return nil, 0, false, fmt.Errorf("the server returned no App-key inventory page")
		}
		return r.AppKeysPage.Items, r.AppKeysPage.Total, r.AppKeysPage.HasMore, nil
	}, func(row *gen.AppKeysScopesHostUsesRows1595) *string {
		if row == nil {
			return nil
		}
		return &row.Id
	})
}
func revokeKey(ctx context.Context, c graphql.Client, id string, i *gen.ActInstructionInput) (*gen.RevokeAppKeyResponse, error) {
	if i == nil {
		return gen.RevokeAppKey(ctx, c, id)
	}
	r, e := gen.RevokeAppKeyInstructed(ctx, c, id, i)
	if e != nil || r == nil {
		return nil, e
	}
	return &gen.RevokeAppKeyResponse{RevokeAppKey: r.RevokeAppKey}, nil
}
func revokeScope(ctx context.Context, c graphql.Client, id, scope string, i *gen.ActInstructionInput) (*gen.RevokeAppKeyScopeResponse, error) {
	if i == nil {
		return gen.RevokeAppKeyScope(ctx, c, id, scope)
	}
	r, e := gen.RevokeAppKeyScopeInstructed(ctx, c, id, scope, i)
	if e != nil || r == nil {
		return nil, e
	}
	return &gen.RevokeAppKeyScopeResponse{RevokeAppKeyScope: r.RevokeAppKeyScope}, nil
}
func memoryGrantsCurrent(ctx context.Context, c graphql.Client, ref string, revoked *bool, i *gen.ActInstructionInput) (*gen.AppMemoryWriteGrantsResponse, error) {
	if i == nil {
		return gen.AppMemoryWriteGrants(ctx, c, ref, revoked)
	}
	r, e := gen.AppMemoryWriteGrantsInstructed(ctx, c, i, ref, revoked)
	if r == nil {
		return nil, e
	}
	return &gen.AppMemoryWriteGrantsResponse{AppMemoryWriteGrants: r.AppMemoryWriteGrants}, e
}
func revokeMemoryGrantCurrent(ctx context.Context, c graphql.Client, id string, i *gen.ActInstructionInput) (*gen.RevokeAppMemoryWriteGrantResponse, error) {
	if i == nil {
		return gen.RevokeAppMemoryWriteGrant(ctx, c, id)
	}
	r, e := gen.RevokeAppMemoryWriteGrantInstructed(ctx, c, id, i)
	if r == nil {
		return nil, e
	}
	return &gen.RevokeAppMemoryWriteGrantResponse{RevokeAppMemoryWriteGrant: r.RevokeAppMemoryWriteGrant}, e
}

// credentialClient checks the original snapshot again after credential-store
// setup, which may block. It never adopts a successor binding as the caller.
func credentialClient(cmd *cobra.Command, f *cmdutil.Factory, claim *gen.ActInstructionInput) (graphql.Client, error) {
	client, err := f.GraphQLClient()
	if err != nil {
		return nil, err
	}
	if claim != nil {
		ctx, err := f.InstructedContext(cmd.Context())
		if err != nil {
			return nil, err
		}
		cmd.SetContext(ctx)
	}
	return client, nil
}

// memberInstructed is a member add/remove's at-send-time context for an
// instructed write (cli#1594).
type memberInstructed func(ctx context.Context, targetAppID, targetAppRef string) (context.Context, bool, error)

// memberInstruction reads `app member add|remove --instruction` before any
// prompt or request (a bad kind or blank ref: exit 2, nothing sent) and, with
// the flag, snapshots the binding. Without it both results are nil: the
// command's document and its headerless, as-the-person attribution are
// unchanged.
func memberInstruction(cmd *cobra.Command, f *cmdutil.Factory) (*gen.ActInstructionInput, memberInstructed, error) {
	if !cmd.Flags().Changed("instruction") {
		return nil, nil, nil
	}
	raw, _ := cmd.Flags().GetString("instruction")
	claim, err := instruction.Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	if f.BeginInstructedWrite == nil {
		return nil, nil, exitcode.Newf(exitcode.Usage, "worker-session verification unavailable; nothing was sent")
	}
	return claim, f.BeginInstructedWrite(cmd.Context()), nil
}

func addMemberInstruction(cmd *cobra.Command) {
	cmd.Flags().String("instruction", "", instruction.StaffingFlagUsage)
	cmd.Long += "\n\n" + instruction.StaffingHelp("app.members")
}

// memberWriteContext is the context the one member write goes out in. Its
// error comes before anything is sent, so it is returned as is, never as an
// unknown outcome.
func memberWriteContext(cmd *cobra.Command, appRef string, claim *gen.ActInstructionInput, instructed memberInstructed) (context.Context, error) {
	if claim == nil {
		return cmd.Context(), nil
	}
	ctx, _, err := instructed(cmd.Context(), "", appRef)
	return ctx, err
}

func ensureMember(ctx context.Context, c graphql.Client, appRef, user, role string, claim *gen.ActInstructionInput) (*gen.EnsureAppMemberResponse, error) {
	if claim == nil {
		return gen.EnsureAppMember(ctx, c, appRef, user, role)
	}
	r, err := gen.EnsureAppMemberInstructed(ctx, c, appRef, user, role, claim)
	if r == nil || r.EnsureAppMember == nil {
		return nil, err
	}
	m := r.EnsureAppMember
	out := &gen.EnsureAppMemberEnsureAppMember{Role: m.Role, CreatedAt: m.CreatedAt}
	if m.User != nil {
		out.User = &gen.EnsureAppMemberEnsureAppMemberUser{Id: m.User.Id, Handle: m.User.Handle, Urn: m.User.Urn}
	}
	return &gen.EnsureAppMemberResponse{EnsureAppMember: out}, err
}

func removeMember(ctx context.Context, c graphql.Client, appRef, user string, claim *gen.ActInstructionInput) (*gen.RemoveAppMemberResponse, error) {
	if claim == nil {
		return gen.RemoveAppMember(ctx, c, appRef, user)
	}
	r, err := gen.RemoveAppMemberInstructed(ctx, c, appRef, user, claim)
	if r == nil {
		return nil, err
	}
	return &gen.RemoveAppMemberResponse{RemoveAppMember: r.RemoveAppMember}, err
}
