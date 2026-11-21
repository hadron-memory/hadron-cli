package ticker

import (
	"context"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/spf13/cobra"
)

// managerInstructionMinimum is the server minimum the shared --instruction
// text states, the same as the app and team doors (cli#2448). It is display
// text only.
const managerInstructionMinimum = "2026-11-04"

// instructed is one ticker write's --instruction claim and the function that
// yields the context the write goes out in (cli#2773). The zero value is "no
// flag": the plain operation, the command's document and its headerless,
// as-the-person attribution all unchanged.
type instructed struct {
	claim *gen.ActInstructionInput
	send  func(context.Context) (context.Context, bool, error)
}

func addInstruction(cmd *cobra.Command) { instruction.AddManagerFlag(cmd, managerInstructionMinimum) }

// readInstruction parses --instruction before any prompt or request (a bad
// kind or a blank ref is exit 2 with nothing sent) and, with the flag,
// snapshots the worktree binding. A ticker or worker ref does not name its
// App, so the server resolves the target App and owns the admission; the
// binding is verified locally and a server refusal is never retried as the
// person or without the claim.
func readInstruction(cmd *cobra.Command, f *cmdutil.Factory) (instructed, error) {
	if !cmd.Flags().Changed("instruction") {
		return instructed{}, nil
	}
	raw, _ := cmd.Flags().GetString("instruction")
	claim, err := instruction.Parse(raw)
	if err != nil {
		return instructed{}, err
	}
	if f.BeginServerTargetInstructedWrite == nil {
		return instructed{}, exitcode.Newf(exitcode.Usage, "worker-session verification unavailable; nothing was sent")
	}
	return instructed{claim: claim, send: f.BeginServerTargetInstructedWrite(cmd.Context())}, nil
}

// context is the context the one write goes out in. Its error comes before
// anything is sent.
func (i instructed) context(ctx context.Context) (context.Context, error) {
	if i.claim == nil {
		return ctx, nil
	}
	out, _, err := i.send(ctx)
	return out, err
}

func (i instructed) updateTicker(ctx context.Context, c graphql.Client, ref string, p *gen.TickerPatch) (*gen.TickerFields, error) {
	if i.claim == nil {
		r, err := gen.UpdateTicker(ctx, c, ref, p)
		if err != nil || r == nil || r.UpdateTicker == nil {
			return nil, err
		}
		return &r.UpdateTicker.TickerFields, nil
	}
	r, err := gen.UpdateTickerInstructed(ctx, c, ref, p, i.claim)
	if err != nil || r == nil || r.UpdateTicker == nil {
		return nil, err
	}
	return &r.UpdateTicker.TickerFields, nil
}

func (i instructed) deleteTicker(ctx context.Context, c graphql.Client, ref string) (bool, error) {
	if i.claim == nil {
		r, err := gen.DeleteTicker(ctx, c, ref)
		if r == nil {
			return false, err
		}
		return r.DeleteTicker, err
	}
	r, err := gen.DeleteTickerInstructed(ctx, c, ref, i.claim)
	if r == nil {
		return false, err
	}
	return r.DeleteTicker, err
}

// subscription is the answer to a subscribe or unsubscribe, whichever
// operation carried it.
type subscription struct {
	fields       *gen.TickerSubscriptionFields
	announcement *gen.GrantAnnouncement
}

func (i instructed) subscribe(ctx context.Context, c graphql.Client, ticker, worker string, unsubscribe bool) (subscription, error) {
	var out subscription
	switch {
	case i.claim == nil && unsubscribe:
		r, err := gen.UnsubscribeTicker(ctx, c, ticker, worker)
		if err != nil || r == nil || r.UnsubscribeWorker == nil {
			return out, err
		}
		out.announcement = r.UnsubscribeWorker.Announcement
		if s := r.UnsubscribeWorker.Subscription; s != nil {
			out.fields = &s.TickerSubscriptionFields
		}
	case i.claim == nil:
		r, err := gen.SubscribeTicker(ctx, c, ticker, worker)
		if err != nil || r == nil || r.SubscribeWorker == nil {
			return out, err
		}
		out.announcement = r.SubscribeWorker.Announcement
		if s := r.SubscribeWorker.Subscription; s != nil {
			out.fields = &s.TickerSubscriptionFields
		}
	case unsubscribe:
		r, err := gen.UnsubscribeTickerInstructed(ctx, c, ticker, worker, i.claim)
		if err != nil || r == nil || r.UnsubscribeWorker == nil {
			return out, err
		}
		out.announcement = r.UnsubscribeWorker.Announcement
		if s := r.UnsubscribeWorker.Subscription; s != nil {
			out.fields = &s.TickerSubscriptionFields
		}
	default:
		r, err := gen.SubscribeTickerInstructed(ctx, c, ticker, worker, i.claim)
		if err != nil || r == nil || r.SubscribeWorker == nil {
			return out, err
		}
		out.announcement = r.SubscribeWorker.Announcement
		if s := r.SubscribeWorker.Subscription; s != nil {
			out.fields = &s.TickerSubscriptionFields
		}
	}
	return out, nil
}

// applied is the answer to apply-templates, whichever operation carried it.
type applied struct {
	present        bool
	created        []appliedRow
	alreadyPresent []appliedTemplateDTO
}

type appliedRow struct {
	ticker   gen.TickerFields
	template *appliedTemplateDTO
	cadence  int
}

func (i instructed) applyTemplates(ctx context.Context, c graphql.Client, worker string) (applied, error) {
	var out applied
	if i.claim == nil {
		r, err := gen.ApplyAgentTickerTemplates(ctx, c, worker)
		if err != nil || r == nil || r.ApplyAgentTickerTemplates == nil {
			return out, err
		}
		out.present = true
		for _, x := range r.ApplyAgentTickerTemplates.Created {
			if x == nil || x.Ticker == nil {
				continue
			}
			row := appliedRow{ticker: x.Ticker.TickerFields, cadence: x.EffectiveCadenceSeconds}
			if x.Template != nil {
				row.template = &appliedTemplateDTO{x.Template.Id, x.Template.Name}
			}
			out.created = append(out.created, row)
		}
		for _, x := range r.ApplyAgentTickerTemplates.AlreadyPresent {
			if x != nil {
				out.alreadyPresent = append(out.alreadyPresent, appliedTemplateDTO{x.Id, x.Name})
			}
		}
		return out, nil
	}
	r, err := gen.ApplyAgentTickerTemplatesInstructed(ctx, c, worker, i.claim)
	if err != nil || r == nil || r.ApplyAgentTickerTemplates == nil {
		return out, err
	}
	out.present = true
	for _, x := range r.ApplyAgentTickerTemplates.Created {
		if x == nil || x.Ticker == nil {
			continue
		}
		row := appliedRow{ticker: x.Ticker.TickerFields, cadence: x.EffectiveCadenceSeconds}
		if x.Template != nil {
			row.template = &appliedTemplateDTO{x.Template.Id, x.Template.Name}
		}
		out.created = append(out.created, row)
	}
	for _, x := range r.ApplyAgentTickerTemplates.AlreadyPresent {
		if x != nil {
			out.alreadyPresent = append(out.alreadyPresent, appliedTemplateDTO{x.Id, x.Name})
		}
	}
	return out, nil
}
