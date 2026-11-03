package hostpoller

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

// TestResetRecordSurvivesRestart pins seam 1's local half (#1043): which
// resets this host collected is kept in the state file, so a restarted
// poller can find them. The phase is never stored: it is read from the
// server on recovery.
func TestResetRecordSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := ResetRecord{ResetID: "r1", OperationID: "op1", WorkerID: "w1", Epoch: 0, Old: ProcessIdentity{PID: 42, StartTime: "999"}}
	if err := s.recordReset(rec); err != nil {
		t.Fatal(err)
	}
	// Kept on disk at once: the claim is sent only after this returns.
	if first, _ := LoadState(path); !reflect.DeepEqual(first.inFlightResets(), []ResetRecord{rec}) {
		t.Fatalf("not on disk before the claim: %+v", first.inFlightResets())
	}
	if err := s.updateReset("r1", func(r *ResetRecord) { r.Epoch = 3; r.New = &ProcessIdentity{PID: 43, StartTime: "1000"} }); err != nil {
		t.Fatal(err)
	}
	again, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []ResetRecord{{ResetID: "r1", OperationID: "op1", WorkerID: "w1", Epoch: 3, Old: ProcessIdentity{PID: 42, StartTime: "999"}, New: &ProcessIdentity{PID: 43, StartTime: "1000"}}}
	if got := again.inFlightResets(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after reload %+v, want %+v", got, want)
	}
	if err := again.dropReset("r1"); err != nil {
		t.Fatal(err)
	}
	third, _ := LoadState(path)
	if got := third.inFlightResets(); len(got) != 0 {
		t.Fatalf("a dropped record survived: %+v", got)
	}
}

func TestResetRecordUpdateOfUnknownIsAnError(t *testing.T) {
	s, _ := LoadState(filepath.Join(t.TempDir(), "state.json"))
	if err := s.updateReset("nope", func(*ResetRecord) {}); err == nil {
		t.Fatal("updating an unknown reset record succeeded")
	}
}

// readResets is a ReadReset fake keyed by reset id.
type readResets struct {
	fakeResets
	answers map[string]ContextReset
	errs    map[string]error
}

func (f *readResets) ReadReset(_ context.Context, id string) (ContextReset, error) {
	f.calls = append(f.calls, "read:"+id)
	if err, ok := f.errs[id]; ok {
		return ContextReset{}, err
	}
	return f.answers[id], nil
}

// TestPendingRecoveriesReadEveryPhaseFromTheServer pins that recovery takes
// each record's phase from the server, one read per record and no step; a
// reset the server no longer shows this host (NOT_FOUND, NOT_COLLECTED) is
// dropped locally, any other read error keeps the record for the next pass.
func TestPendingRecoveriesReadEveryPhaseFromTheServer(t *testing.T) {
	s, _ := LoadState(filepath.Join(t.TempDir(), "state.json"))
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := s.recordReset(ResetRecord{ResetID: id, OperationID: "op-" + id, WorkerID: "w-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	rs := &readResets{
		answers: map[string]ContextReset{"a": {ID: "a", Phase: "stop-granted", Epoch: 3}},
		errs: map[string]error{
			"b": &ResetStepError{Code: "CONTEXT_RESET_NOT_FOUND", Err: errors.New("x")},
			"c": &ResetStepError{Code: "HOST_OPERATION_NOT_COLLECTED", Err: errors.New("x")},
			"d": errors.New("transport down"),
		},
	}
	got := pendingRecoveries(context.Background(), s, rs)
	if len(got) != 1 || got[0].Record.ResetID != "a" || got[0].Reset.Phase != "stop-granted" {
		t.Fatalf("pending %+v, want only a at stop-granted", got)
	}
	for _, c := range rs.calls {
		if c != "read:a" && c != "read:b" && c != "read:c" && c != "read:d" {
			t.Fatalf("recovery made a protocol step: %v", rs.calls)
		}
	}
	left := map[string]bool{}
	for _, r := range s.inFlightResets() {
		left[r.ResetID] = true
	}
	if !left["a"] || left["b"] || left["c"] || !left["d"] {
		t.Fatalf("records left %v: want a (pending) and d (unread) kept, b and c dropped", left)
	}
}
