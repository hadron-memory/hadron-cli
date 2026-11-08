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
	"runtime"
	"strings"
	"time"
)

// StopDrop is the outbox record of a halt notice the poller could not type
// (hadron-cli#1629; Holger's option A on hadron-server#2169: a dropped
// interrupt is logged and flagged to the coordinator). Ids and codes only:
// never screen, draft or proof text. The poller writes it once per notice;
// the host's reporter reads it, posts the flag, and writes its own
// <name>.reported marker beside it, which the poller never touches.
type StopDrop struct {
	EventID    string    `json:"eventId"`
	SignalID   string    `json:"signalId"`
	WorkerID   string    `json:"workerId"`
	Worker     string    `json:"worker"`
	Level      string    `json:"level"`
	HoldReason string    `json:"holdReason"`
	HeldAt     time.Time `json:"heldAt"`
	// TypedAt is set once the same notice is later typed and acknowledged,
	// so a report can say it has since landed. Zero while it is still due.
	TypedAt time.Time `json:"typedAt,omitzero"`
}

// StopOutbox is the dropped-halt outbox: one 0600 file per notice under a
// 0700 directory, created at most once (link, which fails if the name
// exists), so a notice held on every poll, or across a poller restart, is
// recorded once.
type StopOutbox struct {
	Dir string
}

func stopDropName(eventID string) string {
	h := sha256.Sum256([]byte(eventID))
	return hex.EncodeToString(h[:])[:32] + ".json"
}

func (o *StopOutbox) write(name string, rec StopDrop, replace bool) (bool, error) {
	data, err := json.Marshal(rec)
	if err != nil {
		return false, err
	}
	return o.writeFile(name, data, replace)
}

// writeFile writes data under name durably: a synced 0600 temporary, then a
// rename (replace) or a link that fails if the name exists (create once),
// then a sync of the directory, so the name survives a crash once this
// returns. created is false only when replace is false and the name existed.
func (o *StopOutbox) writeFile(name string, data []byte, replace bool) (created bool, err error) {
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(o.Dir, ".stop-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	dst := filepath.Join(o.Dir, name)
	if replace {
		if err := os.Rename(tmp.Name(), dst); err != nil {
			return false, err
		}
	} else if err := os.Link(tmp.Name(), dst); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, syncDir(o.Dir)
}

// syncDir makes a new or renamed name in dir durable. Windows cannot open a
// directory for sync; there it is a no-op.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// StopOutboxDir is the outbox under a host's delivery state directory: the
// one place the poller (New) and the reporter (report-stops) both take it.
func StopOutboxDir(stateDir string) string {
	return filepath.Join(stateDir, "delivery", "stop-dropped")
}

// Drop records rec unless its notice is already recorded. created is true
// only for the first record of that notice.
func (o *StopOutbox) Drop(rec StopDrop) (created bool, err error) {
	if rec.EventID == "" {
		return false, errors.New("a stop drop without its notice event")
	}
	return o.write(stopDropName(rec.EventID), rec, false)
}

// Typed stamps a recorded notice as since typed and acknowledged. A notice
// with no record is left without one: it was never dropped.
func (o *StopOutbox) Typed(eventID string, at time.Time) error {
	name := stopDropName(eventID)
	data, err := os.ReadFile(filepath.Join(o.Dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var rec StopDrop
	if err := json.Unmarshal(data, &rec); err != nil {
		return err
	}
	if !rec.TypedAt.IsZero() {
		return nil
	}
	rec.TypedAt = at.UTC()
	_, err = o.write(name, rec, true)
	return err
}

// namedDrop is a record with its file's base name (the hashed notice id),
// which its .sending and .reported siblings share.
type namedDrop struct {
	base string
	rec  StopDrop
}

// List is every recorded notice, for the reporter and status. Markers and
// temporaries are skipped; an unreadable record is an error, never skipped.
func (o *StopOutbox) List() ([]StopDrop, error) {
	named, corrupt, err := o.listNamed()
	if err == nil && len(corrupt) > 0 {
		err = fmt.Errorf("unreadable record %s: %s", corrupt[0].file, corrupt[0].cause)
	}
	if err != nil {
		return nil, err
	}
	out := make([]StopDrop, 0, len(named))
	for _, n := range named {
		out = append(out, n.rec)
	}
	return out, nil
}

// corruptDrop is a record file that could not be read or parsed (#1660). It
// is named, never skipped silently, and never counts as reported: the notice
// it stood for stays unreported until the file is readable again or removed.
type corruptDrop struct {
	file  string
	cause string
}

// listNamed reads every record. A record that cannot be read or parsed does
// not fail the listing for the others: it is returned in corrupt, by file
// name. Only a failure to list the directory itself is an error.
func (o *StopOutbox) listNamed() (named []namedDrop, corrupt []corruptDrop, err error) {
	entries, err := os.ReadDir(o.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []namedDrop{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	named = []namedDrop{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(o.Dir, e.Name()))
		if err != nil {
			corrupt = append(corrupt, corruptDrop{file: e.Name(), cause: "cannot be read"})
			continue
		}
		var rec StopDrop
		if err := json.Unmarshal(data, &rec); err != nil {
			corrupt = append(corrupt, corruptDrop{file: e.Name(), cause: "is not a valid record"})
			continue
		}
		named = append(named, namedDrop{base: strings.TrimSuffix(e.Name(), ".json"), rec: rec})
	}
	return named, corrupt, nil
}
