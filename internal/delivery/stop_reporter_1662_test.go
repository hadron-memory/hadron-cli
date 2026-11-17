package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every iteration constructs a new reporter: the accepted answer must survive
// process replacement, not just a retry on one in-memory object.
func TestStopReporterAcceptedSeqNeverReposts_1662(t *testing.T) {
	for _, kind := range []string{"body", "author", "not found", "read error"} {
		t.Run(kind, func(t *testing.T) {
			f := newReporterFixture(t)
			r := f.reporter()
			r.afterPost = func() {
				switch kind {
				case "body":
					f.chat.msgs[len(f.chat.msgs)-1].Body = "edited"
				case "author":
					f.chat.msgs[len(f.chat.msgs)-1].AuthorWorkerID = "different-worker"
				case "not found":
					f.chat.hideFromRead = true
				case "read error":
					f.chat.readErr = errors.New("synthetic read failure")
				}
			}
			alterReadBack := r.afterPost
			wrongResult := false
			for i, step := range []time.Duration{0, time.Minute, 11 * time.Minute, 22 * time.Minute, time.Hour} {
				f.clock = f.clock.Add(step)
				out := onlyOutcome(t, f.run(r))
				wrongResult = wrongResult || out.Result != "failed"
				if f.reported() {
					t.Fatalf("run%d incorrectly reported: %+v", i, out)
				}
				if f.chat.attempts != 1 || f.chat.posts != 1 {
					t.Fatalf("run%d reposted: attempts%d posts%d", i, f.chat.attempts, f.chat.posts)
				}
				pending, _, err := f.reporter().Pending(0)
				if err != nil || pending != 1 {
					t.Fatalf("pending%d err%v", pending, err)
				}
				r = f.reporter()
				r.afterPost = alterReadBack
			}
			if wrongResult {
				t.Fatal("an accepted-seq read failure must stay failed, never waiting")
			}
			data, err := os.ReadFile(filepath.Join(f.box.Dir, ev1Base()+".sending"))
			if err != nil {
				t.Fatal(err)
			}
			var state struct {
				Seq    int    `json:"acceptedSeq"`
				Author string `json:"acceptedAuthorWorkerId"`
			}
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if state.Seq != 2 || state.Author != "w-hugo" {
				t.Fatalf("accepted state%+v", state)
			}
		})
	}
}

func TestStopReporterAcceptedMismatchNeedsPerson_1662(t *testing.T) {
	f := newReporterFixture(t)
	r := f.reporter()
	r.afterPost = func() { f.chat.msgs[len(f.chat.msgs)-1].Body = "edited" }
	out := onlyOutcome(t, f.run(r))
	if out.Result != "failed" || out.Seq != 2 || !strings.Contains(out.Error, "needs a person") {
		t.Fatalf("outcome%+v", out)
	}
}

func TestStopReporterAcceptedSeqLaterMatchesAcrossReporterChange_1662(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.hideFromRead = true
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "failed" {
		t.Fatal(out)
	}
	f.clock = f.clock.Add(time.Hour)
	f.chat.boundID, f.chat.boundName = "w-other", "Other"
	r := f.reporter()
	r.ReporterWorker = "Other"
	f.chat.hideFromRead = false
	out := onlyOutcome(t, f.run(r))
	if out.Result != "reconciled" || out.Seq != 2 || !f.reported() || f.chat.attempts != 1 {
		t.Fatalf("outcome%+v attempts%d reported%v", out, f.chat.attempts, f.reported())
	}
}

func TestStopReporterDuplicateSeqNeverRepostsAfterMismatch_1662(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.loseResponse = true
	f.run(f.reporter()) // Legacy/unknown answer: no accepted seq yet.
	f.chat.loseResponse = false
	f.chat.hideFromRead = true
	f.clock = f.clock.Add(DefaultReconcileAfter + time.Second)
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "failed" || f.chat.attempts != 2 || f.chat.posts != 1 {
		t.Fatalf("duplicate setup%+v attempts%d posts%d", out, f.chat.attempts, f.chat.posts)
	}
	f.clock = f.clock.Add(time.Hour)
	out := onlyOutcome(t, f.run(f.reporter()))
	if out.Result != "failed" || f.chat.attempts != 2 || f.chat.posts != 1 || f.reported() {
		t.Fatalf("outcome%+v attempts%d posts%d", out, f.chat.attempts, f.chat.posts)
	}
	f.chat.hideFromRead = false
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "reconciled" || !f.reported() || f.chat.attempts != 2 {
		t.Fatalf("recovery%+v attempts%d", out, f.chat.attempts)
	}
}

func TestStopReporterAcceptedSeqDurableBeforeReadBack_1662(t *testing.T) {
	f := newReporterFixture(t)
	r := f.reporter()
	r.afterPost = func() {
		data, err := os.ReadFile(filepath.Join(f.box.Dir, ev1Base()+".sending"))
		if err != nil {
			t.Fatal(err)
		}
		var state map[string]any
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		if state["acceptedSeq"] != float64(2) || state["acceptedAuthorWorkerId"] != "w-hugo" {
			t.Fatalf("accepted answer not durable before read-back: %s", data)
		}
		panic("crash before read-back")
	}
	func() { defer func() { _ = recover() }(); f.run(r) }()
	if f.reported() {
		t.Fatal("crash wrote marker")
	}
	f.clock = f.clock.Add(time.Hour)
	f.chat.hideFromRead = true
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "failed" || f.chat.attempts != 1 {
		t.Fatalf("outcome%+v attempts%d", out, f.chat.attempts)
	}
	f.chat.hideFromRead = false
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "reconciled" || !f.reported() || f.chat.attempts != 1 {
		t.Fatalf("outcome%+v attempts%d", out, f.chat.attempts)
	}
}

func TestStopReporterInvalidAcceptedRecordRefusesWithoutPost_1662(t *testing.T) {
	for _, state := range []string{`"acceptedSeq":-1,"acceptedAuthorWorkerId":"w-hugo"`, `"acceptedSeq":2`, `"acceptedSeq":2,"acceptedAuthorWorkerId":"unrecorded-worker"`} {
		t.Run(state, func(t *testing.T) {
			f := newReporterFixture(t)
			raw := `{"eventId":"ev-1","body":"frozen","headSeq":1,"authorWorkerId":"w-hugo","firstAttemptAt":"2026-11-04T10:00:00Z",` + state + `}`
			if _, err := f.box.writeFile(ev1Base()+".sending", []byte(raw), true); err != nil {
				t.Fatal(err)
			}
			out := onlyOutcome(t, f.run(f.reporter()))
			if out.Result != "failed" || !strings.Contains(out.Error, "invalid recorded accepted post") || f.chat.attempts != 0 || f.reported() {
				t.Fatalf("outcome%+v attempts%d", out, f.chat.attempts)
			}
		})
	}
}

type acceptedAnswerAPI1662 struct {
	ReporterAPI
	afterAnswer func()
	reads       int
	invalidSeq  bool
}

func (a *acceptedAnswerAPI1662) Post(ctx context.Context, body string) (int, error) {
	seq, err := a.ReporterAPI.Post(ctx, body)
	if a.afterAnswer != nil {
		a.afterAnswer()
	}
	if a.invalidSeq {
		seq = 0
	}
	return seq, err
}
func (a *acceptedAnswerAPI1662) MessagesAfter(ctx context.Context, after, limit int) ([]ChatMessage, error) {
	a.reads++
	return a.ReporterAPI.MessagesAfter(ctx, after, limit)
}

func TestStopReporterAcceptedPersistenceFailureStopsBeforeReadBack_1662(t *testing.T) {
	f := newReporterFixture(t)
	api := &acceptedAnswerAPI1662{ReporterAPI: f.chat}
	api.afterAnswer = func() {
		name := filepath.Join(f.box.Dir, ev1Base()+".sending")
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		} // rename cannot replace a directory
	}
	r := f.reporter()
	r.API = api
	out := onlyOutcome(t, f.run(r))
	if out.Result != "failed" || out.Seq != 2 || !strings.Contains(out.Error, "recording accepted post #2") || api.reads != 0 || f.reported() {
		t.Fatalf("outcome%+v reads%d", out, api.reads)
	}
}

func TestStopReporterInvalidAnswerStopsBeforeReadBack_1662(t *testing.T) {
	f := newReporterFixture(t)
	api := &acceptedAnswerAPI1662{ReporterAPI: f.chat, invalidSeq: true}
	r := f.reporter()
	r.API = api
	out := onlyOutcome(t, f.run(r))
	if out.Result != "failed" || !strings.Contains(out.Error, "valid sequence") || api.reads != 0 || f.reported() {
		t.Fatalf("outcome%+v reads%d", out, api.reads)
	}
}
