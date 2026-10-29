package orca

import (
	"context"
	"errors"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

// Worker is one casting of the App, as the command needs it.
type Worker struct {
	ID   string
	Name string
}

// Reads is every server read the command makes. All of them are reads: the
// command never acknowledges a wake and writes nothing.
type Reads interface {
	// Workers is the App's active (not retired) workers.
	Workers(ctx context.Context) ([]Worker, error)
	// Subscriptions is each worker's actively subscribed, enabled tickers,
	// by worker id.
	Subscriptions(ctx context.Context) (map[string][]terminalstatus.Ticker, error)
	// DueTicks is the oldest unanswered tick of every ticker the named
	// workers are owed, by worker id. A refusal is an error: the caller
	// fails closed on it.
	DueTicks(ctx context.Context, workerIDs []string) (map[string][]terminalstatus.DueTick, error)
}

const (
	pageSize = 200
	// maxPages bounds every paged read: a cursor that never ends is a
	// refusal, not a loop.
	maxPages = 100
)

var errCursor = errors.New("paging cursor did not advance")

type graphReads struct {
	client graphql.Client
	app    string
}

func (g graphReads) Workers(ctx context.Context) ([]Worker, error) {
	retired := false
	limit := pageSize
	out := []Worker{}
	for offset := 0; ; {
		off := offset
		resp, err := gen.Workers(ctx, g.client, g.app, &retired, &limit, &off)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.Workers == nil {
			return out, nil
		}
		for _, w := range resp.Workers.Items {
			if w != nil && w.RetiredAt == nil {
				out = append(out, Worker{ID: w.Id, Name: w.Name})
			}
		}
		offset += len(resp.Workers.Items)
		if len(resp.Workers.Items) < pageSize || offset >= resp.Workers.Total {
			return out, nil
		}
	}
}

func (g graphReads) Subscriptions(ctx context.Context) (map[string][]terminalstatus.Ticker, error) {
	resp, err := gen.ListTickers(ctx, g.client, g.app)
	if err != nil {
		return nil, api.MapError(err)
	}
	out := map[string][]terminalstatus.Ticker{}
	for _, t := range resp.Tickers {
		if t == nil || !t.Enabled || t.CadenceSeconds <= 0 {
			continue
		}
		tk := terminalstatus.Ticker{ID: t.Id, Name: t.Name, Interval: time.Duration(t.CadenceSeconds) * time.Second}
		var after *string
		seen := map[string]bool{}
		for page := 0; ; page++ {
			sub, err := gen.TickerSubscribers(ctx, g.client, t.Id, pageSize, after)
			if err != nil {
				return nil, api.MapError(err)
			}
			// A missing connection is unreadable evidence, not "no subscribers".
			if sub.Ticker == nil || sub.Ticker.Subscribers == nil {
				return nil, errors.New("subscriptions unreadable for ticker " + t.Id)
			}
			s := sub.Ticker.Subscribers
			for _, it := range s.Items {
				if it != nil && it.EndedAt == nil && it.Worker != nil {
					out[it.Worker.Id] = append(out[it.Worker.Id], tk)
				}
			}
			if !s.HasNextPage {
				break
			}
			if s.EndCursor == nil || *s.EndCursor == "" || seen[*s.EndCursor] || page+1 >= maxPages {
				return nil, errCursor
			}
			seen[*s.EndCursor] = true
			after = s.EndCursor
		}
	}
	return out, nil
}

func (g graphReads) DueTicks(ctx context.Context, workerIDs []string) (map[string][]terminalstatus.DueTick, error) {
	out := map[string][]terminalstatus.DueTick{}
	if len(workerIDs) == 0 {
		return out, nil
	}
	var after *string
	seen := map[string]bool{}
	for n := 0; ; n++ {
		resp, err := gen.DueTickWakes(ctx, g.client, g.app, pageSize, workerIDs, after)
		if err != nil {
			return nil, api.MapError(err)
		}
		page := resp.DueWakes
		if page == nil || page.PageInfo == nil {
			return nil, errors.New("due wakes: no page")
		}
		if page.Status != gen.DueWakeStatusOk {
			// WAKE_BUDGET_EXHAUSTED withholds due wakes: an empty page then
			// proves nothing, so it must not read as "nothing due".
			return nil, errors.New("due wakes withheld: " + string(page.Status))
		}
		for _, w := range page.Wakes {
			if w == nil || w.Worker == nil {
				continue
			}
			due, err := time.Parse(time.RFC3339, w.AnchorAt)
			if err != nil {
				return nil, errors.New("due wakes: bad anchorAt")
			}
			// anchorAt is the earliest waiting reason of ANY kind. It is a tick's
			// age only when the wake holds exactly one reason and it is a tick;
			// beside a mention, or beside another ticker's tick, it would age
			// the tick, so the ticker's own tick time stands in: the youngest
			// bound, which can never raise a false dark. A tick without that
			// time has lost its age evidence, which is an error, not "idle".
			tickOnly := len(w.Reasons) == 1 && w.Reasons[0] != nil && w.Reasons[0].Kind == gen.WakeKindTick
			for _, r := range w.Reasons {
				if r == nil || r.Kind != gen.WakeKindTick || r.Ticker == nil {
					continue
				}
				at := due
				if !tickOnly {
					if r.Ticker.LastTickAt == nil {
						return nil, errors.New("due wakes: merged tick without lastTickAt")
					}
					if at, err = time.Parse(time.RFC3339, *r.Ticker.LastTickAt); err != nil {
						return nil, errors.New("due wakes: bad lastTickAt")
					}
				}
				out[w.Worker.Id] = append(out[w.Worker.Id], terminalstatus.DueTick{TickerID: r.Ticker.Id, TickerName: r.Ticker.Name, DueAt: at})
			}
		}
		if !page.PageInfo.HasNextPage {
			return out, nil
		}
		next := page.PageInfo.EndCursor
		if next == nil || *next == "" || seen[*next] || n+1 >= maxPages {
			return nil, errCursor
		}
		seen[*next] = true
		after = next
	}
}
