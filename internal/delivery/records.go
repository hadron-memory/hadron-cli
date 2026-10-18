package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// RecordConfirmed is the only state a record is ever written in. Nothing is
// persisted before the delivery test succeeds: a batch read, prepared or
// sent without confirmation has no record, and counts as not delivered.
const RecordConfirmed = "confirmed"

// Record is submitted evidence for one batch: written after a confirmed send
// and before its ack, deleted once the ack lands. It holds no text, no proof
// and no batch id.
type Record struct {
	State       string    `json:"state"`
	WorkerID    string    `json:"workerId"`
	BatchKey    string    `json:"batchKey,omitempty"`
	Ranges      []Range   `json:"ranges"`
	ConfirmedAt time.Time `json:"confirmedAt"`
}

// Records is the confirmed-delivery store: one 0600 file per batch under a
// 0700 directory per worker. Files are written by atomic rename and named by
// a hash of the ranges, so a writer that lost its lock never clobbers
// another deliverer's record.
type Records struct {
	Dir string
}

// recordName hashes what identifies a batch's messages: per channel, the id,
// the incarnation and the exact seqs.
func recordName(ranges []Range) string {
	h := sha256.New()
	for _, r := range ranges {
		fmt.Fprintf(h, "%s\x00%d\x00%v\x00", r.ChannelID, r.Incarnation, r.Seqs)
	}
	return hex.EncodeToString(h.Sum(nil))[:32] + ".json"
}

func (s *Records) workerDir(workerID string) (string, error) {
	if err := checkWorkerID(workerID); err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, workerID), nil
}

// Put records a confirmed delivery.
func (s *Records) Put(b Batch, at time.Time) error {
	dir, err := s.workerDir(b.WorkerID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	rec := Record{State: RecordConfirmed, WorkerID: b.WorkerID, BatchKey: b.BatchKey, Ranges: b.Ranges, ConfirmedAt: at.UTC()}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".record-*")
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
	return os.Rename(tmp.Name(), filepath.Join(dir, recordName(b.Ranges)))
}

// List is the worker's confirmed records. A file that does not parse as a
// confirmed record for this worker is ignored: only typed, confirmed
// evidence may suppress a send.
func (s *Records) List(workerID string) ([]Record, error) {
	dir, err := s.workerDir(workerID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Record{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var rec Record
		if json.Unmarshal(b, &rec) != nil || rec.State != RecordConfirmed || rec.WorkerID != workerID || recordName(rec.Ranges) != e.Name() {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// Delete removes a record once its ack has landed.
func (s *Records) Delete(workerID string, ranges []Range) error {
	dir, err := s.workerDir(workerID)
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, recordName(ranges)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Prune drops records confirmed before cutoff: past the proof's lifetime a
// record can only match a re-hand, and S1 re-hands within minutes.
func (s *Records) Prune(workerID string, cutoff time.Time) error {
	recs, err := s.List(workerID)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.ConfirmedAt.Before(cutoff) {
			if err := s.Delete(workerID, r.Ranges); err != nil {
				return err
			}
		}
	}
	return nil
}

// Cover is what confirmed records prove about a batch.
type Cover struct {
	// Full: every message of the batch was confirmed delivered. Ack it whole,
	// with no typing.
	Full bool
	// Through: the channels with a confirmed covered prefix, when not Full.
	// Channels left out are not covered and move nothing.
	Through []Through
	// Matched are the records that proved it.
	Matched []Record
}

// Reconcile is what confirmed records prove about b. A record applies to a
// channel only when the channel id AND the incarnation match: a recreated
// channel restarts its seqs on the same id (hadron-server src/lib/channels.ts
// at b8384631), so an earlier incarnation's record proves nothing. Within a
// channel the covered prefix is the batch's own seqs from its first, for as
// long as each is in one record's confirmed seqs, and that record's first
// seq is the batch's first. The first seq not in the record ends it, and a
// later distinct message is therefore never acked unseen. A batchKey match
// with a confirmed record is a full cover by S1's definition of the key.
func Reconcile(b Batch, recs []Record) Cover {
	if b.BatchKey != "" {
		for _, r := range recs {
			if r.BatchKey == b.BatchKey && sameMessages(r.Ranges, b.Ranges) {
				return Cover{Full: true, Matched: []Record{r}}
			}
		}
	}
	var c Cover
	full := true
	matched := map[string]Record{}
	for _, br := range b.Ranges {
		best, from := 0, Record{}
		for _, rec := range recs {
			for _, rr := range rec.Ranges {
				if rr.ChannelID != br.ChannelID || rr.Incarnation != br.Incarnation || len(rr.Seqs) == 0 || rr.Seqs[0] != br.Seqs[0] {
					continue
				}
				n := 0
				for n < len(br.Seqs) && slices.Contains(rr.Seqs, br.Seqs[n]) {
					n++
				}
				if n > best {
					best, from = n, rec
				}
			}
		}
		if best < len(br.Seqs) {
			full = false
		}
		if best > 0 {
			c.Through = append(c.Through, Through{ChannelID: br.ChannelID, Seq: br.Seqs[best-1]})
			matched[recordName(from.Ranges)] = from
		}
	}
	for _, r := range matched {
		c.Matched = append(c.Matched, r)
	}
	if full && len(c.Through) == len(b.Ranges) {
		return Cover{Full: true, Matched: c.Matched}
	}
	return c
}

// sameMessages: the same channels, incarnations and seqs.
func sameMessages(a, b []Range) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ChannelID != b[i].ChannelID || a[i].Incarnation != b[i].Incarnation || !slices.Equal(a[i].Seqs, b[i].Seqs) {
			return false
		}
	}
	return true
}
