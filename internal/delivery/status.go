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
}

// Status is the poller's local delivery snapshot. It is observational: what
// this host's poller last saw, never authoritative delivery health (that is
// S5's). It holds no text, proof, draft or batch id.
type Status struct {
	Version    int                     `json:"version"`
	LastStepAt time.Time               `json:"lastStepAt"`
	LastRead   string                  `json:"lastRead"`
	Workers    map[string]WorkerStatus `json:"workers"`
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
	if old, ok := b.st.Workers[e.WorkerID]; ok && e.Time.Before(old.At) {
		return
	}
	b.st.Workers[e.WorkerID] = WorkerStatus{Name: e.Worker, Kind: e.Kind, Reason: e.Reason, At: e.Time, ResolvedBy: e.ResolvedBy}
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
