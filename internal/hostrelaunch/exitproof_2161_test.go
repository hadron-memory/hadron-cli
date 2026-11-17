package hostrelaunch

import (
	"context"
	"errors"
	"testing"
	"time"
)

// cli#2161 (C12): after a context reset the poller has positive evidence that
// the old agent is gone (the pidfd of the agent it launched fired, and a fresh
// stamp names the same session). That exit proof stands in for the
// two-sweep absence rule and nothing else: every guard, the cap and the
// verification are the same Runner code.

func exitProofOf(proven bool, err error, calls *int) func(context.Context, Target) (bool, error) {
	return func(context.Context, Target) (bool, error) {
		*calls++
		return proven, err
	}
}

func TestAnExitProofLaunchesOnTheFirstObservation2161(t *testing.T) {
	f := newFixture(t)
	calls := 0
	f.runner.ExitProof = exitProofOf(true, nil, &calls)
	if r := f.run(t, 5); r.Status != Applied || r.Reference != "own-terminal" {
		t.Fatalf("an exit-proven pass: %+v", r)
	}
	if calls != 1 || f.starts != 1 || f.claimedAtStart != 1 || f.verified != 1 {
		t.Fatalf("calls=%d fixture=%+v", calls, f)
	}
	if rec := f.store.rows[f.target.WorkerID]; rec.AbsentFor != "" || rec.AbsentSweep != 0 || len(rec.Attempts) != 1 {
		t.Fatalf("record after an exit-proven launch: %+v", rec)
	}
}

// Without the proof an exit pass launches nothing and leaves the absence
// record alone: it observed one worker, not a sweep, so it must neither
// start nor advance two-sweep continuity.
func TestAnUnprovenExitPassLaunchesNothingAndRecordsNoAbsence2161(t *testing.T) {
	for name, proof := range map[string]struct {
		proven bool
		err    error
	}{"not proven": {false, nil}, "proof error": {false, errors.New("stamp unreadable")}, "error with true": {true, errors.New("stamp unreadable")}} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			// An earlier sweep's first absence is left exactly as it was.
			f.run(t, 4)
			before := f.store.rows[f.target.WorkerID]
			saves := f.store.saves
			calls := 0
			f.runner.ExitProof = exitProofOf(proof.proven, proof.err, &calls)
			f.runner.Sweep = 5
			r, _ := f.runner.Execute(context.Background(), f.target)
			if r.Reason != NoExitProof || f.starts != 0 {
				t.Fatalf("unproven exit: %+v starts=%d", r, f.starts)
			}
			if f.store.saves != saves || f.store.rows[f.target.WorkerID].AbsentSweep != before.AbsentSweep {
				t.Fatalf("an unproven exit pass changed the journal: %+v", f.store.rows[f.target.WorkerID])
			}
		})
	}
}

// The proof never lifts the cap: the attempts in the last hour still refuse,
// and the proof is not even consulted (a stamp is not consumed by a refusal).
func TestAnExitProofDoesNotLiftTheCap2161(t *testing.T) {
	f := newFixture(t)
	now := f.runner.Now()
	f.store.rows[f.target.WorkerID] = Record{Attempts: []time.Time{now.Add(-50 * time.Minute), now.Add(-20 * time.Minute), now.Add(-time.Minute)}}
	calls := 0
	f.runner.ExitProof = exitProofOf(true, nil, &calls)
	if r := f.run(t, 5); r.Reason != RateLimited || f.starts != 0 || calls != 0 {
		t.Fatalf("capped: %+v starts=%d proofCalls=%d", r, f.starts, calls)
	}
}

// Nor any refusal: desired state, binding identity and a present agent all
// refuse before the proof is asked.
func TestAnExitProofDoesNotLiftARefusal2161(t *testing.T) {
	for name, set := range map[string]func(*fixture){
		"paused":           func(f *fixture) { f.desired.Reason = "PAUSED" },
		"resetting":        func(f *fixture) { f.desired.Reason = "RESETTING" },
		"not should-run":   func(f *fixture) { f.desired.State = "STOPPED" },
		"unknown reason":   func(f *fixture) { f.desired.Reason = "SHORT_LIVED" },
		"sweep not apply":  func(f *fixture) { f.desired.SweepMode = "OBSERVE" },
		"another session":  func(f *fixture) { f.binding.SessionID = "other-session" },
		"unusable binding": func(f *fixture) { f.binding.Usable = false },
		"agent present":    func(f *fixture) { f.launched, f.post = true, Observation{Proven: true, PIDs: []int{7}} },
		"census unproven":  func(f *fixture) { f.launched, f.post = true, Observation{} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			set(f)
			calls := 0
			f.runner.ExitProof = exitProofOf(true, nil, &calls)
			f.runner.Sweep = 5
			r, _ := f.runner.Execute(context.Background(), f.target)
			if r.Status == Applied || f.starts != 0 || calls != 0 {
				t.Fatalf("%s: %+v starts=%d proofCalls=%d", name, r, f.starts, calls)
			}
		})
	}
}
