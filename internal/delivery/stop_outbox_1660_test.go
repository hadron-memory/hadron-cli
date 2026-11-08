package delivery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hadron-cli#1660 ask 1(b): one corrupt record does not stop every other
// notice from being reported. Red on main: the whole run aborted.
func TestStopReporterCorruptRecordFailsAloneAndOthersAreReported_1660(t *testing.T) {
	f := newReporterFixture(t) // ev-1 pending
	bad := stopDropName("ev-corrupt")
	if err := os.WriteFile(filepath.Join(f.box.Dir, bad), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := f.reporter().Run(t.Context())
	if err != nil {
		t.Fatalf("a corrupt record must not abort the run: %v", err)
	}
	var reported, failed []ReportOutcome
	for _, o := range run.Outcomes {
		switch o.Result {
		case "reported":
			reported = append(reported, o)
		case "failed":
			failed = append(failed, o)
		}
	}
	if len(reported) != 1 || reported[0].EventID != "ev-1" || !f.reported() {
		t.Fatalf("the valid notice was not reported: %+v", run.Outcomes)
	}
	if len(failed) != 1 || failed[0].File != bad || !strings.Contains(failed[0].Error, bad) {
		t.Fatalf("the corrupt file is not named as failed: %+v", failed)
	}
	// Never counted as reported: no marker, and a second run fails it again.
	if _, err := os.Stat(filepath.Join(f.box.Dir, strings.TrimSuffix(bad, ".json")+".reported")); err == nil {
		t.Fatal("a corrupt record got a reported marker")
	}
	run2, _ := f.reporter().Run(t.Context())
	if len(run2.Outcomes) != 1 || run2.Outcomes[0].File != bad || run2.Outcomes[0].Result != "failed" {
		t.Fatalf("second run: %+v", run2.Outcomes)
	}
	// An unreadable (directory) .json entry is skipped as before; a record
	// that is a valid-looking name but empty is corrupt too.
	p, err := f.reporter().PendingStatus(time.Minute)
	if err != nil || p.Corrupt != 1 {
		t.Fatalf("status must count the corrupt file: %+v err %v", p, err)
	}
}

// Ask 1(a): a reported record past the window is removed (record and
// markers); a pending one is never removed, however old.
func TestStopReporterRetentionRemovesOnlyOldReportedRecords_1660(t *testing.T) {
	f := newReporterFixture(t)
	r := f.reporter()
	r.RetainReported = 48 * time.Hour
	f.run(r) // ev-1 reported at clock
	if !f.reported() {
		t.Fatal("setup: not reported")
	}
	// a second notice that stays pending: its post fails
	if _, err := f.box.Drop(StopDrop{EventID: "ev-2", WorkerID: "w-gil", Worker: "Gil", Level: "HALT", HoldReason: "draft", HeldAt: f.clock.Add(-100 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f.chat.failPost = true
	exists := func(base, suffix string) bool {
		_, err := os.Stat(filepath.Join(f.box.Dir, base+suffix))
		return err == nil
	}
	ev2 := strings.TrimSuffix(stopDropName("ev-2"), ".json")

	f.clock = f.clock.Add(47 * time.Hour) // inside the window
	run := f.run(r)
	if run.Pruned != 0 || !exists(ev1Base(), ".json") || !f.reported() {
		t.Fatalf("inside the window nothing is removed: pruned %d", run.Pruned)
	}
	f.clock = f.clock.Add(2 * time.Hour) // past it
	run = f.run(r)
	if run.Pruned != 1 {
		t.Fatalf("pruned = %d, want 1", run.Pruned)
	}
	for _, suffix := range []string{".json", ".sending", ".reported", ".lastfail"} {
		if exists(ev1Base(), suffix) {
			t.Fatalf("ev-1%s survived retention", suffix)
		}
	}
	if !exists(ev2, ".json") || !exists(ev2, ".sending") {
		t.Fatal("a pending record was removed")
	}
	// a pruned notice is not reported again
	f.chat.failPost = false
	f.run(r)
	if n := strings.Count(strings.Join(f.chat.bodies(), "\n"), "ev-1"); n != 1 {
		t.Fatalf("ev-1 posted %d times", n)
	}
}

func TestStopReporterNegativeRetentionKeepsReportedRecords_1660(t *testing.T) {
	f := newReporterFixture(t)
	r := f.reporter()
	r.RetainReported = -1
	f.run(r)
	f.clock = f.clock.Add(365 * 24 * time.Hour)
	if run := f.run(r); run.Pruned != 0 || !f.reported() {
		t.Fatalf("negative retention removed a record: %+v", run)
	}
}

func TestStopReporterDefaultRetentionIsThirtyDays_1660(t *testing.T) {
	f := newReporterFixture(t)
	r := f.reporter()
	f.run(r)
	f.clock = f.clock.Add(29 * 24 * time.Hour)
	if run := f.run(r); run.Pruned != 0 {
		t.Fatalf("pruned inside 30 days: %d", run.Pruned)
	}
	f.clock = f.clock.Add(2 * 24 * time.Hour)
	if run := f.run(r); run.Pruned != 1 {
		t.Fatalf("not pruned past 30 days: %d", run.Pruned)
	}
}

// Ask 2: the non-empty-author guards in reconcile. A corrupt-looking intent
// with no author lists "" among its attempting reporters, so only the guards
// stop a message with no author from being taken as this notice's post.
func writeIntent1660(t *testing.T, f *reporterFixture, s stopSending) {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.box.Dir, ev1Base()+".sending"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStopReporterReconcileNeverMatchesAPostWithNoAuthor_1660(t *testing.T) {
	f := newReporterFixture(t)
	writeIntent1660(t, f, stopSending{EventID: "ev-1", Body: "the flag", HeadSeq: 1, FirstAttemptAt: f.clock, AuthorWorkerID: ""})
	f.chat.mu.Lock()
	f.chat.msgs = append(f.chat.msgs, ChatMessage{Seq: 2, AuthorWorkerID: "", Body: "the flag"})
	f.chat.mu.Unlock()
	f.clock = f.clock.Add(DefaultReconcileAfter + time.Second)
	out := onlyOutcome(t, f.run(f.reporter()))
	if out.Result == "reconciled" || out.Seq == 2 || f.reported() && out.Seq == 2 {
		t.Fatalf("an authorless post was taken as ours: %+v", out)
	}
	if out.Result != "reported" || f.chat.posts != 1 {
		t.Fatalf("expected its own post: %+v posts %d", out, f.chat.posts)
	}
}

func TestStopReporterAcceptedRecordWithNoAuthorNeedsAPerson_1660(t *testing.T) {
	f := newReporterFixture(t)
	writeIntent1660(t, f, stopSending{EventID: "ev-1", Body: "the flag", HeadSeq: 1, FirstAttemptAt: f.clock,
		AuthorWorkerID: "", AcceptedSeq: 2, AcceptedAuthorWorkerID: ""})
	f.chat.mu.Lock()
	f.chat.msgs = append(f.chat.msgs, ChatMessage{Seq: 2, AuthorWorkerID: "", Body: "the flag"})
	f.chat.mu.Unlock()
	out := onlyOutcome(t, f.run(f.reporter()))
	if out.Result != "failed" || out.Reason != "invalid-accepted-record" || f.reported() {
		t.Fatalf("a record with no accepted author was finished: %+v", out)
	}
}
