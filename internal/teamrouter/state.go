package teamrouter

// The router's private, host-local state: the adopted attention token, the
// delivery ledger keyed on it, and the #1826 liveness clocks. The on-disk
// shapes are router.ts's, byte-compatible, so an existing state directory
// carries over unchanged (cli#829: token/ledger carried).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LedgerNudge records a delivered (or dropped-as-read) nudge's mention frontier.
type LedgerNudge struct {
	// RelevantSeq is the worker's last RELEVANT (mention) seq the nudge was about.
	RelevantSeq int    `json:"relevantSeq"`
	At          int64  `json:"at"`
	Stages      string `json:"stages"`
}

// Ledger is the durable delivery record for the adopted token: which workers
// were already nudged for the attention reported since that token, so a poll
// that must hold the token does not nudge them again.
type Ledger struct {
	Base   string                 `json:"base"`
	Nudged map[string]LedgerNudge `json:"nudged"`
	// Reported is each held worker's last LOGGED reason (by name), so a hold
	// is logged once per token, not once per poll.
	Reported map[string]string `json:"reported"`
	// DraftNotes: stale-draft discards already logged under this token.
	DraftNotes map[string]string `json:"draftNotes"`
	// HeldSince: epoch ms a worker's current hold began (cleared on delivery).
	HeldSince map[string]int64 `json:"heldSince"`
	// HeldLoggedAt: epoch ms the last long-hold line was logged per worker.
	HeldLoggedAt map[string]int64 `json:"heldLoggedAt"`
}

func emptyLedger(base string) Ledger {
	return Ledger{Base: base, Nudged: map[string]LedgerNudge{}, Reported: map[string]string{},
		DraftNotes: map[string]string{}, HeldSince: map[string]int64{}, HeldLoggedAt: map[string]int64{}}
}

// LivenessRecord is one flagged worker's liveness clock (#1826). Deliberately
// a SEPARATE file from the ledger: adoption deletes the ledger, but an idle
// episode must survive token adoption and router restarts untouched.
type LivenessRecord struct {
	// IdleSince is the epoch ms the continuous idle episode began; nil while reset.
	IdleSince *int64 `json:"idleSince,omitempty"`
	// Handle is the terminal the clock is measured on; a change is a replaced
	// terminal, which resets the clock.
	Handle string `json:"handle,omitempty"`
	// CoveredFrontier is the channel head at the last DELIVERED liveness
	// nudge; re-eligibility needs head > this.
	CoveredFrontier *int `json:"coveredFrontier,omitempty"`
}

// seqMax is the 32-bit seq column ceiling: a "position" above it is
// corruption, not a place in the chat (#1829 local P1-1/P2-2).
const seqMax = 2_147_483_647

// State is the token/ledger/liveness persistence (dir 0700, files 0600).
type State struct{ dir string }

// NewState addresses the state directory dir; nothing is touched until used.
func NewState(dir string) *State { return &State{dir: dir} }

func (s *State) tokenPath() string    { return filepath.Join(s.dir, "adopted.token") }
func (s *State) ledgerPath() string   { return filepath.Join(s.dir, "ledger.json") }
func (s *State) livenessPath() string { return filepath.Join(s.dir, "liveness.json") }
func (s *State) notesPath() string    { return filepath.Join(s.dir, "liveness-notes.json") }

// ensure creates the private state directory (0700, re-asserted every run).
func (s *State) ensure() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(s.dir, 0o700)
}

// token is the adopted token, or "" before the first adoption.
func (s *State) token() (string, error) {
	b, err := os.ReadFile(s.tokenPath())
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Token is the adopted token, or "" when none is adopted or it is unreadable.
func (s *State) Token() string {
	t, _ := s.token()
	return t
}

// base is a fingerprint of the adopted token — never the token itself.
func (s *State) base() string {
	sum := sha256.Sum256([]byte(s.Token()))
	return hex.EncodeToString(sum[:])
}

// Ledger is the ledger for the current token; a stale (other token),
// unreadable or malformed one reads as empty.
func (s *State) Ledger() Ledger {
	base := s.base()
	var l Ledger
	b, err := os.ReadFile(s.ledgerPath())
	if err != nil || json.Unmarshal(b, &l) != nil || l.Base != base {
		return emptyLedger(base)
	}
	fresh := emptyLedger(base)
	if l.Nudged == nil {
		l.Nudged = fresh.Nudged
	}
	if l.Reported == nil {
		l.Reported = fresh.Reported
	}
	if l.DraftNotes == nil {
		l.DraftNotes = fresh.DraftNotes
	}
	if l.HeldSince == nil {
		l.HeldSince = fresh.HeldSince
	}
	if l.HeldLoggedAt == nil {
		l.HeldLoggedAt = fresh.HeldLoggedAt
	}
	return l
}

// SaveLedger persists l atomically (0600).
func (s *State) SaveLedger(l Ledger) error {
	return writeJSON(s.ledgerPath(), l)
}

// adopt adopts a token and clears the ledger — only after every delivery
// succeeded. Never touches liveness.json.
func (s *State) adopt(token string) error {
	if err := atomicWrite(s.tokenPath(), []byte(token)); err != nil {
		return err
	}
	if err := os.Remove(s.ledgerPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Liveness is the liveness clocks, SANITIZED on read so corruption can never
// fail open (#1829 local P2-2): a non-object file reads as fresh, a
// non-object record as reset, and only well-typed fields survive — a
// non-negative integer idleSince, a non-empty handle, an integer frontier
// within the seq ceiling. Corruption costs at most one repeated nudge, never
// a suppressed worker.
func (s *State) Liveness() map[string]LivenessRecord {
	out := map[string]LivenessRecord{}
	b, err := os.ReadFile(s.livenessPath())
	if err != nil {
		return out
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil || top == nil {
		return out
	}
	for worker, raw := range top {
		var rec map[string]json.RawMessage
		if json.Unmarshal(raw, &rec) != nil {
			continue
		}
		var r LivenessRecord
		if v, ok := jsonInt(rec["idleSince"]); ok && v >= 0 {
			r.IdleSince = &v
		}
		var h string
		if json.Unmarshal(rec["handle"], &h) == nil && h != "" {
			r.Handle = h
		}
		if v, ok := jsonInt(rec["coveredFrontier"]); ok && v >= 0 && v <= seqMax {
			f := int(v)
			r.CoveredFrontier = &f
		}
		out[worker] = r
	}
	return out
}

// jsonInt reads a JSON number that is an exact integer (Number.isInteger).
func jsonInt(raw json.RawMessage) (int64, bool) {
	var f float64
	if len(raw) == 0 || json.Unmarshal(raw, &f) != nil || f != float64(int64(f)) {
		return 0, false
	}
	return int64(f), true
}

func (s *State) saveLiveness(m map[string]LivenessRecord) error {
	return writeJSON(s.livenessPath(), m)
}

// livenessNotes is each flagged worker's last LOGGED fail-closed reason, so a
// persistently unreadable source logs once per reason. Its own file: the
// ledger's Reported map is token-scoped, and a source outage outlives tokens.
func (s *State) livenessNotes() map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(s.notesPath())
	if err != nil {
		return out
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return out
	}
	for k, v := range m {
		if str, ok := v.(string); ok {
			out[k] = str
		}
	}
	return out
}

func (s *State) saveLivenessNotes(m map[string]string) error {
	return writeJSON(s.notesPath(), m)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return atomicWrite(path, b)
}

// atomicWrite writes via a same-directory temp file and a rename, mode 0600.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // a no-op once renamed
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
