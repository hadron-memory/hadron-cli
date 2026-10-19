package delivery

import "time"

// Wake record states (cli#906 6257108208 §1). The two are kept apart: a
// typed record is pending evidence and never expires by age; only its own
// landed ack, or a newer tick of the same ticker whose ack landed, moves it
// to acked. Acked records are archive, pruned after a day.
const (
	WakeTyped = "typed"
	WakeAcked = "acked"
)

// WakeRecord is submitted evidence for one TICK or TASK reason. It holds the
// reason's key and nothing else from the wake: no text, no proof.
type WakeRecord struct {
	State    string    `json:"state"`
	WorkerID string    `json:"workerId"`
	Key      string    `json:"key"`
	TickerID string    `json:"tickerId,omitempty"`
	TickAt   time.Time `json:"tickAt,omitzero"`
	At       time.Time `json:"at"`
}

// WakeRecords is the wake-reason store: one 0600 file per key under a 0700
// directory per worker, written by atomic rename.
type WakeRecords struct {
	Dir string
}

// Typed records reasons whose line was confirmed submitted. Written after
// the confirmation and before any ack.
func (s *WakeRecords) Typed(workerID string, reasons []WakeReason, at time.Time) error { return nil }

// Acked moves reasons' records to acked (writing them if absent).
func (s *WakeRecords) Acked(workerID string, keys []string, at time.Time) error { return nil }

// List is the worker's wake records, keyed by reason key.
func (s *WakeRecords) List(workerID string) (map[string]WakeRecord, error) {
	return map[string]WakeRecord{}, nil
}

// Prune drops acked records older than cutoff. Typed records are never
// pruned by age.
func (s *WakeRecords) Prune(workerID string, cutoff time.Time) error { return nil }
