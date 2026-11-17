package delivery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStopReporterNonFirstAcceptedAuthor1673(t *testing.T) {
	for _, mismatch := range []string{"hidden", "body", "author"} {
		t.Run(mismatch, func(t *testing.T) {
			f := newReporterFixture(t)
			f.chat.failPost = true
			if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "failed" || f.chat.posts != 0 || f.chat.attempts != 1 {
				t.Fatalf("first reporter: %+v attempts%d posts%d", out, f.chat.attempts, f.chat.posts)
			}
			f.chat.failPost = false
			f.chat.boundID, f.chat.boundName = "w-hal", "Hal"
			hal := func() *StopReporter { r := f.reporter(); r.ReporterWorker = "Hal"; return r }
			if out := onlyOutcome(t, f.run(hal())); out.Result != "waiting" || f.chat.attempts != 1 {
				t.Fatalf("reconcile window: %+v attempts%d", out, f.chat.attempts)
			}
			f.clock = f.clock.Add(DefaultReconcileAfter + time.Second)
			r := hal()
			var accepted ChatMessage
			r.afterPost = func() {
				accepted = f.chat.msgs[len(f.chat.msgs)-1]
				switch mismatch {
				case "hidden":
					f.chat.hideFromRead = true
				case "body":
					f.chat.msgs[len(f.chat.msgs)-1].Body = "edited"
				case "author":
					f.chat.msgs[len(f.chat.msgs)-1].AuthorWorkerID = "wrong-worker"
				}
			}
			if out := onlyOutcome(t, f.run(r)); out.Result != "failed" || out.Seq != 2 || f.reported() {
				t.Fatalf("accepted read-back: %+v reported%v", out, f.reported())
			}
			if accepted.AuthorWorkerID != "w-hal" || accepted.Seq != 2 {
				t.Fatalf("accepted post: %+v", accepted)
			}
			// Read the durable answer, not a field on the reporter that just posted.
			data, err := os.ReadFile(filepath.Join(f.box.Dir, ev1Base()+".sending"))
			if err != nil {
				t.Fatal(err)
			}
			var state stopSending
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if state.AuthorWorkerID != "w-hugo" || state.AcceptedAuthorWorkerID != "w-hal" || state.AcceptedSeq != accepted.Seq || !state.attemptedBy("w-hal") {
				t.Fatalf("accepted author not retained: %s", data)
			}
			// A new reporter object/process must not post again while the accepted
			// sequence is unreadable, even after the duplicate window has elapsed.
			f.clock = f.clock.Add(time.Hour)
			if out := onlyOutcome(t, f.run(hal())); out.Result != "failed" || f.reported() || f.chat.attempts != 2 || f.chat.posts != 1 {
				t.Fatalf("hidden accepted replay: %+v attempts%d posts%d", out, f.chat.attempts, f.chat.posts)
			}
			f.chat.hideFromRead = false
			f.chat.msgs[len(f.chat.msgs)-1] = accepted
			f.chat.boundID, f.chat.boundName = "w-other", "Other"
			resumed := f.reporter()
			resumed.ReporterWorker = "Other"
			if out := onlyOutcome(t, f.run(resumed)); out.Result != "reconciled" || out.Seq != accepted.Seq || !f.reported() || f.chat.attempts != 2 || f.chat.posts != 1 {
				t.Fatalf("visible accepted replay: %+v attempts%d posts%d", out, f.chat.attempts, f.chat.posts)
			}
		})
	}
}

func TestStopReporterAcceptedReplayMarkerSequence1673(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.hideFromRead = true
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "failed" || out.Seq != 2 || f.reported() {
		t.Fatalf("setup: %+v", out)
	}
	f.clock = f.clock.Add(time.Hour)
	f.chat.hideFromRead = false
	out := onlyOutcome(t, f.run(f.reporter()))
	if out.Result != "reconciled" || out.Seq != 2 || f.chat.posts != 1 || f.chat.attempts != 1 {
		t.Fatalf("replay: %+v attempts%d posts%d", out, f.chat.attempts, f.chat.posts)
	}
	data, err := os.ReadFile(filepath.Join(f.box.Dir, ev1Base()+".reported"))
	if err != nil {
		t.Fatal(err)
	}
	var marker stopReported
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.EventID != "ev-1" || marker.Seq != 2 {
		t.Fatalf("wrong durable marker: %s", data)
	}
}
