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

// snapshotTriggerFilter is list's --trigger-kind / --trigger-id (cli#2775). A nil
// field was not given and is omitted; a set one is sent exactly as given (the
// server validates triggerKind). With neither set, the legacy documents are sent.
type snapshotTriggerFilter struct {
	kind, id *string
}

func (t snapshotTriggerFilter) set() bool { return t.kind != nil || t.id != nil }

// snapshotRawPage is one page as any of the four list documents returned it.
type snapshotRawPage struct {
	items   []*gen.AutomationTeamSnapshotGrantFields
	total   int
	hasMore bool
}

// snapshotPageOf collects a generated page's rows as their shared fragment.
func snapshotPageOf[T any](items []*T, fields func(*T) *gen.AutomationTeamSnapshotGrantFields, total int, hasMore bool) *snapshotRawPage {
	p := &snapshotRawPage{total: total, hasMore: hasMore}
	for _, g := range items {
		if g == nil {
			p.items = append(p.items, nil)
		} else {
			p.items = append(p.items, fields(g))
		}
	}
	return p
}

// fetchSnapshotPage sends one list request: the legacy person or instructed
// document when no trigger filter is set (their bytes unchanged), else the
// matching ByTrigger document. The result is (nil, nil) when the server answers
// without a page or without the list field; page turns that into an error, so
// no caller sees a nil page as success. The nil guard is the first statement of
// each branch below (r == nil || the page field == nil), before any p.Items read.
func (i actInstruction) fetchSnapshotPage(ctx context.Context, client graphql.Client, app string, include *bool, trigger snapshotTriggerFilter, limit, offset int) (*snapshotRawPage, error) {
	switch {
	case i.input == nil && !trigger.set():
		r, e := gen.AutomationTeamSnapshotGrants(ctx, client, app, include, limit, offset)
		if e != nil || r == nil || r.AutomationTeamSnapshotGrants == nil {
			return nil, e
		}
		p := r.AutomationTeamSnapshotGrants
		return snapshotPageOf(p.Items, func(g *gen.AutomationTeamSnapshotGrantsAutomationTeamSnapshotGrantsAutomationTeamSnapshotGrantPageItemsAutomationTeamSnapshotGrant) *gen.AutomationTeamSnapshotGrantFields {
			return &g.AutomationTeamSnapshotGrantFields
		}, p.Total, p.HasMore), nil
	case i.input == nil:
		r, e := gen.AutomationTeamSnapshotGrantsByTrigger(ctx, client, app, include, trigger.kind, trigger.id, limit, offset)
		if e != nil || r == nil || r.AutomationTeamSnapshotGrants == nil {
			return nil, e
		}
		p := r.AutomationTeamSnapshotGrants
		return snapshotPageOf(p.Items, func(g *gen.AutomationTeamSnapshotGrantsByTriggerAutomationTeamSnapshotGrantsAutomationTeamSnapshotGrantPageItemsAutomationTeamSnapshotGrant) *gen.AutomationTeamSnapshotGrantFields {
			return &g.AutomationTeamSnapshotGrantFields
		}, p.Total, p.HasMore), nil
	case !trigger.set():
		r, e := gen.AutomationTeamSnapshotGrantsInstructed(ctx, client, app, include, limit, offset, i.input)
		if e != nil || r == nil || r.AutomationTeamSnapshotGrants == nil {
			return nil, e
		}
		p := r.AutomationTeamSnapshotGrants
		return snapshotPageOf(p.Items, func(g *gen.AutomationTeamSnapshotGrantsInstructedAutomationTeamSnapshotGrantsAutomationTeamSnapshotGrantPageItemsAutomationTeamSnapshotGrant) *gen.AutomationTeamSnapshotGrantFields {
			return &g.AutomationTeamSnapshotGrantFields
		}, p.Total, p.HasMore), nil
	default:
		r, e := gen.AutomationTeamSnapshotGrantsByTriggerInstructed(ctx, client, app, include, trigger.kind, trigger.id, limit, offset, i.input)
		if e != nil || r == nil || r.AutomationTeamSnapshotGrants == nil {
			return nil, e
		}
		p := r.AutomationTeamSnapshotGrants
		return snapshotPageOf(p.Items, func(g *gen.AutomationTeamSnapshotGrantsByTriggerInstructedAutomationTeamSnapshotGrantsAutomationTeamSnapshotGrantPageItemsAutomationTeamSnapshotGrant) *gen.AutomationTeamSnapshotGrantFields {
			return &g.AutomationTeamSnapshotGrantFields
		}, p.Total, p.HasMore), nil
	}
}

// Each page is a separate server-admitted call, including the revoke lookup.
// Recheck the original binding before every call; never retry a server refusal.
func (i actInstruction) page(ctx context.Context, client graphql.Client, app string, include *bool, trigger snapshotTriggerFilter, limit, offset int) (snapshotGrantPageDTO, error) {
	ctx, err := i.context(ctx)
	if err != nil {
		return snapshotGrantPageDTO{}, err
	}
	d := snapshotGrantPageDTO{Items: []snapshotGrantDTO{}}
	p, err := i.fetchSnapshotPage(ctx, client, app, include, trigger, limit, offset)
	if err != nil {
		return d, api.MapErrorKeepingCode(err)
	}
	if p == nil {
		return d, exitcode.Newf(exitcode.Error, "the server returned no team snapshot grant page")
	}
	d.Total = p.total
	d.HasMore = p.hasMore
	for _, g := range p.items {
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
