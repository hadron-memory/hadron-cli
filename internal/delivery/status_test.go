package delivery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cli#906 row 3: the poller's local delivery snapshot for `team host status`.
// It is observational only, and it holds codes, never text, proofs or drafts.
func TestStatusBookRoundTrip(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC)
	book := NewStatusBook(dir)
	book.Observe(Event{Time: at, Worker: "iris", WorkerID: "wkr1", Kind: Held, Reason: HoldDraft, Terminal: "term_1", ResolvedBy: "screen"})
	book.Observe(Event{Time: at.Add(time.Second), Worker: "iris", WorkerID: "wkr1", Kind: Delivered, Terminal: "term_1", ResolvedBy: "identity"})
	book.Observe(Event{Time: at, Worker: "dara", WorkerID: "wkr2", Kind: Skipped, Reason: "no-terminal"})
	if err := book.Step(at.Add(2*time.Second), StepRead); err != nil {
		t.Fatal(err)
	}
	st, err := ReadStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.LastStepAt.Equal(at.Add(2*time.Second)) || st.LastRead != StepRead {
		t.Fatalf("step %+v", st)
	}
	iris := st.Workers["wkr1"]
	if iris.Name != "iris" || iris.Kind != Delivered || iris.Reason != "" || iris.ResolvedBy != "identity" || !iris.At.Equal(at.Add(time.Second)) {
		t.Fatalf("iris %+v: the newest event wins", iris)
	}
	if st.Workers["wkr2"].Reason != "no-terminal" {
		t.Fatalf("dara %+v", st.Workers["wkr2"])
	}
	info, err := os.Stat(filepath.Join(dir, "delivery", "status.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("status file %v %v, want 0600", info, err)
	}
}

func TestStatusBookKeepsAnUnreportedHold(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC)
	book := NewStatusBook(dir)
	book.Observe(Event{Time: at, Worker: "iris", WorkerID: "wkr1", Kind: Held, Reason: HoldDraft, Unreported: true})
	if err := book.Step(at, StepRead); err != nil {
		t.Fatal(err)
	}
	st, err := ReadStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if w := st.Workers["wkr1"]; w.Kind != Held || !w.Unreported {
		t.Fatalf("iris %+v", w)
	}
}

func TestStatusBookRecordsAScopeRefusal(t *testing.T) {
	dir := t.TempDir()
	book := NewStatusBook(dir)
	if err := book.Step(time.Now(), StepOffByScope); err != nil {
		t.Fatal(err)
	}
	if st, err := ReadStatus(dir); err != nil || st.LastRead != StepOffByScope {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestReadStatusMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadStatus(dir); err == nil || !os.IsNotExist(err) {
		t.Fatalf("missing: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "delivery"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "delivery", "status.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStatus(dir); err == nil || os.IsNotExist(err) {
		t.Fatalf("corrupt: %v", err)
	}
}

// The book only ever sees Events, which carry codes; this pins that a
// Terminal handle and codes are all that reach the file.
func TestStatusFileCarriesNoText(t *testing.T) {
	dir := t.TempDir()
	book := NewStatusBook(dir)
	book.Observe(Event{Time: time.Now(), Worker: "iris", WorkerID: "wkr1", Span: "ch-team:5-5", Kind: Held, Reason: HoldDraft})
	_ = book.Step(time.Now(), StepRead)
	b, _ := os.ReadFile(filepath.Join(dir, "delivery", "status.json"))
	if strings.Contains(string(b), "ch-team:5-5") {
		t.Fatalf("the span is per batch, not status: %s", b)
	}
}

// hadron-cli#1178: a worker's current run of holds keeps the time it began,
// so `team host status` can flag a worker held too long (the #2746 class
// held four workers for two days with no readable reason). Only a delivered
// or reconciled event ends the run; a deferral or skip in between does not.
func TestStatusBookKeepsWhenAHoldRunBegan(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 29, 1, 0, 0, 0, time.UTC)
	book := NewStatusBook(dir)
	ev := func(off time.Duration, k Kind, reason string) {
		book.Observe(Event{Time: at.Add(off), Worker: "iris", WorkerID: "wkr1", Kind: k, Reason: reason, Wake: true})
	}
	read := func() WorkerStatus {
		t.Helper()
		if err := book.Step(at.Add(time.Hour), StepRead); err != nil {
			t.Fatal(err)
		}
		st, err := ReadStatus(dir)
		if err != nil {
			t.Fatal(err)
		}
		return st.Workers["wkr1"]
	}
	ev(0, Delivered, "")
	if w := read(); w.HeldSince != nil {
		t.Fatalf("no hold yet: %+v", w)
	}
	ev(time.Minute, Held, HoldNotSubmitted)
	ev(2*time.Minute, Deferred, "busy")
	ev(3*time.Minute, Skipped, "locked")
	ev(4*time.Minute, Held, HoldNotSubmitted)
	if w := read(); w.HeldSince == nil || !w.HeldSince.Equal(at.Add(time.Minute)) || w.Kind != Held {
		t.Fatalf("the run began at the first hold: %+v", w)
	}
	ev(5*time.Minute, Reconciled, "")
	if w := read(); w.HeldSince != nil {
		t.Fatalf("a reconciled event ends the run: %+v", w)
	}
}
