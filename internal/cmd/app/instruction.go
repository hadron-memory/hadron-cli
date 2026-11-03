package app

import (
	"context"
	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/spf13/cobra"
	"strings"
)

const credentialInstructionHelp = `--instruction <kind>:<ref> claims, for this call, that the worker's holder
instructed it. kind is team-chat-message, task or live-chat; ref is everything
after the first colon, unchanged, never the instruction's text. The holder
answers for the claim and must manage the App. The server checks the reference,
never that an instruction happened. A host-launched session is refused whatever
it claims; there is no grant arm here. Needs server c3ca05b5 or later.

With the flag, a complete, unchanged worktree binding for this server sends
its worker session; the server checks the target App and holder authority.
An unbound, corrupt, incomplete or foreign-server binding refuses locally
(exit 2), never falling back to the person. No extra lookup, retry or downgrade
is made. Without the flag, the call acts as you, with no worktree session; its
document and attribution stay unchanged. Minting, rotating and granting remain person-only and have no flag.

An admitted instructed list is NOT record-free: every call (each page) records
one app.credential-act event, with no dedupe. A changing instructed revoke
records one; an already-revoked no-op records none. Events name worker, holder,
App, act kind and instruction, never a key, scope or grant id.`

func addCredentialInstruction(cmd *cobra.Command) {
	cmd.Flags().String("instruction", "", strings.Replace(instruction.FlagUsage, "17ef5905", "c3ca05b5", 1))
	cmd.Long += "\n\n" + credentialInstructionHelp
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

// Each bridge uses a separately generated instructed document; nil takes the
// unchanged legacy operation. Shared generated row types keep output identical.
func keysMetadata(ctx context.Context, c graphql.Client, ref string, i *gen.ActInstructionInput) (*gen.AppKeysMetadataResponse, error) {
	if i == nil {
		return gen.AppKeysMetadata(ctx, c, ref)
	}
	r, e := gen.AppKeysMetadataInstructed(ctx, c, ref, i)
	if e != nil || r == nil {
		return nil, e
	}
	return &gen.AppKeysMetadataResponse{AppKeys: r.AppKeys}, nil
}
func keysScopes(ctx context.Context, c graphql.Client, ref string, i *gen.ActInstructionInput) (*gen.AppKeysScopesResponse, error) {
	if i == nil {
		return gen.AppKeysScopes(ctx, c, ref)
	}
	r, e := gen.AppKeysScopesInstructed(ctx, c, ref, i)
	if e != nil || r == nil {
		return nil, e
	}
	return &gen.AppKeysScopesResponse{AppKeys: r.AppKeys}, nil
}
func keysHostUses(ctx context.Context, c graphql.Client, ref string, first int, i *gen.ActInstructionInput) (*gen.AppKeysScopesHostUsesResponse, error) {
	if i == nil {
		return gen.AppKeysScopesHostUses(ctx, c, ref, first)
	}
	r, e := gen.AppKeysScopesHostUsesInstructed(ctx, c, ref, first, i)
	if e != nil || r == nil {
		return nil, e
	}
	return &gen.AppKeysScopesHostUsesResponse{AppKeys: r.AppKeys}, nil
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
func memoryGrants(ctx context.Context, c graphql.Client, ref string, revoked *bool, i *gen.ActInstructionInput) (*gen.AppMemoryWriteGrantsResponse, error) {
	if i == nil {
		return gen.AppMemoryWriteGrants(ctx, c, ref, revoked)
	}
	r, e := gen.AppMemoryWriteGrantsInstructed(ctx, c, i, ref, revoked)
	if e != nil || r == nil {
		return nil, e
	}
	return &gen.AppMemoryWriteGrantsResponse{AppMemoryWriteGrants: r.AppMemoryWriteGrants}, nil
}
func revokeMemoryGrant(ctx context.Context, c graphql.Client, id string, i *gen.ActInstructionInput) (*gen.RevokeAppMemoryWriteGrantResponse, error) {
	if i == nil {
		return gen.RevokeAppMemoryWriteGrant(ctx, c, id)
	}
	r, e := gen.RevokeAppMemoryWriteGrantInstructed(ctx, c, id, i)
	if e != nil || r == nil {
		return nil, e
	}
	return &gen.RevokeAppMemoryWriteGrantResponse{RevokeAppMemoryWriteGrant: r.RevokeAppMemoryWriteGrant}, nil
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
