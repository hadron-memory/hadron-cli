package teamrouter

// The v2 source (cli#829 PR 2), red first. The orca half reuses the ported
// fake (fakeCLIs.orca); the server half is fakeWakes: the main scan's pages,
// then each per-worker fresh re-read just before a send, then the
// acknowledgement. Contract: plan hadron-server:design:named-heartbeats §4.3
// and §7, cor:agt:080:16/:17/:22, and the server's reservation rule (an
// unacknowledged wake is re-handed IDENTICALLY with a fresh proof).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeWakes struct {
	t *testing.T
	// pages served to the main scan, in order; nil = one page of wakes.
	pages [][]Wake
	wakes []Wake
	// fresh overrides the per-worker re-read: worker -> wake (nil = not due now).
	fresh map[string]*Wake
	// freshFails: the re-read errors for these workers.
	freshFails map[string]bool
	status     string
	withheld   int
	dormant    []DormantWorker
	mainFails  bool

	inMain   bool
	mainRefs [][]string
	freshFor []string
	// freshHook runs as a fresh re-read arrives — how a test stages
	// something that happens between the gates and the send.
	freshHook func()
	issued    int
	acked     [][]string
	ackErr    error
	ackHook   func()
}

func newFakeWakes(t *testing.T) *fakeWakes {
	return &fakeWakes{t: t, fresh: map[string]*Wake{}, freshFails: map[string]bool{}, status: "OK"}
}

func (w *fakeWakes) proof() string {
	w.issued++
	return fmt.Sprintf("proof-%d-SECRET", w.issued)
}

func (w *fakeWakes) DueWakes(_ context.Context, refs []string, after string) (WakePage, error) {
	if w.inMain {
		w.mainRefs = append(w.mainRefs, append([]string{}, refs...))
		if w.mainFails {
			return WakePage{}, errors.New("dueWakes down")
		}
		pages := w.pages
		if pages == nil {
			pages = [][]Wake{w.wakes}
		}
		n := 0
		if after != "" {
			if _, err := fmt.Sscanf(after, "c%d", &n); err != nil {
				w.t.Fatalf("bad cursor %q", after)
			}
		}
		page := WakePage{Status: w.status, Withheld: w.withheld, Dormant: w.dormant}
		for _, wk := range pages[n] {
			wk.Proof = w.proof()
			page.Wakes = append(page.Wakes, wk)
		}
		if n+1 < len(pages) {
			page.HasNext, page.EndCursor = true, fmt.Sprintf("c%d", n+1)
		} else {
			w.inMain = false
		}
		return page, nil
	}
	// The fresh re-read: exactly one worker, first page.
	if len(refs) != 1 || after != "" {
		w.t.Fatalf("fresh re-read must name one worker, first page: refs=%v after=%q", refs, after)
	}
	worker := refs[0]
	w.freshFor = append(w.freshFor, worker)
	if w.freshHook != nil {
		w.freshHook()
	}
	if w.freshFails[worker] {
		return WakePage{}, errors.New("fresh dueWakes down")
	}
	page := WakePage{Status: "OK"}
	if f, ok := w.fresh[worker]; ok {
		if f != nil {
			wk := *f
			wk.Proof = w.proof()
			page.Wakes = []Wake{wk}
		}
		return page, nil
	}
	for _, wk := range w.wakes {
		if wk.Worker == worker {
			wk.Proof = w.proof()
			page.Wakes = []Wake{wk}
		}
	}
	return page, nil
}

func (w *fakeWakes) AcknowledgeWakes(_ context.Context, proofs []string) (AckResult, error) {
	w.acked = append(w.acked, append([]string{}, proofs...))
	if w.ackHook != nil {
		w.ackHook()
	}
	if w.ackErr != nil {
		return AckResult{}, w.ackErr
	}
	return AckResult{Acknowledged: len(proofs)}, nil
}

func dueWake(worker, name string, from, through, mention int) Wake {
	m := mention
	return Wake{Worker: worker, WorkerName: name, Unread: through - from + 1, UnreadMentions: 1,
		Channels: []WakeChannel{{ID: channel, Name: "team", FromSeq: from, ThroughSeq: through, LatestMentionSeq: &m}},
		Reasons:  []WakeReason{{Kind: "MENTION"}}}
}

func (w *fakeWakes) poll(t *testing.T, f *fakeCLIs, c *Config, shadow bool) *WakeOutcome {
	t.Helper()
	out, err := w.pollErr(f, c, shadow)
	if err != nil {
		t.Fatalf("pollWakes: %v", err)
	}
	return out
}

func (w *fakeWakes) pollErr(f *fakeCLIs, c *Config, shadow bool) (*WakeOutcome, error) {
	w.inMain = true
	return PollWakes(context.Background(), c, w, f.orca, func(l string) { f.logs = append(f.logs, l) }, nil, shadow)
}

func setupWakes(t *testing.T, names ...string) (*fakeWakes, *fakeCLIs, *Config) {
	t.Helper()
	root := t.TempDir()
	f := newFake(t, root)
	var entries [][2]string
	for _, n := range names {
		entries = append(entries, [2]string{"w-" + strings.ToLower(n), n})
		f.addTerminal(strings.ToLower(n), "claude", "", true)
	}
	return newFakeWakes(t), f, cfgEntries(root, entries...)
}

// ── The happy path ───────────────────────────────────────────────────────

func TestWakesHappyNudgesThenAcksTheFreshProof(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	out := w.poll(t, f, c, false)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("outcome = %+v, want Eli nudged", out)
	}
	if len(f.sent) != 1 || f.sent[0].handle != "term_eli" {
		t.Fatalf("sent = %v", f.sent)
	}
	// The main scan named the roster; the fresh re-read named Eli alone.
	if len(w.mainRefs) != 1 || strings.Join(w.mainRefs[0], ",") != "w-eli" {
		t.Errorf("main scan refs = %v, want the roster", w.mainRefs)
	}
	if strings.Join(w.freshFor, ",") != "w-eli" {
		t.Errorf("fresh re-reads = %v, want [w-eli]", w.freshFor)
	}
	// The proof acknowledged is the FRESH one (#2), never the main scan's (#1):
	// it was issued after the gates, so the 15-minute window starts at the send.
	if len(w.acked) != 1 || strings.Join(w.acked[0], ",") != "proof-2-SECRET" {
		t.Fatalf("acked = %v, want exactly the fresh proof", w.acked)
	}
	// Gates strictly precede the send; the ack strictly follows it.
	if !strings.HasPrefix(firstVerb(f.calls, "terminal wait"), "terminal wait") {
		t.Fatal("no idle gate ran")
	}
}

func firstVerb(calls []string, verb string) string {
	for _, c := range calls {
		if strings.HasPrefix(c, verb) {
			return c
		}
	}
	return ""
}

func TestWakesOffRosterWorkerIsNeverTouched(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-ada", "Ada", 100, 120, 110)}
	out := w.poll(t, f, c, false)
	if len(out.Nudged) != 0 || len(f.sent) != 0 || len(w.acked) != 0 {
		t.Fatalf("an off-roster wake was acted on: %+v sent=%v acked=%v", out, f.sent, w.acked)
	}
}

func TestWakesDrainsEveryPage(t *testing.T) {
	w, f, c := setupWakes(t, "Eli", "Baloo")
	w.pages = [][]Wake{{dueWake("w-eli", "Eli", 100, 120, 110)}, {dueWake("w-baloo", "Baloo", 50, 60, 55)}}
	w.wakes = append(append([]Wake{}, w.pages[0]...), w.pages[1]...)
	out := w.poll(t, f, c, false)
	if strings.Join(out.Nudged, ",") != "Eli,Baloo" {
		t.Fatalf("nudged = %v, want both pages delivered", out.Nudged)
	}
	if len(w.mainRefs) != 2 {
		t.Fatalf("main scan pages read = %d, want 2", len(w.mainRefs))
	}
}

// ── Holds: never typed into, never acknowledged ─────────────────────────

func TestWakesBusyHoldsWithoutAck(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	f.busy["term_eli"] = true
	f.setScreen("term_eli", screenSpec{tail: claudeBusyEmpty}) // #1924: busy needs the live indicator on screen
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	out := w.poll(t, f, c, false)
	if !strings.Contains(out.Held["Eli"], "a running turn is drawn above the prompt") {
		t.Fatalf("held = %v, want the busy hold", out.Held)
	}
	if len(f.sent) != 0 || len(w.acked) != 0 || len(w.freshFor) != 0 {
		t.Fatalf("a held wake must not be re-read, sent or acked: fresh=%v sent=%v acked=%v", w.freshFor, f.sent, w.acked)
	}
}

func TestWakesTypingHoldsWithoutAck(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	f.setScreen("term_eli", screenSpec{tail: withPrompt(claudeIdle, "❯", "Holger typing")})
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	out := w.poll(t, f, c, false)
	if out.Held["Eli"] != "prompt not empty (text in the prompt)" {
		t.Fatalf("held = %v", out.Held)
	}
	if len(f.sent) != 0 || len(w.acked) != 0 {
		t.Fatal("typed into, or acked, a held wake")
	}
	if strings.Contains(strings.Join(f.logs, "\n"), "Holger typing") {
		t.Error("the typed text leaked into the log")
	}
}

func TestWakesUnresolvableHolds(t *testing.T) {
	w, f, c := setupWakes(t)
	c.Roster = append(c.Roster, RosterEntry{Worker: "w-eli", Name: "Eli", Worktree: filepath.Join(t.TempDir(), "eli")})
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	out := w.poll(t, f, c, false)
	if out.Held["Eli"] != "no live agent terminal in its worktree" || len(w.acked) != 0 {
		t.Fatalf("outcome = %+v acked=%v", out, w.acked)
	}
}

func TestWakesRecheckAfterFreshReadBusyHolds(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	f.busyRecheck["term_eli"] = true
	f.setRecheck("term_eli", screenSpec{tail: claudeBusyEmpty}) // #1924: busy needs the live indicator on screen
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	out := w.poll(t, f, c, false)
	if !strings.Contains(out.Held["Eli"], "busy again after the re-read") {
		t.Fatalf("held = %v", out.Held)
	}
	if len(f.sent) != 0 || len(w.acked) != 0 {
		t.Fatal("sent or acked after a failed recheck")
	}
}

func TestWakesRecheckAfterFreshReadTypingHolds(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	typed := "half typed"
	f.setRecheck("term_eli", screenSpec{tail: withPrompt(claudeIdle, "❯", "half typed"), draft: &typed})
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	out := w.poll(t, f, c, false)
	if !strings.Contains(out.Held["Eli"], "prompt not empty on recheck") {
		t.Fatalf("held = %v", out.Held)
	}
	if len(f.sent) != 0 || len(w.acked) != 0 {
		t.Fatal("sent or acked after a failed recheck")
	}
}

// ── The fresh re-read ───────────────────────────────────────────────────

func TestWakesNoLongerDueAtReReadIsDropped(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	w.fresh["w-eli"] = nil // read its chat while the gates ran
	out := w.poll(t, f, c, false)
	if strings.Join(out.Dropped, ",") != "Eli" || len(f.sent) != 0 || len(w.acked) != 0 {
		t.Fatalf("outcome = %+v sent=%v acked=%v, want Eli dropped", out, f.sent, w.acked)
	}
}

func TestWakesSendsTheFreshWakesCounts(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	later := dueWake("w-eli", "Eli", 100, 131, 125)
	w.fresh["w-eli"] = &later
	w.poll(t, f, c, false)
	if len(f.sent) != 1 || !strings.Contains(f.sent[0].text, "#100–#131") || !strings.Contains(f.sent[0].text, "latest mention #125") {
		t.Fatalf("sent = %v, want the fresh range and mention", f.sent)
	}
}

func TestWakesFailedReReadFailsThePollWithoutSending(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	w.freshFails["w-eli"] = true
	if _, err := w.pollErr(f, c, false); err == nil {
		t.Fatal("a failed re-read must fail the poll")
	}
	if len(f.sent) != 0 || len(w.acked) != 0 {
		t.Fatal("sent or acked on a failed re-read")
	}
}

func TestWakesFailedMainScanFailsThePoll(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.mainFails = true
	if _, err := w.pollErr(f, c, false); err == nil {
		t.Fatal("a failed dueWakes must fail the poll")
	}
	if len(f.calls) != 0 {
		t.Fatalf("orca was touched after a failed scan: %v", f.calls)
	}
}

// ── Acknowledgement: never type the same wake twice ─────────────────────

func TestWakesAckFailureIsRecordedAndRecoveredWithoutResending(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	w.ackErr = errors.New("WAKE_PROOF_STALE")
	out := w.poll(t, f, c, false)
	if len(out.Nudged) != 1 || out.AckFailed["Eli"] == "" {
		t.Fatalf("outcome = %+v, want delivered with the ack failure recorded", out)
	}
	if strings.Contains(out.AckFailed["Eli"], "SECRET") {
		t.Error("the proof leaked into the ack-failure reason")
	}
	// The server re-hands the SAME wake until it is acknowledged: the next
	// poll acknowledges it with the new proof and types nothing.
	w.ackErr = nil
	out = w.poll(t, f, c, false)
	if len(f.sent) != 1 {
		t.Fatalf("sends = %d, want 1 — a delivered wake was typed again", len(f.sent))
	}
	if strings.Join(out.Recovered, ",") != "Eli" || len(out.Nudged) != 0 {
		t.Fatalf("second = %+v, want Eli recovered, nobody nudged", out)
	}
	if last := w.acked[len(w.acked)-1]; len(last) != 1 || last[0] == "proof-2-SECRET" {
		t.Fatalf("recovery acked %v, want the new scan's proof", last)
	}
}

func TestWakesSentRecordPersistedBeforeTheAck(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	var sawRecord bool
	w.ackHook = func() {
		blob, err := os.ReadFile(filepath.Join(c.StateDir, "wakes-sent.json"))
		sawRecord = err == nil && strings.Contains(string(blob), "w-eli")
	}
	w.poll(t, f, c, false)
	if !sawRecord {
		t.Fatal("the sent-record must be durable before acknowledgeWakes is called (a crash there must not resend)")
	}
}

func TestWakesNewWakeAfterADeliveredOneIsSent(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	w.ackErr = errors.New("transport")
	w.poll(t, f, c, false)
	w.ackErr = nil
	w.wakes = []Wake{dueWake("w-eli", "Eli", 121, 140, 135)} // a different wake: new range
	out := w.poll(t, f, c, false)
	if strings.Join(out.Nudged, ",") != "Eli" || len(f.sent) != 2 {
		t.Fatalf("outcome = %+v sends=%d, want the new wake delivered", out, len(f.sent))
	}
}

func TestWakesAcknowledgedRecordIsCleared(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	w.poll(t, f, c, false)
	blob, _ := os.ReadFile(filepath.Join(c.StateDir, "wakes-sent.json"))
	if strings.Contains(string(blob), "w-eli") {
		t.Fatalf("an acknowledged wake's record must be cleared: %s", blob)
	}
}

// ── Budget, dormancy, the lock ──────────────────────────────────────────

func TestWakesBudgetAndDormantAreSurfacedAndLoggedOnce(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.status, w.withheld = "WAKE_BUDGET_EXHAUSTED", 3
	w.dormant = []DormantWorker{{Worker: "w-gene", Name: "Gene", Mentions: 2}}
	out := w.poll(t, f, c, false)
	if out.Status != "WAKE_BUDGET_EXHAUSTED" || out.Withheld != 3 || strings.Join(out.Dormant, ",") != "Gene" {
		t.Fatalf("outcome = %+v", out)
	}
	w.poll(t, f, c, false)
	if n := countLines(f.logs, "WAKE_BUDGET_EXHAUSTED"); n != 1 {
		t.Fatalf("budget lines = %d, want 1 (logged once until it changes)", n)
	}
}

func TestWakesLostLockStopsBeforeSendOrAck(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	base := f.orca
	steal := func(ctx context.Context, argv []string) RunResult {
		if strings.Join(argv[0:2], " ") == "terminal read" {
			if err := os.WriteFile(filepath.Join(c.StateDir, ".writer-lock", "owner"), []byte("someone-else"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return base(ctx, argv)
	}
	w.inMain = true
	_, err := PollWakes(context.Background(), c, w, steal, func(string) {}, nil, false)
	if err == nil || !strings.Contains(err.Error(), "lost the writer lock") {
		t.Fatalf("err = %v, want the lost-lock stop", err)
	}
	if len(f.sent) != 0 || len(w.acked) != 0 {
		t.Fatal("sent or acked after losing the lock")
	}
}

func TestWakesLockLostDuringReReadStopsBeforeSend(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	// The lock is taken over AFTER the gates' ownership check: the check
	// right before the send must still catch it.
	w.freshHook = func() {
		if err := os.WriteFile(filepath.Join(c.StateDir, ".writer-lock", "owner"), []byte("someone-else"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.pollErr(f, c, false); err == nil || !strings.Contains(err.Error(), "lost the writer lock") {
		t.Fatalf("err = %v, want the lost-lock stop", err)
	}
	if len(f.sent) != 0 || len(w.acked) != 0 {
		t.Fatal("sent or acked after losing the lock")
	}
}

func TestWakesEmptyRosterNeverReadsTheWholeApp(t *testing.T) {
	w, f, c := setupWakes(t)
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	for _, shadow := range []bool{false, true} {
		if _, err := w.pollErr(f, c, shadow); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.mainRefs) != 0 {
		t.Fatalf("dueWakes was called with refs %v: an empty workerRefs reads every worker of the App", w.mainRefs)
	}
}

// ── Shadow mode ─────────────────────────────────────────────────────────

func TestWakesShadowReadsOnly(t *testing.T) {
	w, f, c := setupWakes(t, "Eli")
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110)}
	out := w.poll(t, f, c, true)
	if len(f.calls) != 0 || len(w.acked) != 0 || len(w.freshFor) != 0 {
		t.Fatalf("shadow touched orca, re-read or acked: calls=%v acked=%v fresh=%v", f.calls, w.acked, w.freshFor)
	}
	if !strings.Contains(out.Would["Eli"], "MENTION") {
		t.Fatalf("would = %v, want Eli with its reason", out.Would)
	}
	if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
		t.Fatalf("shadow wrote state (%v)", err)
	}
}

// ── The nudge text ──────────────────────────────────────────────────────

func TestWakeNudgeTextTeamChannelAnchor(t *testing.T) {
	text := WakeNudgeText(dueWake("w-eli", "Eli", 100, 120, 110), channel)
	for _, want := range []string{"Hadron team attention (1 mention)", "21 relevant unread", "#100–#120", "latest mention #110",
		"START AT #100", "--since 99 --limit 200", "sinceSeq=99", "mark read", "no chat post"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\n") || strings.Contains(text, "proof") {
		t.Errorf("one line, and never the proof:\n%s", text)
	}
}

func TestWakeNudgeTextLargeGapWalksMentions(t *testing.T) {
	text := WakeNudgeText(dueWake("w-eli", "Eli", 61, 9901, 9750), channel)
	for _, want := range []string{"LARGE GAP", "--mentions-me --since 60 --before 9902 --limit 200", "never above #9901", "BACKWARD"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
}

func TestWakeNudgeTextOtherChannelAndReasons(t *testing.T) {
	wk := dueWake("w-eli", "Eli", 100, 120, 110)
	m := 7
	wk.Channels = append(wk.Channels, WakeChannel{ID: "chan-dm", Name: "ada-eli", FromSeq: 5, ThroughSeq: 9, LatestMentionSeq: &m})
	wk.Reasons = []WakeReason{{Kind: "TICK", Ticker: "engineering"}, {Kind: "MENTION"},
		{Kind: "TASK", Task: &WakeTask{Ref: "TASK:t1:assigned", Title: "Port the router", Event: "ASSIGNED"}}}
	text := WakeNudgeText(wk, channel)
	for _, want := range []string{"engineering tick", "task assigned: Port the router (TASK:t1:assigned)",
		"ada-eli #5–#9", "hadron channel read chan-dm --since 4"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
}

func TestWakeNudgeTextTaskOnlyNamesNoChannel(t *testing.T) {
	wk := Wake{Worker: "w-eli", WorkerName: "Eli",
		Reasons: []WakeReason{{Kind: "TASK", Task: &WakeTask{Ref: "TASK:t9:child-finished", Title: "Child done", Event: "CHILD_FINISHED"}}}}
	text := WakeNudgeText(wk, channel)
	if !strings.Contains(text, "task child finished: Child done (TASK:t9:child-finished)") || strings.Contains(text, "START AT") {
		t.Fatalf("text = %s", text)
	}
	if !strings.Contains(text, "hadron team task get t9") {
		t.Errorf("a task wake must say how to read the task: %s", text)
	}
}

// ── Secrets ─────────────────────────────────────────────────────────────

func TestWakesProofNeverLeaks(t *testing.T) {
	w, f, c := setupWakes(t, "Eli", "Baloo")
	f.busy["term_baloo"] = true
	f.setScreen("term_baloo", screenSpec{tail: claudeBusyEmpty}) // #1924: busy needs the live indicator on screen
	w.wakes = []Wake{dueWake("w-eli", "Eli", 100, 120, 110), dueWake("w-baloo", "Baloo", 1, 9, 5)}
	out := w.poll(t, f, c, false)
	outBlob, _ := json.Marshal(out)
	logBlob, _ := json.Marshal(f.logs)
	sentBlob, _ := json.Marshal(f.sent)
	state, _ := os.ReadFile(filepath.Join(c.StateDir, "wakes-sent.json"))
	if blob := string(outBlob) + string(logBlob) + string(sentBlob) + string(state) + strings.Join(f.calls, " "); strings.Contains(blob, "SECRET") {
		t.Fatal("a wake proof leaked into the outcome, logs, sends, argv or state")
	}
}

func TestReadAppKeyFileCustody(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("good", "hdr_app_KEYSECRET\n", 0o600)
	if k, err := ReadAppKeyFile(good); err != nil || k != "hdr_app_KEYSECRET" {
		t.Fatalf("key = %q, err = %v", k, err)
	}
	for name, p := range map[string]string{
		"group-readable": write("loose", "hdr_app_KEYSECRET\n", 0o640),
		"not an App key": write("user", "hdr_user_KEYSECRET\n", 0o600),
		"empty":          write("empty", "\n", 0o600),
		"absent":         filepath.Join(dir, "nope"),
	} {
		_, err := ReadAppKeyFile(p)
		if err == nil {
			t.Errorf("%s: must refuse", name)
			continue
		}
		if strings.Contains(err.Error(), "KEYSECRET") {
			t.Errorf("%s: the refusal quoted the key: %v", name, err)
		}
	}
}
