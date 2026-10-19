package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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

func wakeRecordName(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])[:32] + ".json"
}

func (s *WakeRecords) put(rec WakeRecord) error {
	if err := checkWorkerID(rec.WorkerID); err != nil {
		return err
	}
	dir := filepath.Join(s.Dir, rec.WorkerID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wake-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, wakeRecordName(rec.Key)))
}

// Typed records reasons whose line was confirmed submitted. Written after
// the confirmation and before any ack.
func (s *WakeRecords) Typed(workerID string, reasons []WakeReason, at time.Time) error {
	for _, r := range reasons {
		rec := WakeRecord{State: WakeTyped, WorkerID: workerID, Key: r.Key(), At: at.UTC()}
		if rec.Key == "" {
			continue // a mention: never recorded
		}
		if r.Kind == WakeTick {
			rec.TickerID, rec.TickAt = r.TickerID, r.LastTickAt.UTC()
		}
		if err := s.put(rec); err != nil {
			return err
		}
	}
	return nil
}

// Acked moves records to acked. A key with no record is skipped: only
// typed evidence is ever archived.
func (s *WakeRecords) Acked(workerID string, keys []string, at time.Time) error {
	recs, err := s.List(workerID)
	if err != nil {
		return err
	}
	for _, k := range keys {
		rec, ok := recs[k]
		if !ok || rec.State == WakeAcked {
			continue
		}
		rec.State, rec.At = WakeAcked, at.UTC()
		if err := s.put(rec); err != nil {
			return err
		}
	}
	return nil
}

// List is the worker's wake records, keyed by reason key. A file that does
// not parse as this worker's record under its own name is ignored.
func (s *WakeRecords) List(workerID string) (map[string]WakeRecord, error) {
	if err := checkWorkerID(workerID); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.Dir, workerID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]WakeRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]WakeRecord{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 — our own state directory
		if err != nil {
			continue
		}
		var rec WakeRecord
		if json.Unmarshal(b, &rec) != nil || rec.WorkerID != workerID || rec.Key == "" || wakeRecordName(rec.Key) != e.Name() ||
			(rec.State != WakeTyped && rec.State != WakeAcked) {
			continue
		}
		out[rec.Key] = rec
	}
	return out, nil
}

// Prune drops acked records older than cutoff. Typed records are never
// pruned by age.
func (s *WakeRecords) Prune(workerID string, cutoff time.Time) error {
	recs, err := s.List(workerID)
	if err != nil {
		return err
	}
	for k, rec := range recs {
		if rec.State == WakeAcked && rec.At.Before(cutoff) {
			err := os.Remove(filepath.Join(s.Dir, workerID, wakeRecordName(k)))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
