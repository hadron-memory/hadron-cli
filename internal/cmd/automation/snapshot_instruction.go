package automation

import (
	"context"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/spf13/cobra"
)

type snapshotInstruction struct {
	claim *gen.ActInstructionInput
	send  func(context.Context) (context.Context, bool, error)
}

func parseSnapshotInstruction(cmd *cobra.Command, f *cmdutil.Factory, raw string) (snapshotInstruction, error) {
	if !cmd.Flags().Changed("instruction") {
		return snapshotInstruction{}, nil
	}
	claim, err := instruction.Parse(raw)
	if err != nil {
		return snapshotInstruction{}, err
	}
	if f.BeginServerTargetInstructedWrite == nil {
		return snapshotInstruction{}, exitcode.Newf(exitcode.Error, "instruction session helper unavailable")
	}
	return snapshotInstruction{claim: claim, send: f.BeginServerTargetInstructedWrite(cmd.Context())}, nil
}
func (i snapshotInstruction) context(ctx context.Context) (context.Context, error) {
	if i.claim == nil {
		return ctx, nil
	}
	ctx, _, err := i.send(ctx)
	return ctx, err
}

// Each page is a separate server-admitted call, including the revoke lookup.
// Recheck the original binding before every call; never retry a server refusal.
func (i snapshotInstruction) page(ctx context.Context, client graphql.Client, app string, include *bool, limit, offset int) (snapshotGrantPageDTO, error) {
	ctx, err := i.context(ctx)
	if err != nil {
		return snapshotGrantPageDTO{}, err
	}
	d := snapshotGrantPageDTO{Items: []snapshotGrantDTO{}}
	var fields []*gen.AutomationTeamSnapshotGrantFields
	if i.claim == nil {
		r, e := gen.AutomationTeamSnapshotGrants(ctx, client, app, include, limit, offset)
		if e != nil {
			return d, api.MapErrorKeepingCode(e)
		}
		if r == nil || r.AutomationTeamSnapshotGrants == nil {
			return d, exitcode.Newf(exitcode.Error, "the server returned no team snapshot grant page")
		}
		p := r.AutomationTeamSnapshotGrants
		d.Total = p.Total
		d.HasMore = p.HasMore
		for _, g := range p.Items {
			if g == nil {
				fields = append(fields, nil)
			} else {
				fields = append(fields, &g.AutomationTeamSnapshotGrantFields)
			}
		}
	} else {
		r, e := gen.AutomationTeamSnapshotGrantsInstructed(ctx, client, app, include, limit, offset, i.claim)
		if e != nil {
			return d, api.MapErrorKeepingCode(e)
		}
		if r == nil || r.AutomationTeamSnapshotGrants == nil {
			return d, exitcode.Newf(exitcode.Error, "the server returned no team snapshot grant page")
		}
		p := r.AutomationTeamSnapshotGrants
		d.Total = p.Total
		d.HasMore = p.HasMore
		for _, g := range p.Items {
			if g == nil {
				fields = append(fields, nil)
			} else {
				fields = append(fields, &g.AutomationTeamSnapshotGrantFields)
			}
		}
	}
	for _, g := range fields {
		if g == nil {
			return d, exitcode.Newf(exitcode.Error, "the server returned a null team snapshot grant")
		}
		row, e := snapshotGrantFrom(*g)
		if e != nil {
			return d, e
		}
		d.Items = append(d.Items, row)
	}
	return d, nil
}
func (i snapshotInstruction) revoke(ctx context.Context, client graphql.Client, id string) (*gen.AutomationTeamSnapshotGrantFields, error) {
	ctx, err := i.context(ctx)
	if err != nil {
		return nil, err
	}
	if i.claim == nil {
		r, e := gen.RevokeAutomationTeamSnapshotGrant(ctx, client, id)
		if e != nil {
			return nil, mapSnapshotWriteError(e)
		}
		if r == nil || r.RevokeAutomationTeamSnapshotGrant == nil {
			return nil, nil
		}
		return &r.RevokeAutomationTeamSnapshotGrant.AutomationTeamSnapshotGrantFields, nil
	}
	r, e := gen.RevokeAutomationTeamSnapshotGrantInstructed(ctx, client, id, i.claim)
	if e != nil {
		return nil, mapSnapshotWriteError(e)
	}
	if r == nil || r.RevokeAutomationTeamSnapshotGrant == nil {
		return nil, nil
	}
	return &r.RevokeAutomationTeamSnapshotGrant.AutomationTeamSnapshotGrantFields, nil
}
