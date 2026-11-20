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

// instructionFlagUsage is the --instruction flag's usage on these pages. It names
// no server revision (a reader cannot map one to a release); the page text says an
// older server refuses the flag.
const instructionFlagUsage = "the holder's instruction as <kind>:<ref>; kind is team-chat-message, task or live-chat, and ref is a reference (a team-chat message URN, a task URN, or an opaque client address), never the instruction's text; an older server refuses it"

// snapshotInstructionHelp is the --instruction paragraph for the commands that
// take the flag (list and revoke); set has no such flag and does not carry it.
const snapshotInstructionHelp = `--instruction is for a worker acting on its holder's instruction; anyone else
leaves it out. It takes <kind>:<ref>: kind is team-chat-message, task or
live-chat; ref is where the instruction was given (a team-chat message URN, a
task URN, or an opaque client address), never its text. The CLI splits at the
first colon and sends everything after it as the ref, as written.

Without --instruction, no worker session header is sent even from a bound
worktree. With it, the command uses this worktree's verified session; an
unusable binding falls back to the signed-in person with a note, and that
fallback still has its reference checked but records no worker claim. The
server checks the reference and owns App admission; refusals are never retried
or downgraded. Each admitted worker-instructed list page records one team
event, including empty pages: the default list walks to the end (one event per
page), --limit reads one page; an instructed revoke's history lookup records
one event per page before its single revoke. An older server refuses
--instruction.`

type actInstruction struct {
	input *gen.ActInstructionInput
	send  func(context.Context) (context.Context, bool, error)
}

func parseActInstruction(cmd *cobra.Command, f *cmdutil.Factory, raw string) (actInstruction, error) {
	if !cmd.Flags().Changed("instruction") {
		return actInstruction{}, nil
	}
	claim, err := instruction.Parse(raw)
	if err != nil {
		return actInstruction{}, err
	}
	if f.BeginServerTargetInstructedWrite == nil {
		return actInstruction{}, exitcode.Newf(exitcode.Error, "instruction session helper unavailable")
	}
	return actInstruction{input: claim, send: f.BeginServerTargetInstructedWrite(cmd.Context())}, nil
}
func (i actInstruction) context(ctx context.Context) (context.Context, error) {
	if i.input == nil {
		return ctx, nil
	}
	ctx, _, err := i.send(ctx)
	return ctx, err
}

// Each page is a separate server-admitted call, including the revoke lookup.
// Recheck the original binding before every call; never retry a server refusal.
func (i actInstruction) page(ctx context.Context, client graphql.Client, app string, include *bool, limit, offset int) (snapshotGrantPageDTO, error) {
	ctx, err := i.context(ctx)
	if err != nil {
		return snapshotGrantPageDTO{}, err
	}
	d := snapshotGrantPageDTO{Items: []snapshotGrantDTO{}}
	var fields []*gen.AutomationTeamSnapshotGrantFields
	if i.input == nil {
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
		r, e := gen.AutomationTeamSnapshotGrantsInstructed(ctx, client, app, include, limit, offset, i.input)
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
func (i actInstruction) revoke(ctx context.Context, client graphql.Client, id string) (*gen.AutomationTeamSnapshotGrantFields, error) {
	ctx, err := i.context(ctx)
	if err != nil {
		return nil, err
	}
	if i.input == nil {
		r, e := gen.RevokeAutomationTeamSnapshotGrant(ctx, client, id)
		if e != nil {
			return nil, mapSnapshotWriteError(e)
		}
		if r == nil || r.RevokeAutomationTeamSnapshotGrant == nil {
			return nil, nil
		}
		return &r.RevokeAutomationTeamSnapshotGrant.AutomationTeamSnapshotGrantFields, nil
	}
	r, e := gen.RevokeAutomationTeamSnapshotGrantInstructed(ctx, client, id, i.input)
	if e != nil {
		return nil, mapSnapshotWriteError(e)
	}
	if r == nil || r.RevokeAutomationTeamSnapshotGrant == nil {
		return nil, nil
	}
	return &r.RevokeAutomationTeamSnapshotGrant.AutomationTeamSnapshotGrantFields, nil
}
