package lintwarn

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// ReadTimeout and MaxRequests bound ALL the advisory reads of one command
// (cli#2007 §6). The budget starts when the main operation has succeeded and
// never constrains the main read or write; when it runs out, what is unfinished
// is unavailable, never wrong.
const (
	ReadTimeout = 5 * time.Second
	MaxRequests = 1000
)

// FirstPage is the page size every new lint operation asks for (cor:api:120:04:
// a client does not ask for more than 20 unless its user did); the cursor is
// followed to the end.
const FirstPage = 20

// Budget is the one advisory budget of a command. It is safe for concurrent use.
type Budget struct {
	ctx    context.Context
	cancel context.CancelFunc
	n      atomic.Int64
}

// NewBudget starts the budget now, as a child of parent (which carries the
// caller's principal and session header, if any).
func NewBudget(parent context.Context) *Budget {
	ctx, cancel := context.WithTimeout(parent, ReadTimeout)
	return &Budget{ctx: ctx, cancel: cancel}
}

// Ctx is the context for lint requests only.
func (b *Budget) Ctx() context.Context { return b.ctx }

// Close releases the budget's timer.
func (b *Budget) Close() { b.cancel() }

// Allow counts one request and reports whether it may go out.
func (b *Budget) Allow() bool {
	return b.ctx.Err() == nil && b.n.Add(1) <= MaxRequests
}

var errUnavailable = errors.New("lint page unavailable")

func fromFields(f *gen.LintWarningPageFields) *Raw {
	if f == nil {
		return nil
	}
	raw := &Raw{Total: f.Total, NextCursor: f.NextCursor}
	if f.Items != nil {
		raw.Items = make([]*Warning, len(f.Items))
		for i, v := range f.Items {
			if v != nil {
				raw.Items[i] = &Warning{Kind: string(v.Kind), Position: v.Position, Text: v.Text, TargetURN: v.TargetUrn, PinnedRevision: v.PinnedRevision, NewerRevision: v.NewerRevision, OverriddenNodeLoc: v.OverriddenNodeLoc, WorkerField: v.WorkerField}
			}
		}
	}
	return raw
}

// ReadSignatureLint drains Worker.signatureLint. The op's worker id must be the
// worker the command shows; a different id is unavailable, never another
// worker's warnings. Nil means unavailable.
func ReadSignatureLint(b *Budget, client graphql.Client, workerID string) *Page {
	return Drain(b.Ctx(), b.Allow, func(ctx context.Context, after *string) (*Raw, error) {
		r, err := gen.GetWorkerSignatureLint(ctx, client, workerID, after)
		if err != nil || r == nil || r.Worker == nil || r.Worker.Id != workerID || r.Worker.SignatureLint == nil {
			return nil, errUnavailable
		}
		return fromFields(&r.Worker.SignatureLint.LintWarningPageFields), nil
	})
}

// PromptState says why a prompt read has, or has no, page.
type PromptState string

const (
	// PromptOK: the page answered (it may hold zero warnings).
	PromptOK PromptState = "ok"
	// PromptNothingServed: the worker has no served record for an open session.
	PromptNothingServed PromptState = "nothing-served"
	// PromptUnavailable: a record is served but the lint could not be read.
	PromptUnavailable PromptState = "unavailable"
)

// PromptLint is the outcome of Worker.promptLintWarnings.
type PromptLint struct {
	State PromptState
	Page  *Page
	// ServedID and ProvenanceRevSeq name the record the warnings belong to
	// (never the Agent's newest compile).
	ServedID         string
	ProvenanceRevSeq int
}

// ReadPromptLint drains Worker.promptLintWarnings. Nothing-served is told from
// unavailable by servedPrompt { id } in the same operation, never by
// hasLiveSession: an idle but open session still has a record and warnings.
func ReadPromptLint(b *Budget, client graphql.Client, workerID string) PromptLint {
	out := PromptLint{State: PromptUnavailable}
	nothing := false
	var seen bool
	page := Drain(b.Ctx(), b.Allow, func(ctx context.Context, after *string) (*Raw, error) {
		r, err := gen.GetWorkerPromptLint(ctx, client, workerID, after)
		if err != nil || r == nil || r.Worker == nil || r.Worker.Id != workerID {
			return nil, errUnavailable
		}
		sp := r.Worker.ServedPrompt
		if sp == nil {
			if seen { // a record that was served vanished between pages
				return nil, errUnavailable
			}
			nothing = true
			return nil, errUnavailable
		}
		if !seen {
			seen, out.ServedID, out.ProvenanceRevSeq = true, sp.Id, sp.ProvenanceRevSeq
		} else if sp.Id != out.ServedID || sp.ProvenanceRevSeq != out.ProvenanceRevSeq {
			return nil, errUnavailable // a new bind replaced the open session
		}
		if r.Worker.PromptLintWarnings == nil {
			return nil, errUnavailable
		}
		return fromFields(&r.Worker.PromptLintWarnings.LintWarningPageFields), nil
	})
	switch {
	case page != nil:
		out.State, out.Page = PromptOK, page
	case nothing:
		out = PromptLint{State: PromptNothingServed}
	}
	return out
}

// RecordFetch reads one lint page of ONE served record.
type RecordFetch func(ctx context.Context, after *string) (recordID string, page *gen.LintWarningPageFields, err error)

// ReadRecordLint drains the lint of the single record the command shows
// (worker or run served-prompt). Every page must come from record wantID.
func ReadRecordLint(b *Budget, wantID string, fetch RecordFetch) *Page {
	return Drain(b.Ctx(), b.Allow, func(ctx context.Context, after *string) (*Raw, error) {
		id, p, err := fetch(ctx, after)
		if err != nil || id != wantID || p == nil {
			return nil, errUnavailable
		}
		return fromFields(p), nil
	})
}

// HistItem is one record of a history page with its inline first lint page.
type HistItem struct {
	ID   string
	Lint *gen.LintWarningPageFields
}

// HistFetch is one history read: the history total and its items.
type HistFetch func(ctx context.Context) (total int, items []HistItem, err error)

// HistFetchAt re-reads exactly one record by offset, after a lint cursor.
type HistFetchAt func(ctx context.Context, offset int, after *string) (total int, items []HistItem, err error)

// ReadHistoryLint returns, for each shown record (ids in display order, the
// page starting at shownOffset of shownTotal), its lint page or nil. Each
// record is whole or nil, never partial and never another record's: the inline
// first page counts only if item i has the shown id and the history total is
// unchanged; a record with more pages is drained one at a time by offset and
// every page must return that one record with the same lint and history total.
func ReadHistoryLint(b *Budget, shownIDs []string, shownOffset, shownTotal int, first HistFetch, at HistFetchAt) []*Page {
	pages := make([]*Page, len(shownIDs))
	if len(shownIDs) == 0 || !b.Allow() {
		return pages
	}
	total, items, err := first(b.Ctx())
	if err != nil || total != shownTotal {
		return pages
	}
	for i, id := range shownIDs {
		if i >= len(items) || items[i].ID != id || items[i].Lint == nil {
			continue
		}
		inline := items[i].Lint
		offset := shownOffset + i
		firstCall := true
		pages[i] = Drain(b.Ctx(), func() bool {
			if firstCall { // the inline page is already paid for
				firstCall = false
				return b.Ctx().Err() == nil
			}
			return b.Allow()
		}, func(ctx context.Context, after *string) (*Raw, error) {
			if after == nil {
				return fromFields(inline), nil
			}
			t, its, err := at(ctx, offset, after)
			if err != nil || t != shownTotal || len(its) != 1 || its[0].ID != id || its[0].Lint == nil {
				return nil, errUnavailable
			}
			return fromFields(its[0].Lint), nil
		})
	}
	return pages
}
