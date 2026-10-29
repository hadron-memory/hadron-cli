package delivery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// How a poller step's batch read went (cli#906): the poller reports one per
// step, so `team host status` can tell a quiet poller from a stopped one.
const (
	StepRead       = "read"
	StepReadFailed = "read-failed"
	StepOffByScope = "off-by-scope"
	// WakeBudgetExhausted: the wake read answered WAKE_BUDGET_EXHAUSTED.
	WakeBudgetExhausted = "budget-exhausted"
)

// knownCodes is the delivery diagnostic vocabulary: the only codes a
// delivery error may carry into a log, status line or receipt. S1's and the
// auth refusals (server main ad7f1c2), and the adapter's fixed classes and
// own failures. A code is trusted by membership, never by its shape: a
// code-shaped token can still be private text (Rodolfo's §10 P2 on #939;
// Xan #24626).
var knownCodes = map[string]bool{
	"APP_KEY_SCOPE_REQUIRED": true, "HOST_KEY_REQUIRED": true, "APP_NOT_FOUND": true,
	"APP_KEY_SESSION_BOUND": true, "BAD_USER_INPUT": true, "WAKE_PROOF_STALE": true,
	"WAKE_PROOF_INVALID": true, "WAKE_PROOF_REQUESTER_MISMATCH": true,
	"FORBIDDEN": true, "UNAUTHENTICATED": true,
	"server-refused": true, "unauthenticated": true, "forbidden": true,
	"unavailable": true, "transport-error": true,
	"no-page": true, "no-cursor": true, "bad-deliver-after": true,
	"bad-busy-deadline": true, "no-result": true, "bad-count": true,
}

// KnownCode reports whether code is in the delivery diagnostic vocabulary.
func KnownCode(code string) bool { return knownCodes[code] }

// WakeRead is how a step's wake read went (cli#906 row 1): its code and
// the counts dueWakes reports for status. Counts only.
type WakeRead struct {
	At       time.Time
	Read     string
	Dormant  int
	Withheld int
}

// WorkerStatus is a worker's newest delivery event: codes only.
type WorkerStatus struct {
	Name       string    `json:"name"`
	Kind       Kind      `json:"kind"`
	Reason     string    `json:"reason,omitempty"`
	At         time.Time `json:"at"`
	ResolvedBy string    `json:"resolvedBy,omitempty"`
	// Wake: the event was a tick or task wake's, not a batch's.
	Wake bool `json:"wake,omitempty"`
	// Unreported: the newest event was a hold the server was not told about.
	Unreported bool `json:"unreported,omitempty"`
	// HeldSince is when the worker's current run of holds began: set by its
	// first hold, kept through later holds, deferrals, skips and errors, and
	// cleared only by a delivered or reconciled event (cli#1178). Nil when
	// no hold is outstanding.
	HeldSince *time.Time `json:"heldSince,omitempty"`
}

// Status is the poller's local delivery snapshot. It is observational: what
// this host's poller last saw, never authoritative delivery health (that is
// S5's). It holds no text, proof, draft or batch id.
type Status struct {
	Version    int                     `json:"version"`
	LastStepAt time.Time               `json:"lastStepAt"`
	LastRead   string                  `json:"lastRead"`
	Workers    map[string]WorkerStatus `json:"workers"`
	// The newest wake read (cli#906 row 1): its code, and dueWakes' counts.
	WakeRead          string     `json:"wakeRead,omitempty"`
	BudgetExhaustedAt *time.Time `json:"budgetExhaustedAt,omitempty"`
	Dormant           int        `json:"dormant"`
	Withheld          int        `json:"withheld"`
}

// ObserveWakes records a step's wake read. It writes nothing: Step does.
// The budget time is kept only while the newest read says it is spent.
func (b *StatusBook) ObserveWakes(r WakeRead) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.st.WakeRead, b.st.Dormant, b.st.Withheld = r.Read, r.Dormant, r.Withheld
	b.st.BudgetExhaustedAt = nil
	if r.Read == WakeBudgetExhausted {
		at := r.At.UTC()
		b.st.BudgetExhaustedAt = &at
	}
}

// StatusBook keeps the snapshot as events arrive and writes it after each
// poller step. Safe for concurrent use: deliveries for different workers
// run at once.
type StatusBook struct {
	dir string
	mu  sync.Mutex
	st  Status
}

// NewStatusBook keeps the snapshot under stateDir, beside the records and
// locks.
func NewStatusBook(stateDir string) *StatusBook {
	return &StatusBook{dir: stateDir, st: Status{Version: 1, Workers: map[string]WorkerStatus{}}}
}

// Observe records e as its worker's newest event, unless an event already
// recorded is newer. It writes nothing: Step does.
func (b *StatusBook) Observe(e Event) {
	if e.WorkerID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	old, ok := b.st.Workers[e.WorkerID]
	if ok && e.Time.Before(old.At) {
		return
	}
	held := old.HeldSince
	switch e.Kind {
	case Delivered, Reconciled:
		held = nil
	case Held:
		if held == nil {
			at := e.Time.UTC()
			held = &at
		}
	}
	b.st.Workers[e.WorkerID] = WorkerStatus{Name: e.Worker, Kind: e.Kind, Reason: e.Reason, At: e.Time, ResolvedBy: e.ResolvedBy, Wake: e.Wake, Unreported: e.Unreported, HeldSince: held}
}

// Step records a finished poller step and writes the snapshot: 0600, by
// atomic rename.
func (b *StatusBook) Step(at time.Time, read string) error {
	b.mu.Lock()
	b.st.LastStepAt, b.st.LastRead = at.UTC(), read
	data, err := json.Marshal(b.st)
	b.mu.Unlock()
	if err != nil {
		return err
	}
	dir := filepath.Join(b.dir, "delivery")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".status-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), statusPath(b.dir))
}

// ReadStatus reads the snapshot under stateDir. A missing file is an
// fs.ErrNotExist error; a corrupt one is any other error.
func ReadStatus(stateDir string) (Status, error) {
	var st Status
	data, err := os.ReadFile(statusPath(stateDir)) // #nosec G304 — beside the host config's own state file
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("delivery status is corrupt: %w", err)
	}
	if st.Workers == nil {
		st.Workers = map[string]WorkerStatus{}
	}
	return st, nil
}

func statusPath(stateDir string) string { return filepath.Join(stateDir, "delivery", "status.json") }
