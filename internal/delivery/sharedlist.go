package delivery

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// SharedList is a Terminals whose List is shared by every delivery within a
// short window, so a poll cycle's cost is one `orca terminal list` however
// many workers it decides for (#2181). The list only finds a worker's
// terminal; the composer, the question, the idle proof and the binding are
// all judged from a screen read afterwards, so a list a few seconds old
// cannot make a send safe that is not.
//
// Never shared: a failed list (the next caller asks again), and any list
// older than the window or older than a Send (a send changes a terminal, so
// the next delivery looks fresh).
type SharedList struct {
	Terminals
	ttl time.Duration
	now func() time.Time

	mu    sync.Mutex
	terms []orcaterm.Terminal
	at    time.Time
	have  bool
}

// NewSharedList wraps inner so one List answers every caller within ttl.
func NewSharedList(inner Terminals, ttl time.Duration, now func() time.Time) *SharedList {
	if now == nil {
		now = time.Now
	}
	return &SharedList{Terminals: inner, ttl: ttl, now: now}
}

// List is the shared list: the one call a window needs, made under the lock
// so concurrent deliveries wait for it instead of each making their own.
func (s *SharedList) List(ctx context.Context) ([]orcaterm.Terminal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.have && s.now().Sub(s.at) < s.ttl {
		return slices.Clone(s.terms), nil
	}
	s.have = false
	terms, err := s.Terminals.List(ctx)
	if err != nil {
		return nil, err
	}
	s.terms, s.at, s.have = slices.Clone(terms), s.now(), true
	return terms, nil
}

// Send types through inner and ends the shared list: what follows looks fresh.
func (s *SharedList) Send(ctx context.Context, handle, text string, o orcaterm.SendOptions) orcaterm.Receipt {
	rc := s.Terminals.Send(ctx, handle, text, o)
	s.mu.Lock()
	s.have = false
	s.mu.Unlock()
	return rc
}
