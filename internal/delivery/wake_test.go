package delivery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// --- wake fakes and helpers (cli#906 row 1) --------------------------------

type fakeWakeServer struct {
	acks []string
	errs []error
	res  AckResult
}

func (s *fakeWakeServer) AckWake(_ context.Context, proof string) (AckResult, error) {
	s.acks = append(s.acks, proof)
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		if err != nil {
			return "", err
		}
	}
	if s.res != "" {
		return s.res, nil
	}
	return Acked, nil
}

func (h *harness) wakeServer() *fakeWakeServer {
	h.t.Helper()
	if ws, ok := h.d.Wakes.(*fakeWakeServer); ok {
		return ws
	}
	ws := &fakeWakeServer{}
	h.d.Wakes = ws
	return ws
}

var tickAt = time.Date(2026, 10, 18, 6, 0, 0, 0, time.UTC)

func tick(id string, at time.Time) WakeReason {
	return WakeReason{Kind: WakeTick, TickerID: id, TickerName: "Check-in " + id, LastTickAt: at}
}

func task(eventID string, chain int, title string) WakeReason {
	return WakeReason{Kind: WakeTask, Task: &TaskReason{EventID: eventID, ChainIndex: chain, Event: "assigned", ID: "task-" + eventID, Title: title}}
}

func mention() WakeReason { return WakeReason{Kind: WakeMention} }

func (h *harness) wake(reasons ...WakeReason) Wake {
	return Wake{Proof: "wake-proof-SECRET", WorkerID: workerID, Reasons: reasons,
		DeliverAfter: h.now.Add(-time.Minute), BusyDeadline: h.now.Add(10 * time.Minute)}
}

func (h *harness) deliverWake(w Wake) Outcome {
	h.t.Helper()
	h.wakeServer()
	return h.d.DeliverWake(context.Background(), w, h.r)
}

func (h *harness) wakeRecords() map[string]WakeRecord {
	h.t.Helper()
	recs, err := h.d.WakeRecords.List(workerID)
	if err != nil {
		h.t.Fatal(err)
	}
	return recs
}

func (h *harness) wantWakeRecord(key, state string) {
	h.t.Helper()
	rec, ok := h.wakeRecords()[key]
	if !ok || rec.State != state {
		h.t.Fatalf("record %s = %+v (present %v), want %s", key, rec, ok, state)
	}
}

const tickLine = "[hadron tick] Check-in tk1: scheduled check-in. Read your team chat and tasks."

// --- the line ----------------------------------------------------------------

func TestWakeKeys(t *testing.T) {
	if k := tick("tk1", tickAt).Key(); k != "tick:tk1:2026-10-18T06:00:00Z" {
		t.Fatalf("tick key %q", k)
	}
	if k := task("ev9", 2, "x").Key(); k != "task:ev9:2" {
		t.Fatalf("task key %q", k)
	}
	if k := mention().Key(); k != "" {
		t.Fatalf("a mention is never recorded: %q", k)
	}
}

// S5 #2021 comment-6259060238: a task-less alarm carries no task id or
// title. It renders from its alarm type, never a blank or invented task,
// and keeps eventId+chainIndex as its identity.
func TestATaskLessAlarm(t *testing.T) {
	alarm := WakeReason{Kind: WakeTask, Task: &TaskReason{EventID: "ev7", ChainIndex: 2, Event: "alarm", AlarmType: "delivery.stuck", Title: "ignored without a task"}}
	if got := RenderWake([]WakeReason{alarm}); got != "[hadron task] alarm: delivery.stuck (no task)" {
		t.Fatalf("typed alarm %q", got)
	}
	untyped := alarm
	untyped.Task = &TaskReason{EventID: "ev7", ChainIndex: 2, Event: "alarm"}
	if got := RenderWake([]WakeReason{untyped}); got != "[hadron task] alarm (no task)" {
		t.Fatalf("alarm with no type %q", got)
	}
	if alarm.Key() != "task:ev7:2" || CheckWake(Wake{Proof: "p", WorkerID: "w", Reasons: []WakeReason{alarm}}) != nil {
		t.Fatal("a task-less alarm is a well-formed reason keyed by its event")
	}
	notAlarm := WakeReason{Kind: WakeTask, Task: &TaskReason{EventID: "ev8", Event: "assigned"}}
	if CheckWake(Wake{Proof: "p", WorkerID: "w", Reasons: []WakeReason{notAlarm}}) == nil {
		t.Fatal("only an alarm may come without its task")
	}
	h := newHarness(t)
	if o := h.deliverWake(h.wake(alarm)); o.Kind != Delivered || h.orca.sends[0].text != "[hadron task] alarm: delivery.stuck (no task)" {
		t.Fatalf("outcome %+v sends %+v", o, h.orca.sends)
	}
	h.wantWakeRecord("task:ev7:2", WakeAcked)
}

func TestRenderWake(t *testing.T) {
	if got := RenderWake([]WakeReason{tick("tk1", tickAt)}); got != tickLine {
		t.Fatalf("tick line %q", got)
	}
	got := RenderWake([]WakeReason{tick("tk1", tickAt), task("ev9", 0, "Fix the\nrouter")})
	want := tickLine + "; [hadron task] assigned: Fix the router (task-ev9)"
	if got != want {
		t.Fatalf("joined line\n got %q\nwant %q", got, want)
	}
	if strings.ContainsAny(got, "\r\n\x1b") {
		t.Fatal("a wake line must be one line with no control characters")
	}

	var many []WakeReason
	for i := range 30 {
		many = append(many, task(fmt.Sprintf("ev%d", i), 0, strings.Repeat("t", 60)))
	}
	cut := RenderWake(many)
	if n := utf8.RuneCountInString(cut); n > MaxWakeRunes {
		t.Fatalf("cut line is %d runes", n)
	}
	kept := strings.Count(cut, TaskPrefix)
	if !strings.HasSuffix(cut, fmt.Sprintf("(+%d more reasons)", 30-kept)) || kept == 0 {
		t.Fatalf("a cut line keeps the reason count: kept %d, %q", kept, cut[len(cut)-40:])
	}

	long := RenderWake([]WakeReason{task("ev1", 0, strings.Repeat("é", 2000))})
	if n := utf8.RuneCountInString(long); n > MaxWakeRunes || !strings.HasPrefix(long, TaskPrefix) || !utf8.ValidString(long) {
		t.Fatalf("one over-long reason: %d runes, %q…", n, long[:30])
	}
}

// --- what is typed, and what is acknowledged ----------------------------------

func TestANoChatTickIsTypedRecordedAndAcked(t *testing.T) {
	h := newHarness(t)
	o := h.deliverWake(h.wake(tick("tk1", tickAt)))
	if o.Kind != Delivered || len(h.orca.sends) != 1 || h.orca.sends[0].text != tickLine {
		t.Fatalf("outcome %+v sends %+v", o, h.orca.sends)
	}
	if ws := h.wakeServer(); len(ws.acks) != 1 || ws.acks[0] != "wake-proof-SECRET" {
		t.Fatalf("acks %+v", ws.acks)
	}
	if len(h.server.holds) != 0 || len(h.server.acks) != 0 {
		t.Fatal("a wake never touches the batch server")
	}
	h.wantWakeRecord(tick("tk1", tickAt).Key(), WakeAcked)
}

func TestAMentionOnlyWakeIsNeverTypedNorAcked(t *testing.T) {
	h := newHarness(t)
	h.d.AckMixed = true // not even after A′
	o := h.deliverWake(h.wake(mention(), mention()))
	h.wantNoSend()
	if o.Kind != Skipped || o.Reason != "mention-only" || len(h.wakeServer().acks) != 0 {
		t.Fatalf("outcome %+v acks %+v", o, h.wakeServer().acks)
	}
	if len(h.wakeRecords()) != 0 {
		t.Fatal("an untyped mention is never recorded")
	}
}

func TestMixedOrChannelNamingWakesAreTypedOnceAndHeldFromAck(t *testing.T) {
	cases := map[string]func(h *harness) Wake{
		"tick+mention":       func(h *harness) Wake { return h.wake(tick("tk1", tickAt), mention()) },
		"task naming ranges": func(h *harness) Wake { w := h.wake(task("ev1", 0, "T")); w.NamesChannels = true; return w },
		"tick naming ranges": func(h *harness) Wake { w := h.wake(tick("tk1", tickAt)); w.NamesChannels = true; return w },
		"tick+task+mention":  func(h *harness) Wake { return h.wake(tick("tk1", tickAt), task("ev1", 0, "T"), mention()) },
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			w := mk(h)
			o := h.deliverWake(w)
			if o.Kind != Delivered || o.Reason != "ack-held" || len(h.orca.sends) != 1 {
				t.Fatalf("outcome %+v sends %d", o, len(h.orca.sends))
			}
			if strings.Contains(h.orca.sends[0].text, "mention") {
				t.Fatalf("a mention is never typed: %q", h.orca.sends[0].text)
			}
			// The next read hands the same keys under a fresh proof: no
			// retype, and still no ack.
			w.Proof = "fresh-proof"
			o = h.deliverWake(w)
			if o.Kind != Deferred || o.Reason != "ack-held" || len(h.orca.sends) != 1 || len(h.wakeServer().acks) != 0 {
				t.Fatalf("second pass %+v sends %d acks %+v", o, len(h.orca.sends), h.wakeServer().acks)
			}
			for _, r := range w.Reasons {
				if r.Kind != WakeMention {
					h.wantWakeRecord(r.Key(), WakeTyped)
				}
			}
		})
	}
}

func TestAfterAPrimeAMixedWakeIsAckedOnceItsComponentsAreTyped(t *testing.T) {
	h := newHarness(t)
	h.d.AckMixed = true
	w := h.wake(tick("tk1", tickAt), mention())
	w.NamesChannels = true // an old pre-deploy proof: the server moves no mark for it
	o := h.deliverWake(w)
	if o.Kind != Delivered || o.Reason != "" || len(h.wakeServer().acks) != 1 {
		t.Fatalf("outcome %+v acks %+v", o, h.wakeServer().acks)
	}
	h.wantWakeRecord(tick("tk1", tickAt).Key(), WakeAcked)
}

func TestALostAckRecoversByAckOnlyEvenAfterARestartADayLater(t *testing.T) {
	h := newHarness(t)
	h.wakeServer().errs = []error{errors.New("WAKE_PROOF_STALE")}
	o := h.deliverWake(h.wake(tick("tk1", tickAt)))
	if o.Kind != Delivered || !o.AckPending {
		t.Fatalf("outcome %+v", o)
	}
	h.wantWakeRecord(tick("tk1", tickAt).Key(), WakeTyped)

	// Restart a day and more later: a new deliverer on the same state dir.
	h.now = h.now.Add(25 * time.Hour)
	if err := h.d.WakeRecords.Prune(workerID, h.now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	old := h.d
	h.d = New(h.server, h.orca, h.state)
	h.d.Now, h.d.Bound, h.d.Emit, h.d.Wakes = old.Now, old.Bound, old.Emit, old.Wakes
	w := h.wake(tick("tk1", tickAt))
	w.Proof = "fresh-proof"
	o = h.deliverWake(w)
	if o.Kind != Reconciled || len(h.orca.sends) != 1 {
		t.Fatalf("recovery %+v sends %d", o, len(h.orca.sends))
	}
	if ws := h.wakeServer(); ws.acks[len(ws.acks)-1] != "fresh-proof" {
		t.Fatalf("acks %+v", ws.acks)
	}
	h.wantWakeRecord(tick("tk1", tickAt).Key(), WakeAcked)
}

func TestARepeatedTickAcksOnlyAndTheNextTickIsTypedOnce(t *testing.T) {
	h := newHarness(t)
	h.deliverWake(h.wake(tick("tk1", tickAt)))
	if o := h.deliverWake(h.wake(tick("tk1", tickAt))); o.Kind != Reconciled {
		t.Fatalf("tick k again: %+v", o)
	}
	next := tick("tk1", tickAt.Add(time.Hour))
	o := h.deliverWake(h.wake(next))
	if o.Kind != Delivered || len(h.orca.sends) != 2 {
		t.Fatalf("tick k+1: %+v sends %d", o, len(h.orca.sends))
	}
	if h.deliverWake(h.wake(next)); len(h.orca.sends) != 2 {
		t.Fatal("tick k+1 typed twice")
	}
}

func TestOnlyUnrecordedReasonsAreTyped(t *testing.T) {
	h := newHarness(t)
	w := h.wake(tick("tk1", tickAt))
	w.NamesChannels = true
	h.deliverWake(w) // typed, held from ack
	h.deliverWake(h.wake(tick("tk1", tickAt), task("ev1", 0, "Review #9")))
	if len(h.orca.sends) != 2 || h.orca.sends[1].text != "[hadron task] assigned: Review #9 (task-ev1)" {
		t.Fatalf("sends %+v", h.orca.sends)
	}
}

func TestANewerTicksLandedAckSupersedesAnOlderHeldOne(t *testing.T) {
	h := newHarness(t)
	old := h.wake(tick("tk1", tickAt))
	old.NamesChannels = true
	h.deliverWake(old)
	h.wantWakeRecord(tick("tk1", tickAt).Key(), WakeTyped)
	h.deliverWake(h.wake(tick("tk1", tickAt.Add(time.Hour))))
	h.wantWakeRecord(tick("tk1", tickAt).Key(), WakeAcked)
	// Another ticker's typed record is untouched, and a task is superseded
	// only by its own ack.
	other := h.wake(tick("tk2", tickAt), task("ev1", 0, "T"))
	other.NamesChannels = true
	h.deliverWake(other)
	h.deliverWake(h.wake(tick("tk1", tickAt.Add(2*time.Hour))))
	h.wantWakeRecord(tick("tk2", tickAt).Key(), WakeTyped)
	h.wantWakeRecord(task("ev1", 0, "T").Key(), WakeTyped)
}

func TestUnchangedIsALandedWakeAck(t *testing.T) {
	h := newHarness(t)
	h.wakeServer().res = Unchanged
	if o := h.deliverWake(h.wake(tick("tk1", tickAt))); o.Kind != Delivered || o.AckPending {
		t.Fatalf("outcome %+v", o)
	}
	h.wantWakeRecord(tick("tk1", tickAt).Key(), WakeAcked)
}

func TestAForeignOrMalformedWakeIsNeitherTypedNorAcked(t *testing.T) {
	cases := map[string]func(h *harness) Wake{
		"another worker's":  func(h *harness) Wake { w := h.wake(tick("tk1", tickAt)); w.WorkerID = "w-other"; return w },
		"no proof":          func(h *harness) Wake { w := h.wake(tick("tk1", tickAt)); w.Proof = ""; return w },
		"no reasons":        func(h *harness) Wake { return h.wake() },
		"a tick with no id": func(h *harness) Wake { return h.wake(tick("", tickAt)) },
		"a tick with no k":  func(h *harness) Wake { return h.wake(tick("tk1", time.Time{})) },
		"a task with no event": func(h *harness) Wake {
			return h.wake(WakeReason{Kind: WakeTask, Task: &TaskReason{ID: "t", Title: "T", Event: "assigned"}})
		},
		"a task with no task": func(h *harness) Wake { return h.wake(WakeReason{Kind: WakeTask}) },
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			o := h.deliverWake(mk(h))
			h.wantNoSend()
			if o.Kind != Failed || len(h.wakeServer().acks) != 0 || len(h.wakeRecords()) != 0 {
				t.Fatalf("outcome %+v acks %+v", o, h.wakeServer().acks)
			}
		})
	}
}

// --- the shared terminal path ---------------------------------------------------

func TestWakeHoldsAreLocalNeverReportedToS5(t *testing.T) {
	cases := map[string]struct {
		screen []string
		draft  string
		reason string
	}{
		"draft":    {screen: claudeIdle, draft: "my own words", reason: HoldDraft},
		"question": {screen: []string{"Do you want to proceed?", "❯ 1. Yes", "  2. No"}, reason: HoldQuestion},
		"unknown":  {screen: []string{"nothing recognizable"}, reason: HoldScreenUnknown},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: c.screen, Draft: c.draft}}
			o := h.deliverWake(h.wake(tick("tk1", tickAt)))
			h.wantNoSend()
			if o.Kind != Held || o.Reason != c.reason {
				t.Fatalf("outcome %+v", o)
			}
			if len(h.server.holds) != 0 || len(h.wakeServer().acks) != 0 || len(h.wakeRecords()) != 0 {
				t.Fatalf("holds %+v acks %+v", h.server.holds, h.wakeServer().acks)
			}
		})
	}
}

func TestWakeTiming(t *testing.T) {
	t.Run("busy defers until the deadline", func(t *testing.T) {
		h := newHarness(t)
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeBusy)}
		if o := h.deliverWake(h.wake(tick("tk1", tickAt))); o.Kind != Deferred || o.Reason != "busy" {
			t.Fatalf("outcome %+v", o)
		}
		h.wantNoSend()
		h.now = h.now.Add(11 * time.Minute)
		w := h.wake(tick("tk1", tickAt))
		w.BusyDeadline = h.now.Add(-time.Second)
		if o := h.deliverWake(w); o.Kind != Delivered {
			t.Fatalf("at the deadline: %+v", o)
		}
	})
	for name, tail := range map[string][]string{"busy": claudeBusy, "unproven": claudeQuiet} {
		t.Run(name+" with no busyDeadline holds visibly", func(t *testing.T) {
			h := newHarness(t)
			h.orca.screens["term_j"] = []*orcaterm.Screen{screen(tail)}
			w := h.wake(tick("tk1", tickAt))
			w.BusyDeadline = time.Time{}
			if o := h.deliverWake(w); o.Kind != Held || o.Reason != HoldTimingUnavailable {
				t.Fatalf("outcome %+v", o)
			}
			h.wantNoSend()
		})
	}
	t.Run("deliverAfter passed, no busyDeadline, proven idle: typed", func(t *testing.T) {
		h := newHarness(t)
		w := h.wake(tick("tk1", tickAt))
		w.BusyDeadline = time.Time{}
		if o := h.deliverWake(w); o.Kind != Delivered {
			t.Fatalf("outcome %+v", o)
		}
	})
	t.Run("no deliverAfter from the server holds even a proven-idle agent", func(t *testing.T) {
		h := newHarness(t)
		w := h.wake(tick("tk1", tickAt))
		w.DeliverAfter = time.Time{}
		if o := h.deliverWake(w); o.Kind != Held || o.Reason != HoldTimingUnavailable {
			t.Fatalf("outcome %+v", o)
		}
		h.wantNoSend()
		if len(h.server.holds) != 0 || len(h.wakeRecords()) != 0 {
			t.Fatal("a timing hold is local and records nothing")
		}
	})
	t.Run("not yet due, idle", func(t *testing.T) {
		h := newHarness(t)
		w := h.wake(tick("tk1", tickAt))
		w.DeliverAfter = h.now.Add(time.Minute)
		if o := h.deliverWake(w); o.Kind != Deferred || o.Reason != "not-yet-due" {
			t.Fatalf("outcome %+v", o)
		}
		h.wantNoSend()
	})
}

func TestOwnWakeLeftover(t *testing.T) {
	t.Run("the same line is submitted with Enter only", func(t *testing.T) {
		h := newHarness(t)
		h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: claudeIdle, Draft: tickLine}}
		if o := h.deliverWake(h.wake(tick("tk1", tickAt))); o.Kind != Delivered {
			t.Fatalf("outcome %+v", o)
		}
		if s := h.orca.sends[0]; !s.opts.EnterOnly {
			t.Fatalf("retyped instead of submitting: %+v", s)
		}
	})
	t.Run("a batch clears a leftover tick line instead of holding it as a draft", func(t *testing.T) {
		h := newHarness(t)
		h.orca.screens["term_j"] = []*orcaterm.Screen{
			{Source: "screen", Tail: claudeIdle, Draft: tickLine},
			screen(claudeIdle),
		}
		o := h.deliver(h.batch())
		if o.Kind != Delivered || len(h.orca.sends) != 2 || h.orca.sends[0].text != ClearKeys(tickLine) {
			t.Fatalf("outcome %+v sends %+v", o, h.orca.sends)
		}
	})
	t.Run("a wake clears a leftover batch", func(t *testing.T) {
		h := newHarness(t)
		h.orca.screens["term_j"] = []*orcaterm.Screen{
			{Source: "screen", Tail: claudeIdle, Draft: "[hadron chat #4] Ada: old"},
			screen(claudeIdle),
		}
		o := h.deliverWake(h.wake(tick("tk1", tickAt)))
		if o.Kind != Delivered || len(h.orca.sends) != 2 || h.orca.sends[1].text != tickLine {
			t.Fatalf("outcome %+v sends %+v", o, h.orca.sends)
		}
	})
}

func TestAWakeIsNotRecordedUnlessSubmissionIsConfirmed(t *testing.T) {
	h := newHarness(t)
	h.orca.receipts = []orcaterm.Receipt{{Accepted: true}}
	h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeIdle), {Source: "screen", Tail: claudeIdle, Draft: tickLine}}
	o := h.deliverWake(h.wake(tick("tk1", tickAt)))
	if o.Kind != Held || o.Reason != HoldNotSubmitted || len(h.wakeRecords()) != 0 || len(h.wakeServer().acks) != 0 {
		t.Fatalf("outcome %+v records %+v", o, h.wakeRecords())
	}
}

func TestWakesShareTheWorkersLock(t *testing.T) {
	h := newHarness(t)
	err := WithWorkerLock(h.d.LockDir, workerID, func(func() bool) error {
		if o := h.deliverWake(h.wake(tick("tk1", tickAt))); o.Kind != Skipped || o.Reason != "locked" {
			t.Fatalf("outcome %+v", o)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	h.wantNoSend()
}

// --- retention and secrecy --------------------------------------------------------

func TestWakeRecordRetention(t *testing.T) {
	dir := t.TempDir()
	s := &WakeRecords{Dir: dir}
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Typed(workerID, []WakeReason{tick("tk1", tickAt)}, day); err != nil {
		t.Fatal(err)
	}
	if err := s.Typed(workerID, []WakeReason{task("ev1", 0, "T")}, day); err != nil {
		t.Fatal(err)
	}
	if err := s.Acked(workerID, []string{task("ev1", 0, "T").Key()}, day); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(workerID, day.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	recs, err := s.List(workerID)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := recs[tick("tk1", tickAt).Key()]; !ok || r.State != WakeTyped || r.TickerID != "tk1" || !r.TickAt.Equal(tickAt) {
		t.Fatalf("a typed record never expires by age: %+v", recs)
	}
	if _, ok := recs[task("ev1", 0, "T").Key()]; ok {
		t.Fatal("an acked record is pruned after a day")
	}
	if err := s.Typed("../escape", []WakeReason{tick("tk1", tickAt)}, day); err == nil {
		t.Fatal("a path-unsafe worker id must be refused")
	}
}

func TestWakeRecordsAndEventsCarryNoProofTextOrKeys(t *testing.T) {
	h := newHarness(t)
	h.deliverWake(h.wake(tick("tk1", tickAt), task("ev1", 0, "Secret title")))
	err := filepath.WalkDir(filepath.Join(h.state, "delivery", "wakes"), func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		info, _ := e.Info()
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v", p, info.Mode().Perm())
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "Secret title") {
			t.Errorf("%s holds the proof or the text", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range h.events {
		all := fmt.Sprintf("%+v", e)
		if strings.Contains(all, "SECRET") || strings.Contains(all, "Secret") || strings.Contains(all, "tk1") || strings.Contains(all, "ev1") {
			t.Fatalf("an event carries a proof, text or key: %s", all)
		}
	}
	if s := h.wake(tick("tk1", tickAt), task("ev1", 0, "T"), mention()).Span(); s != "wake:tick=1,task=1,mention=1" {
		t.Fatalf("span %q", s)
	}
}

// cli#1229: a mixed wake typed while AckMixed was off reconciles with the
// freshly issued proof after the operator enables A-prime, even after 15m.
func TestAckMixed1229ReconcileUsesFreshProof(t *testing.T) {
	h := newHarness(t)
	w := h.wake(tick("tk1", tickAt), mention())
	w.NamesChannels = true
	if o := h.deliverWake(w); o.Reason != "ack-held" {
		t.Fatal(o)
	}
	h.now = h.now.Add(16 * time.Minute)
	h.d.AckMixed = true
	// A stale proof is refused; neither failure nor success may retype.
	h.wakeServer().errs = []error{errors.New("WAKE_PROOF_STALE")}
	w = h.wake(tick("tk1", tickAt), mention())
	w.Proof = "stale-proof"
	_ = h.deliverWake(w)
	w = h.wake(tick("tk1", tickAt), mention())
	w.Proof = "fresh-after-16m"
	o := h.deliverWake(w)
	if o.Kind != Reconciled || len(h.orca.sends) != 1 {
		t.Fatalf("%+v sends%d", o, len(h.orca.sends))
	}
	a := h.wakeServer().acks
	if len(a) != 2 || a[0] != "stale-proof" || a[1] != "fresh-after-16m" {
		t.Fatal(a)
	}
	h.wantWakeRecord(tick("tk1", tickAt).Key(), WakeAcked)
}
