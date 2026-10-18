package delivery

// RED: signatures only; the implementation follows in the next commit.

import (
	"context"
	"errors"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

const AckTimeout = 30 * time.Second

const DefaultBudget = time.Duration(0)

var ErrBusy = errors.New("busy")

type Terminals interface {
	List(ctx context.Context) ([]orcaterm.Terminal, error)
	ReadScreen(ctx context.Context, handle string) *orcaterm.Screen
	Send(ctx context.Context, handle, text string, o orcaterm.SendOptions) orcaterm.Receipt
}

type Recipient struct {
	WorkerID  string
	Name      string
	TUI       string
	Worktrees []string
}

type Capabilities struct {
	EnterOnly map[string]bool
	Clear     map[string]string
}

type Kind string

const (
	Delivered  Kind = "delivered"
	Reconciled Kind = "reconciled"
	Held       Kind = "held"
	Deferred   Kind = "deferred"
	Skipped    Kind = "skipped"
	Failed     Kind = "error"
)

type Outcome struct {
	Kind       Kind
	Reason     string
	Terminal   string
	AckPending bool
}

type Event struct {
	Time     time.Time
	Worker   string
	Span     string
	Kind     Kind
	Reason   string
	Terminal string
}

type Deliverer struct {
	Server  Server
	Orca    Terminals
	Records *Records
	LockDir string
	Caps    Capabilities
	Bound   func(worktree string) string
	Now     func() time.Time
	Budget  time.Duration
	Emit    func(Event)
}

func New(server Server, orca Terminals, stateDir string) *Deliverer {
	return &Deliverer{Server: server, Orca: orca, Records: &Records{Dir: stateDir}}
}

func (d *Deliverer) Deliver(ctx context.Context, b Batch, r Recipient) Outcome { return Outcome{} }

func WithWorkerLock(dir, workerID string, fn func(stillOwner func() bool) error) error {
	return fn(func() bool { return true })
}

const RecordConfirmed = "confirmed"

type Record struct {
	State       string
	WorkerID    string
	BatchKey    string
	Ranges      []Range
	ConfirmedAt time.Time
}

type Records struct{ Dir string }

func recordName(ranges []Range) string { return "x.json" }

func (s *Records) Put(b Batch, at time.Time) error               { return nil }
func (s *Records) List(workerID string) ([]Record, error)        { return []Record{}, nil }
func (s *Records) Delete(workerID string, ranges []Range) error  { return nil }
func (s *Records) Prune(workerID string, cutoff time.Time) error { return nil }

type Cover struct {
	Full    bool
	Through []Through
	Matched []Record
}

func Reconcile(b Batch, recs []Record) Cover { return Cover{} }

const bindingFileName = "hadron-team-session.json"

func BoundWorker(worktree string) string { return "" }
