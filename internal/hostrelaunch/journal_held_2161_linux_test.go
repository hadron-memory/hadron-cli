//go:build linux

package hostrelaunch

import (
	"path/filepath"
	"testing"
	"time"
)

// cli#2161: an exit pass opens the journal under the same exclusive run lock
// as a sweep, sees the same attempts (the shared cap), and claims no
// generation, so the absence continuity of the other workers is untouched.
func TestHeldJournalSharesTheLockAndCapButClaimsNoGeneration2161(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "relaunch")
	s, err := OpenJournal(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := s.Save("w", Record{Attempts: []time.Time{at}, AbsentFor: "other", AbsentSweep: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJournalHeld(dir); err == nil {
		t.Fatal("an exit pass ran beside a sweep holding the lock")
	}
	_ = s.Close()
	h, err := OpenJournalHeld(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.Sweep() != 1 {
		t.Fatalf("an exit pass claimed a generation: %d", h.Sweep())
	}
	if rec, _ := h.Load("w"); len(rec.Attempts) != 1 || rec.AbsentSweep != 1 {
		t.Fatalf("an exit pass saw %+v", rec)
	}
	if _, err := OpenJournal(dir, false); err == nil {
		t.Fatal("a sweep ran beside an exit pass holding the lock")
	}
	_ = h.Close()
	s, err = OpenJournal(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if s.Sweep() != 2 {
		t.Fatalf("the next sweep is %d, want 2", s.Sweep())
	}
}
