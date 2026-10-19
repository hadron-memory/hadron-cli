package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// --- fakes ---------------------------------------------------------------

const rule = "────────────────────────────────────────────────────────────"
const claudeHint = "  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents"

var claudeIdle = []string{"⏺ Done.", "✻ Cogitated for 1m 3s", rule, "❯ ", rule, claudeHint}
var claudeBusy = []string{"  (ctrl+b to run in background)", "✽ Bunning… (5m 55s · ↓ 15.6k tokens)", rule, "❯ ", rule, claudeHint}
var claudeQuiet = []string{"  some status output nobody classified", rule, "❯ ", rule, claudeHint}
var codexIdle = []string{"  Worked for 33s • 16:32", "› Ask Codex to do anything", "  GPT-6-Sol high · ~/w · y", "  ? for shortcuts"}

func claudeTyped(text string) []string {
	return []string{"✻ Cogitated for 1m 3s", rule, "❯ " + text, rule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
}

type sendCall struct {
	handle string
	text   string
	opts   orcaterm.SendOptions
}

type fakeOrca struct {
	mu       sync.Mutex
	terms    []orcaterm.Terminal
	screens  map[string][]*orcaterm.Screen // successive reads; the last repeats
	reads    map[string]int
	sends    []sendCall
	receipts []orcaterm.Receipt
	listErr  error
	onSend   func()
}

func (f *fakeOrca) List(context.Context) ([]orcaterm.Terminal, error) {
	return f.terms, f.listErr
}

func (f *fakeOrca) ReadScreen(ctx context.Context, handle string) *orcaterm.Screen {
	if ctx.Err() != nil {
		return nil // a cancelled read fails, as a real call would
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	seq := f.screens[handle]
	if len(seq) == 0 {
		return nil
	}
	i := min(f.reads[handle], len(seq)-1)
	f.reads[handle]++
	return seq[i]
}

func (f *fakeOrca) Send(_ context.Context, handle, text string, o orcaterm.SendOptions) orcaterm.Receipt {
	f.mu.Lock()
	f.sends = append(f.sends, sendCall{handle, text, o})
	rc := orcaterm.Receipt{Accepted: true, TurnStarted: true, Stages: []string{"input_accepted", "turn_started"}}
	if len(f.receipts) > 0 {
		rc, f.receipts = f.receipts[0], f.receipts[1:]
	}
	hook := f.onSend
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return rc
}

type ackCall struct {
	id      string
	through []Through
}

type holdCall struct{ id, reason, terminal string }

type fakeServer struct {
	mu       sync.Mutex
	acks     []ackCall
	ackErrs  []error
	holds    []holdCall
	holdErrs []error
}

func (s *fakeServer) Ack(_ context.Context, id string, through []Through) (AckResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acks = append(s.acks, ackCall{id, through})
	if len(s.ackErrs) > 0 {
		err := s.ackErrs[0]
		s.ackErrs = s.ackErrs[1:]
		if err != nil {
			return "", err
		}
	}
	return Acked, nil
}

func (s *fakeServer) Hold(_ context.Context, id, reason, terminal string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holds = append(s.holds, holdCall{id, reason, terminal})
	if len(s.holdErrs) > 0 {
		err := s.holdErrs[0]
		s.holdErrs = s.holdErrs[1:]
		return err
	}
	return nil
}

type harness struct {
	t      *testing.T
	d      *Deliverer
	orca   *fakeOrca
	server *fakeServer
	now    time.Time
	events []Event
	bound  map[string]string
	state  string
	tree   string
	r      Recipient
}

const workerID = "w-joshua"

func newHarness(t *testing.T) *harness {
	t.Helper()
	state := t.TempDir()
	tree := filepath.Join(t.TempDir(), "joshua")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, state: state, tree: tree, now: time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC),
		bound: map[string]string{tree: workerID}}
	h.orca = &fakeOrca{
		terms:   []orcaterm.Terminal{{Handle: "term_j", WorktreePath: tree, AgentIdentity: ptr("claude"), Connected: true, Writable: true}},
		screens: map[string][]*orcaterm.Screen{"term_j": {screen(claudeIdle)}},
		reads:   map[string]int{},
	}
	h.server = &fakeServer{}
	h.d = New(h.server, h.orca, state)
	h.d.Now = func() time.Time { return h.now }
	h.d.Bound = func(w string) string { return h.bound[w] }
	h.d.Emit = func(e Event) { h.events = append(h.events, e) }
	h.r = Recipient{WorkerID: workerID, Name: "Joshua", TUI: "claude", Worktrees: []string{tree}}
	return h
}

func ptr(s string) *string { return &s }

func screen(tail []string) *orcaterm.Screen { return &orcaterm.Screen{Source: "screen", Tail: tail} }

func (h *harness) batch(seqs ...int) Batch {
	if len(seqs) == 0 {
		seqs = []int{5}
	}
	return Batch{
		ID: "signed-proof-SECRET", BatchKey: fmt.Sprintf("key-%v", seqs), WorkerID: workerID,
		Ranges:       []Range{{ChannelID: "ch-team", Incarnation: 1, FromSeq: seqs[0], ThroughSeq: seqs[len(seqs)-1], Seqs: seqs}},
		Text:         fmt.Sprintf("[hadron chat #%d] Ada: dispatch", seqs[len(seqs)-1]),
		DeliverAfter: h.now.Add(-time.Minute), BusyDeadline: h.now.Add(10 * time.Minute),
		ProofExpiresAt: h.now.Add(15 * time.Minute),
	}
}

func (h *harness) deliver(b Batch) Outcome {
	h.t.Helper()
	return h.d.Deliver(context.Background(), b, h.r)
}

func (h *harness) wantNoSend() {
	h.t.Helper()
	if len(h.orca.sends) != 0 {
		h.t.Fatalf("typed %d time(s): %+v", len(h.orca.sends), h.orca.sends)
	}
}

func (h *harness) wantHold(o Outcome, reason string) {
	h.t.Helper()
	if o.Kind != Held || o.Reason != reason {
		h.t.Fatalf("outcome %+v, want held %s", o, reason)
	}
	if n := len(h.server.holds); n == 0 || h.server.holds[n-1].reason != reason {
		h.t.Fatalf("holds reported %+v, want %s", h.server.holds, reason)
	}
	if len(h.server.acks) != 0 {
		h.t.Fatalf("a hold acked: %+v", h.server.acks)
	}
}

// ack is the i-th acknowledgement, failing cleanly when there is none.
func (h *harness) ack(i int) ackCall {
	h.t.Helper()
	if i < 0 {
		i += len(h.server.acks)
	}
	if i < 0 || i >= len(h.server.acks) {
		h.t.Fatalf("no acknowledgement %d: %+v", i, h.server.acks)
	}
	return h.server.acks[i]
}

func (h *harness) records() []Record {
	h.t.Helper()
	recs, err := h.d.Records.List(workerID)
	if err != nil {
		h.t.Fatal(err)
	}
	return recs
}

// --- the happy path --------------------------------------------------------

func TestDeliversIntoAProvenEmptyComposerAndAcksOnce(t *testing.T) {
	h := newHarness(t)
	o := h.deliver(h.batch())
	if o.Kind != Delivered || len(h.orca.sends) != 1 || len(h.server.acks) != 1 {
		t.Fatalf("outcome %+v sends %d acks %d", o, len(h.orca.sends), len(h.server.acks))
	}
	if s := h.orca.sends[0]; s.text != h.batch().Text || s.opts.Interrupt || s.opts.EnterOnly {
		t.Fatalf("sent %+v", s)
	}
	if a := h.ack(0); a.through != nil {
		t.Fatalf("a fresh delivery acks whole: %+v", a)
	}
	if len(h.records()) != 0 {
		t.Fatal("a landed ack must delete its record")
	}
}

// --- checklist 1: questions, menus, approval prompts --------------------------

func TestNeverTypesIntoAQuestion(t *testing.T) {
	h := newHarness(t)
	h.orca.screens["term_j"] = []*orcaterm.Screen{screen([]string{"Do you want to proceed?", "❯ 1. Yes", "  2. No"})}
	h.wantHold(h.deliver(h.batch()), HoldQuestion)
	h.wantNoSend()
}

// --- checklist 2: empty input proven from the screen's shape ---------------------

func TestUnprovenScreensHold(t *testing.T) {
	cases := map[string]func(h *harness){
		"a stream read is not a screen": func(h *harness) {
			h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "stream", Tail: claudeIdle}}
		},
		"the screen shows a different interface than registered": func(h *harness) {
			h.orca.screens["term_j"] = []*orcaterm.Screen{screen(codexIdle)}
		},
		"Orca's identity disagrees with the screen": func(h *harness) {
			h.r.TUI = ""
			h.orca.terms[0].AgentIdentity = ptr("codex")
		},
		"a disconnected terminal": func(h *harness) { h.orca.terms[0].Connected = false },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			set(h)
			h.wantHold(h.deliver(h.batch()), HoldScreenUnknown)
			h.wantNoSend()
		})
	}
	t.Run("a bare shell in the worktree is never a recipient", func(t *testing.T) {
		h := newHarness(t)
		h.orca.terms[0].AgentIdentity = nil
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen([]string{"orca$ ls", "orca$ "})}
		h.wantHold(h.deliver(h.batch()), HoldScreenUnknown)
		h.wantNoSend()
	})
}

// --- checklist 3: drafts (R2) ----------------------------------------------------

const canary = "DRAFT-CANARY-7d1f"

func TestForeignDraftsAreHeldNeverClearedNeverStored(t *testing.T) {
	cases := map[string]*orcaterm.Screen{
		"Orca's draft field": {Source: "screen", Tail: claudeIdle, Draft: canary},
		"a Claude screen that proves the box empty (#6736, inverted)": {Source: "screen", Tail: claudeIdle, Draft: "ghost " + canary},
		"a human's text in the box":                                   screen(claudeTyped("please also " + canary)),
	}
	for name, sc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.d.Caps = Capabilities{EnterOnly: map[string]bool{"claude": true}, Clear: map[string]bool{"claude": true}}
			h.orca.screens["term_j"] = []*orcaterm.Screen{sc}
			o := h.deliver(h.batch())
			h.wantHold(o, HoldDraft)
			h.wantNoSend() // never cleared: not even a clearing keystroke
			h.wantNoCanary(o)
		})
	}
}

func (h *harness) wantNoCanary(o Outcome) {
	h.t.Helper()
	blob, _ := json.Marshal(struct {
		O Outcome
		E []Event
		H []holdCall
		S []sendCall
	}{o, h.events, h.server.holds, h.orca.sends})
	if strings.Contains(string(blob), "CANARY") {
		h.t.Fatalf("draft text escaped: %s", blob)
	}
	_ = filepath.Walk(h.state, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "CANARY") {
				h.t.Fatalf("draft text stored in %s", p)
			}
		}
		return nil
	})
}

func TestOwnLeftover(t *testing.T) {
	t.Run("the current batch already typed: Enter-only once proven, nothing retyped", func(t *testing.T) {
		h := newHarness(t)
		h.d.Caps.EnterOnly = map[string]bool{"claude": true}
		b := h.batch()
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeTyped(b.Text))}
		o := h.deliver(b)
		if o.Kind != Delivered || len(h.orca.sends) != 1 || !h.orca.sends[0].opts.EnterOnly {
			t.Fatalf("outcome %+v sends %+v", o, h.orca.sends)
		}
	})
	t.Run("the current batch already typed, Enter-only unproven: held not-submitted", func(t *testing.T) {
		h := newHarness(t)
		h.d.Caps = Capabilities{}
		b := h.batch()
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeTyped(b.Text))}
		h.wantHold(h.deliver(b), HoldNotSubmitted)
		h.wantNoSend()
	})
	t.Run("an older leftover of ours, clear unproven: held not-submitted", func(t *testing.T) {
		h := newHarness(t)
		h.d.Caps = Capabilities{}
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeTyped("[hadron chat #1] an older batch"))}
		h.wantHold(h.deliver(h.batch()), HoldNotSubmitted)
		h.wantNoSend()
	})
	t.Run("an older leftover of ours, clear proven: cleared, proven empty, then sent", func(t *testing.T) {
		h := newHarness(t)
		h.d.Caps.Clear = map[string]bool{"claude": true}
		older := "[hadron chat #1] an older batch — über"
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeTyped(older)), screen(claudeIdle)}
		o := h.deliver(h.batch())
		if o.Kind != Delivered || len(h.orca.sends) != 2 || !h.orca.sends[0].opts.NoEnter || h.orca.sends[1].text != h.batch().Text {
			t.Fatalf("outcome %+v sends %+v", o, h.orca.sends)
		}
		if h.orca.sends[0].text != ClearKeys(older) {
			t.Fatalf("clear sent %q, want Backspace per rune of the leftover", h.orca.sends[0].text)
		}
	})
	t.Run("a clear that does not leave a proven-empty composer holds", func(t *testing.T) {
		h := newHarness(t)
		h.d.Caps.Clear = map[string]bool{"claude": true}
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeTyped("[hadron chat #1] an older batch"))}
		h.wantHold(h.deliver(h.batch()), HoldNotSubmitted)
		if len(h.orca.sends) != 1 || !h.orca.sends[0].opts.NoEnter {
			t.Fatalf("only the clear may be sent: %+v", h.orca.sends)
		}
	})
}

// The 2026-10-18 scratch terminal (cli#907) proved, on Claude Code 2.1.303
// and Codex CLI 0.163.0: Enter alone submits an existing composer, and
// Backspace once per rune empties one, multi-line included, while doing
// nothing to an empty composer. Ctrl+C is never a clear: ONE on an empty
// Codex composer quits Codex, so a human emptying it between our read and
// our send would cost the worker its agent.
func TestProvenCapabilitiesAreTheDefault(t *testing.T) {
	d := New(&fakeServer{}, &fakeOrca{}, t.TempDir())
	for _, family := range []string{"claude", "codex"} {
		if !d.Caps.EnterOnly[family] || !d.Caps.Clear[family] {
			t.Errorf("%s: caps %+v, want Enter-only and clear proven", family, d.Caps)
		}
	}
}

func TestClearKeysAreBackspacePerRuneNeverCtrlC(t *testing.T) {
	for _, leftover := range []string{"[hadron chat #1] x", "[hadron chat #1] über\nsecond line", ""} {
		keys := ClearKeys(leftover)
		if keys != strings.Repeat("\x7f", utf8.RuneCountInString(leftover)) {
			t.Errorf("%q: keys %q", leftover, keys)
		}
		if strings.ContainsAny(keys, "\x03\x1b\x15") {
			t.Errorf("%q: keys %q carry Ctrl+C, Esc or Ctrl+U", leftover, keys)
		}
	}
}

// --- checklist 4: interrupt only when the batch says so --------------------------

func TestInterruptOnlyWhenTheBatchSaysSo(t *testing.T) {
	for _, want := range []bool{false, true} {
		h := newHarness(t)
		b := h.batch()
		b.Interrupt = want
		h.deliver(b)
		if len(h.orca.sends) != 1 || h.orca.sends[0].opts.Interrupt != want {
			t.Fatalf("Interrupt=%v sent %+v", want, h.orca.sends)
		}
	}
}

// --- checklist 5: delivered means confirmed; only then ack, never retype ------------

func TestConfirmation(t *testing.T) {
	t.Run("input accepted and our text gone from the composer is confirmed", func(t *testing.T) {
		h := newHarness(t)
		h.orca.receipts = []orcaterm.Receipt{{Accepted: true, Stages: []string{"input_accepted"}}}
		if o := h.deliver(h.batch()); o.Kind != Delivered || len(h.server.acks) != 1 {
			t.Fatalf("outcome %+v acks %+v", o, h.server.acks)
		}
	})
	t.Run("input accepted but our text still in the composer: held, no record, no ack", func(t *testing.T) {
		h := newHarness(t)
		b := h.batch()
		h.orca.receipts = []orcaterm.Receipt{{Accepted: true, Stages: []string{"input_accepted"}}}
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeIdle), screen(claudeTyped(b.Text))}
		h.wantHold(h.deliver(b), HoldNotSubmitted)
		if len(h.records()) != 0 {
			t.Fatal("an unconfirmed send must leave no record")
		}
		// Next time the leftover is ours and current: submitted with Enter
		// alone (proven, cli#907 scratch terminal), never retyped.
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeTyped(b.Text)), screen(claudeIdle)}
		h.orca.reads["term_j"] = 0
		if o := h.deliver(b); o.Kind != Delivered {
			t.Fatalf("outcome %+v", o)
		}
		if len(h.orca.sends) != 2 || !h.orca.sends[1].opts.EnterOnly {
			t.Fatalf("want one Enter-only send after the first: %+v", h.orca.sends)
		}
	})
	t.Run("the same, with Enter-only unproven: the leftover holds, never retyped", func(t *testing.T) {
		h := newHarness(t)
		h.d.Caps = Capabilities{}
		b := h.batch()
		h.orca.receipts = []orcaterm.Receipt{{Accepted: true, Stages: []string{"input_accepted"}}}
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeIdle), screen(claudeTyped(b.Text))}
		h.wantHold(h.deliver(b), HoldNotSubmitted)
		h.orca.reads["term_j"] = 1
		h.wantHold(h.deliver(b), HoldNotSubmitted)
		if len(h.orca.sends) != 1 {
			t.Fatalf("retyped: %+v", h.orca.sends)
		}
	})
	t.Run("a send Orca did not accept holds, with no record", func(t *testing.T) {
		h := newHarness(t)
		h.orca.receipts = []orcaterm.Receipt{{ErrCode: "terminal_handle_stale"}}
		h.wantHold(h.deliver(h.batch()), HoldNotSubmitted)
		if len(h.records()) != 0 {
			t.Fatal("record written for an unaccepted send")
		}
	})
}

// --- checklist 6: busy may delay to the cap, never past it --------------------------

func TestBusyDefersUntilTheDeadlineAndNoLater(t *testing.T) {
	for _, c := range []struct {
		name   string
		tail   []string
		offset time.Duration
		typed  bool
	}{
		{"busy, one second before the deadline: deferred", claudeBusy, -time.Second, false},
		{"busy, at the deadline: typed", claudeBusy, 0, true},
		{"busy, past the deadline: typed", claudeBusy, time.Second, true},
		{"unproven liveness, before the deadline: deferred", claudeQuiet, -time.Second, false},
		{"unproven liveness, at the deadline: typed", claudeQuiet, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			b := h.batch()
			h.now = b.BusyDeadline.Add(c.offset)
			b.ProofExpiresAt = h.now.Add(15 * time.Minute)
			h.orca.screens["term_j"] = []*orcaterm.Screen{screen(c.tail)}
			o := h.deliver(b)
			if typed := len(h.orca.sends) == 1; typed != c.typed {
				t.Fatalf("outcome %+v typed=%v", o, typed)
			}
			if !c.typed && (o.Kind != Deferred || o.Reason != "busy") {
				t.Fatalf("outcome %+v", o)
			}
		})
	}
	t.Run("a question still holds past the deadline", func(t *testing.T) {
		h := newHarness(t)
		b := h.batch()
		h.now = b.BusyDeadline.Add(time.Minute)
		b.ProofExpiresAt = h.now.Add(15 * time.Minute)
		h.orca.screens["term_j"] = []*orcaterm.Screen{screen([]string{"Do you want to proceed?", "❯ 1. Yes"})}
		h.wantHold(h.deliver(b), HoldQuestion)
	})
}

// --- checklist 7: the server's text is checked before typing ------------------------

func TestMalformedBatchesAreNeitherTypedNorAckedNorHeld(t *testing.T) {
	for name, mutate := range map[string]func(*Batch){
		"a newline would submit early": func(b *Batch) { b.Text += "\nmore" },
		"no batch prefix":              func(b *Batch) { b.Text = "Ada: dispatch" },
		"too long":                     func(b *Batch) { b.Text += strings.Repeat("x", MaxTextRunes) },
		"a seq outside its range":      func(b *Batch) { b.Ranges[0].Seqs = []int{99} },
		"no ranges":                    func(b *Batch) { b.Ranges = nil },
		"another worker's batch":       func(b *Batch) { b.WorkerID = "w-other" },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			b := h.batch()
			mutate(&b)
			if o := h.deliver(b); o.Kind != Failed {
				t.Fatalf("outcome %+v", o)
			}
			h.wantNoSend()
			if len(h.server.acks)+len(h.server.holds) != 0 {
				t.Fatalf("acks %+v holds %+v", h.server.acks, h.server.holds)
			}
		})
	}
}

// --- 2.2 finding the recipient ---------------------------------------------------

func TestFindingTheRecipient(t *testing.T) {
	t.Run("two agents in the worktree hold", func(t *testing.T) {
		h := newHarness(t)
		h.orca.terms = append(h.orca.terms, orcaterm.Terminal{Handle: "term_k", WorktreePath: h.tree, AgentIdentity: ptr("claude"), Connected: true, Writable: true})
		h.orca.screens["term_k"] = []*orcaterm.Screen{screen(claudeIdle)}
		h.wantHold(h.deliver(h.batch()), HoldTwoAgents)
		h.wantNoSend()
	})
	t.Run("an agent and a shell: the agent is the one", func(t *testing.T) {
		h := newHarness(t)
		h.orca.terms = append(h.orca.terms, orcaterm.Terminal{Handle: "term_sh", WorktreePath: h.tree, Connected: true, Writable: true})
		h.orca.screens["term_sh"] = []*orcaterm.Screen{screen([]string{"orca$ "})}
		if o := h.deliver(h.batch()); o.Kind != Delivered || h.orca.sends[0].handle != "term_j" {
			t.Fatalf("outcome %+v sends %+v", o, h.orca.sends)
		}
	})
	t.Run("a binding naming another worker holds", func(t *testing.T) {
		h := newHarness(t)
		h.bound[h.tree] = "w-someone-else"
		h.wantHold(h.deliver(h.batch()), HoldBindingMismatch)
		h.wantNoSend()
	})
	t.Run("no binding at all holds", func(t *testing.T) {
		h := newHarness(t)
		delete(h.bound, h.tree)
		h.wantHold(h.deliver(h.batch()), HoldBindingMismatch)
	})
	t.Run("no terminal in a registered worktree: skipped, no hold invented", func(t *testing.T) {
		h := newHarness(t)
		h.orca.terms[0].WorktreePath = "/elsewhere"
		if o := h.deliver(h.batch()); o.Kind != Skipped || o.Reason != "no-terminal" {
			t.Fatalf("outcome %+v", o)
		}
		if len(h.server.holds) != 0 {
			t.Fatalf("holds %+v", h.server.holds)
		}
	})
	t.Run("a symlinked worktree path matches", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks")
		}
		h := newHarness(t)
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(h.tree, link); err != nil {
			t.Fatal(err)
		}
		h.orca.terms[0].WorktreePath = link
		h.bound[link] = workerID
		if o := h.deliver(h.batch()); o.Kind != Delivered {
			t.Fatalf("outcome %+v", o)
		}
	})
	t.Run("a failed list is an error, nothing typed", func(t *testing.T) {
		h := newHarness(t)
		h.orca.listErr = orcaterm.ErrOrca
		if o := h.deliver(h.batch()); o.Kind != Failed {
			t.Fatalf("outcome %+v", o)
		}
		h.wantNoSend()
	})
}

// --- 2.4 batch identity (Anne's #885 P2 as a regression) -------------------------------

func TestLostAckResponseReconcilesWithoutRetyping(t *testing.T) {
	h := newHarness(t)
	h.server.ackErrs = []error{errors.New("transport: response lost")}
	b := h.batch(5, 6)
	if o := h.deliver(b); o.Kind != Delivered || !o.AckPending {
		t.Fatalf("outcome %+v", o)
	}
	if len(h.records()) != 1 {
		t.Fatal("a confirmed send whose ack failed must keep its record")
	}
	// S1 re-hands the same messages under a fresh proof.
	again := b
	again.ID = "signed-proof-2"
	if o := h.deliver(again); o.Kind != Reconciled {
		t.Fatalf("outcome %+v", o)
	}
	if len(h.orca.sends) != 1 {
		t.Fatalf("retyped after a lost ack: %+v", h.orca.sends)
	}
	if len(h.records()) != 0 {
		t.Fatal("the reconciling ack must delete the record")
	}
}

func TestADistinctLaterMessageIsTypedNotAckedAsOld(t *testing.T) {
	h := newHarness(t)
	h.server.ackErrs = []error{errors.New("lost")}
	h.deliver(h.batch(5))
	later := h.batch(9) // a later post: a seq the record never saw
	later.ID = "signed-proof-2"
	if o := h.deliver(later); o.Kind != Delivered || len(h.orca.sends) != 2 {
		t.Fatalf("outcome %+v sends %d", o, len(h.orca.sends))
	}
	if last := h.ack(-1); last.id != "signed-proof-2" || last.through != nil {
		t.Fatalf("ack %+v", last)
	}
}

func TestARecreatedChannelIsNeverAckedFromTheOldIncarnation(t *testing.T) {
	h := newHarness(t)
	h.server.ackErrs = []error{errors.New("lost")}
	h.deliver(h.batch(1, 2))
	fresh := h.batch(1, 2) // same id, same seqs, a new incarnation
	fresh.ID, fresh.BatchKey = "signed-proof-2", "key-new-incarnation"
	fresh.Ranges[0].Incarnation = 2
	if o := h.deliver(fresh); o.Kind != Delivered || len(h.orca.sends) != 2 {
		t.Fatalf("outcome %+v sends %d: the new incarnation's messages must be typed", o, len(h.orca.sends))
	}
}

func TestLockLostBeforeTheSendTypesNothing(t *testing.T) {
	h := newHarness(t)
	h.orca.screens["term_j"] = []*orcaterm.Screen{screen(claudeIdle)}
	// Steal the lock while the screen is read: the deliverer must notice.
	h.d.Bound = func(w string) string {
		lock := filepath.Join(h.d.LockDir, workerID+".lock")
		_ = os.WriteFile(filepath.Join(lock, "owner"), []byte(`{"pid":1}`+"\nthief"), 0o600)
		return h.bound[w]
	}
	if o := h.deliver(h.batch()); o.Kind != Deferred || o.Reason != "lock-lost-before-send" {
		t.Fatalf("outcome %+v", o)
	}
	h.wantNoSend()
}

func TestLockLostAfterTheSendStillRecordsAndAcksAndNeverResends(t *testing.T) {
	h := newHarness(t)
	h.server.ackErrs = []error{errors.New("lost")}
	h.orca.onSend = func() {
		lock := filepath.Join(h.d.LockDir, workerID+".lock")
		_ = os.WriteFile(filepath.Join(lock, "owner"), []byte(`{"pid":1}`+"\nthief"), 0o600)
	}
	o := h.deliver(h.batch())
	if o.Kind != Delivered || len(h.orca.sends) != 1 || len(h.server.acks) != 1 {
		t.Fatalf("outcome %+v sends %d acks %d", o, len(h.orca.sends), len(h.server.acks))
	}
	if len(h.records()) != 1 {
		t.Fatal("the known outcome must be recorded even after losing the lock")
	}
}

// --- 2.5 covered-prefix recovery (S1 #2017, S2 #2018 accepted) -------------------------------

func seedRecord(t *testing.T, h *harness, ranges ...Range) {
	t.Helper()
	if err := h.d.Records.Put(Batch{WorkerID: workerID, Ranges: ranges}, h.now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func rng(ch string, inc int, seqs ...int) Range {
	return Range{ChannelID: ch, Incarnation: inc, FromSeq: seqs[0], ThroughSeq: seqs[len(seqs)-1], Seqs: seqs}
}

func TestCoveredPrefixRecovery(t *testing.T) {
	t.Run("a re-hand that extends the record acks the record's end, types nothing", func(t *testing.T) {
		h := newHarness(t)
		seedRecord(t, h, rng("ch-team", 1, 5, 6))
		if o := h.deliver(h.batch(5, 6, 7)); o.Kind != Reconciled || o.Reason != "covered-prefix" {
			t.Fatalf("outcome %+v", o)
		}
		h.wantNoSend()
		if got := h.ack(0).through; len(got) != 1 || got[0] != (Through{"ch-team", 6}) {
			t.Fatalf("through %+v", got)
		}
	})
	t.Run("a foreign seq inside stops the prefix before it", func(t *testing.T) {
		h := newHarness(t)
		seedRecord(t, h, rng("ch-team", 1, 5, 7))
		h.deliver(h.batch(5, 6, 7))
		if got := h.ack(0).through; len(got) != 1 || got[0].Seq != 5 {
			t.Fatalf("through %+v", got)
		}
	})
	t.Run("a record that lacks the batch's first seq proves nothing: typed", func(t *testing.T) {
		h := newHarness(t)
		seedRecord(t, h, rng("ch-team", 1, 6, 7))
		if o := h.deliver(h.batch(5, 6, 7)); o.Kind != Delivered || len(h.orca.sends) != 1 {
			t.Fatalf("outcome %+v", o)
		}
	})
	t.Run("a record that starts earlier still proves the seqs it confirmed: no retype", func(t *testing.T) {
		h := newHarness(t)
		seedRecord(t, h, rng("ch-team", 1, 4, 5, 6))
		if o := h.deliver(h.batch(5, 6, 7)); o.Kind != Reconciled {
			t.Fatalf("outcome %+v", o)
		}
		h.wantNoSend()
		if got := h.ack(0).through; len(got) != 1 || got[0].Seq != 6 {
			t.Fatalf("through %+v", got)
		}
	})
	t.Run("a disjoint record proves nothing: typed", func(t *testing.T) {
		h := newHarness(t)
		seedRecord(t, h, rng("ch-team", 1, 1, 2))
		if o := h.deliver(h.batch(5)); o.Kind != Delivered {
			t.Fatalf("outcome %+v", o)
		}
	})
	t.Run("a multi-channel batch: the unrecorded channel is omitted and stays due", func(t *testing.T) {
		h := newHarness(t)
		seedRecord(t, h, rng("ch-team", 1, 5))
		b := h.batch(5)
		b.Ranges = append(b.Ranges, rng("ch-lane", 1, 40))
		h.deliver(b)
		h.wantNoSend()
		if got := h.ack(0).through; len(got) != 1 || got[0].ChannelID != "ch-team" {
			t.Fatalf("through %+v: ch-lane must be omitted", got)
		}
	})
	t.Run("a refused through rolls back: record kept, nothing typed", func(t *testing.T) {
		h := newHarness(t)
		seedRecord(t, h, rng("ch-team", 1, 5, 6))
		h.server.ackErrs = []error{errors.New("BAD_USER_INPUT")}
		if o := h.deliver(h.batch(5, 6, 7)); o.Kind != Failed {
			t.Fatalf("outcome %+v", o)
		}
		h.wantNoSend()
		if len(h.records()) != 1 {
			t.Fatal("a refused reconciliation must keep the record")
		}
	})
}

// Jane's blind P2 on #939 (issuecomment-6262509785): a full reconcile acks
// only the current batch, so a matched record that also confirmed other
// seqs or channels keeps them. Deleting it whole would retype them when S1
// re-hands them (e.g. a lost ack, then a read whose channel policy left one
// channel out for a while).
func TestAFullReconcileKeepsTheRecordsOtherSeqs(t *testing.T) {
	h := newHarness(t)
	if err := h.d.Records.Put(h.batch(5, 6), h.now); err != nil {
		t.Fatal(err)
	}
	if o := h.deliver(h.batch(5)); o.Kind != Reconciled {
		t.Fatalf("first: %+v", o)
	}
	if o := h.deliver(h.batch(6)); o.Kind != Reconciled {
		t.Fatalf("the confirmed remainder was retyped: %+v", o)
	}
	h.wantNoSend()
	if len(h.server.acks) != 2 || len(h.records()) != 0 {
		t.Fatalf("acks %+v records %+v", h.server.acks, h.records())
	}
}

func TestAFullReconcileKeepsTheRecordsOtherChannels(t *testing.T) {
	h := newHarness(t)
	a, b := h.batch(5), h.batch(6)
	b.Ranges[0].ChannelID = "ch-other"
	both := a
	both.Ranges = append(append([]Range{}, a.Ranges...), b.Ranges...)
	both.BatchKey = "two-channel-confirmed"
	if err := h.d.Records.Put(both, h.now); err != nil {
		t.Fatal(err)
	}
	if o := h.deliver(a); o.Kind != Reconciled {
		t.Fatalf("first: %+v", o)
	}
	// The ack named only a's channel: nothing is acked for a channel the
	// proof did not carry.
	if len(h.server.acks) != 1 || len(h.server.acks[0].through) != 0 {
		t.Fatalf("acks %+v", h.server.acks)
	}
	recs := h.records()
	if len(recs) != 1 || len(recs[0].Ranges) != 1 || recs[0].Ranges[0].ChannelID != "ch-other" {
		t.Fatalf("the remainder %+v, want ch-other's range alone", recs)
	}
	if o := h.deliver(b); o.Kind != Reconciled {
		t.Fatalf("the confirmed other channel was retyped: %+v", o)
	}
	h.wantNoSend()
	if len(h.records()) != 0 {
		t.Fatalf("records %+v", h.records())
	}
}

// Controls: an exact match is deleted whole, and a record of another
// incarnation of the same channel is untouched by the subtraction.
func TestAFullReconcileDeletesAnExactRecordAndKeepsOtherIncarnations(t *testing.T) {
	h := newHarness(t)
	old := h.batch(5)
	old.Ranges[0].Incarnation = 0
	old.BatchKey = "old-incarnation"
	if err := h.d.Records.Put(h.batch(5), h.now); err != nil {
		t.Fatal(err)
	}
	if err := h.d.Records.Put(old, h.now); err != nil {
		t.Fatal(err)
	}
	if o := h.deliver(h.batch(5)); o.Kind != Reconciled {
		t.Fatalf("outcome %+v", o)
	}
	recs := h.records()
	if len(recs) != 1 || recs[0].Ranges[0].Incarnation != 0 || !slices.Equal(recs[0].Ranges[0].Seqs, []int{5}) {
		t.Fatalf("records %+v, want only the other incarnation's, whole", recs)
	}
}

func TestRetireSubtractsOnlyTheSameChannelAndIncarnation(t *testing.T) {
	got := subtractRanges([]Range{rng("c", 0, 5), rng("c", 1, 5, 6), rng("d", 1, 5)}, []Range{rng("c", 1, 5)})
	want := []Range{rng("c", 0, 5), {ChannelID: "c", Incarnation: 1, FromSeq: 5, ThroughSeq: 6, Seqs: []int{6}}, rng("d", 1, 5)}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].ChannelID != want[i].ChannelID || got[i].Incarnation != want[i].Incarnation || !slices.Equal(got[i].Seqs, want[i].Seqs) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
	if rest := subtractRanges([]Range{rng("c", 1, 5)}, []Range{rng("c", 1, 4, 5)}); len(rest) != 0 {
		t.Fatalf("a fully acked record leaves %+v", rest)
	}
}

func TestReconcileMatchesOnlyTheSameIncarnation(t *testing.T) {
	b := Batch{Ranges: []Range{rng("c", 2, 1, 2)}}
	if c := Reconcile(b, []Record{{State: RecordConfirmed, Ranges: []Range{rng("c", 1, 1, 2)}}}); c.Full || len(c.Through) != 0 {
		t.Fatalf("an earlier incarnation's record covered %+v", c)
	}
	if c := Reconcile(b, []Record{{State: RecordConfirmed, Ranges: []Range{rng("c", 2, 1, 2)}}}); !c.Full {
		t.Fatalf("the same incarnation's record must cover: %+v", c)
	}
}

func TestReconcileNeverSendsAnXThatIsNotARenderedSeq(t *testing.T) {
	b := Batch{Ranges: []Range{rng("c", 1, 3, 8, 12)}}
	for _, recSeqs := range [][]int{{3}, {3, 8}, {3, 8, 12}, {3, 4, 5, 6, 7, 8}, {3, 12}} {
		c := Reconcile(b, []Record{{State: RecordConfirmed, Ranges: []Range{rng("c", 1, recSeqs...)}}})
		for _, th := range c.Through {
			if th.Seq != 3 && th.Seq != 8 && th.Seq != 12 {
				t.Fatalf("record %v: through %d is not a rendered seq", recSeqs, th.Seq)
			}
		}
	}
}

// --- expiry budget, timing, stopping -------------------------------------------------

func TestTimingGates(t *testing.T) {
	t.Run("a proof with less than the budget left is re-read, not typed on", func(t *testing.T) {
		h := newHarness(t)
		b := h.batch()
		b.ProofExpiresAt = h.now.Add(DefaultBudget - time.Second)
		if o := h.deliver(b); o.Kind != Deferred || o.Reason != "proof-near-expiry" {
			t.Fatalf("outcome %+v", o)
		}
		h.wantNoSend()
	})
	t.Run("the budget covers send, confirm, record and ack", func(t *testing.T) {
		if DefaultBudget < 2*orcaterm.CallTimeout+AckTimeout {
			t.Fatalf("budget %s", DefaultBudget)
		}
	})
	t.Run("a batch not yet due is skipped", func(t *testing.T) {
		h := newHarness(t)
		b := h.batch()
		b.DeliverAfter = h.now.Add(time.Second)
		if o := h.deliver(b); o.Kind != Deferred || o.Reason != "not-yet-due" {
			t.Fatalf("outcome %+v", o)
		}
	})
	t.Run("stopping before the send types nothing", func(t *testing.T) {
		h := newHarness(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if o := h.d.Deliver(ctx, h.batch(), h.r); o.Kind != Deferred {
			t.Fatalf("outcome %+v", o)
		}
		h.wantNoSend()
	})
	t.Run("stopping during the send still confirms, records and acks", func(t *testing.T) {
		h := newHarness(t)
		ctx, cancel := context.WithCancel(context.Background())
		h.orca.onSend = cancel
		// Accepted without a turn start: the confirm read must still run.
		h.orca.receipts = []orcaterm.Receipt{{Accepted: true, Stages: []string{"input_accepted"}}}
		if o := h.d.Deliver(ctx, h.batch(), h.r); o.Kind != Delivered || len(h.server.acks) != 1 {
			t.Fatalf("outcome %+v acks %d", o, len(h.server.acks))
		}
	})
}

// --- holds are reported once per change ------------------------------------------------

func TestHoldsAreReportedOncePerReasonChange(t *testing.T) {
	h := newHarness(t)
	b := h.batch()
	h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: claudeIdle, Draft: "x"}}
	h.deliver(b)
	h.deliver(b)
	if len(h.server.holds) != 1 {
		t.Fatalf("holds %+v", h.server.holds)
	}
	h.orca.screens["term_j"] = []*orcaterm.Screen{screen([]string{"Do you want to proceed?", "❯ 1. Yes"})}
	h.orca.reads["term_j"] = 0
	h.deliver(b)
	if len(h.server.holds) != 2 || h.server.holds[1].reason != HoldQuestion {
		t.Fatalf("holds %+v", h.server.holds)
	}
}

// cli#940 / Xan's split (#907, 2026-10-19 05:11Z): until S5 is on the
// server, the adapter answers ErrHoldUnavailable without a call. The hold is
// Held and unreported: never Failed, never acked, never recorded as
// delivered, and never cached as reported, so it is reported once the
// capability is there.
func TestAnUnavailableHoldReportIsHeldUnreported(t *testing.T) {
	h := newHarness(t)
	h.server.holdErrs = []error{ErrHoldUnavailable}
	h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: claudeIdle, Draft: canary}}
	o := h.deliver(h.batch())
	if o.Kind != Held || o.Reason != HoldDraft || !o.Unreported {
		t.Fatalf("outcome %+v, want held draft, unreported", o)
	}
	h.wantNoSend()
	if len(h.server.acks) != 0 || len(h.records()) != 0 {
		t.Fatalf("an unreported hold acked %+v or recorded %+v", h.server.acks, h.records())
	}
	if n := len(h.events); n == 0 || !h.events[n-1].Unreported || h.events[n-1].Kind != Held {
		t.Fatalf("events %+v", h.events)
	}
	h.wantNoCanary(o)
}

func TestAnUnreportedHoldIsReportedOnceTheCapabilityIsThere(t *testing.T) {
	h := newHarness(t)
	h.server.holdErrs = []error{ErrHoldUnavailable, ErrHoldUnavailable}
	h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: claudeIdle, Draft: "x"}}
	b := h.batch()
	// S1 re-hands the held batch: each replay holds again, still unreported.
	for i := range 2 {
		if o := h.deliver(b); o.Kind != Held || !o.Unreported {
			t.Fatalf("replay %d: %+v", i, o)
		}
	}
	// The capability arrives: the same hold is now reported, once.
	if o := h.deliver(b); o.Kind != Held || o.Unreported {
		t.Fatalf("outcome %+v, want held and reported", o)
	}
	h.deliver(b)
	if len(h.server.holds) != 3 {
		t.Fatalf("holds %+v: unavailable must not count as reported, and a reported hold is reported once", h.server.holds)
	}
	h.wantNoSend()
	if len(h.server.acks) != 0 {
		t.Fatalf("acks %+v", h.server.acks)
	}
}

func TestARealHoldReportErrorIsFailedNotUnavailable(t *testing.T) {
	cases := map[string]struct {
		err        error
		kind       Kind
		unreported bool
	}{
		"transport":           {errors.New("transport: connection reset"), Failed, false},
		"a server refusal":    {errors.New("BAD_USER_INPUT"), Failed, false},
		"wrapped unavailable": {fmt.Errorf("adapter: %w", ErrHoldUnavailable), Held, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.server.holdErrs = []error{c.err}
			h.orca.screens["term_j"] = []*orcaterm.Screen{{Source: "screen", Tail: claudeIdle, Draft: "x"}}
			o := h.deliver(h.batch())
			if o.Kind != c.kind || o.Unreported != c.unreported {
				t.Fatalf("outcome %+v, want %s unreported=%v", o, c.kind, c.unreported)
			}
			if c.kind == Failed && o.Reason != "hold-report-failed:"+HoldDraft {
				t.Fatalf("reason %q", o.Reason)
			}
		})
	}
}

func TestReconcileIsUnchangedWhenHoldReportsAreUnavailable(t *testing.T) {
	h := newHarness(t)
	h.server.ackErrs = []error{errors.New("transport: response lost")}
	h.server.holdErrs = []error{ErrHoldUnavailable}
	b := h.batch(5, 6)
	if o := h.deliver(b); o.Kind != Delivered || !o.AckPending {
		t.Fatalf("outcome %+v", o)
	}
	again := b
	again.ID = "signed-proof-2"
	if o := h.deliver(again); o.Kind != Reconciled {
		t.Fatalf("outcome %+v", o)
	}
	if len(h.orca.sends) != 1 || len(h.server.holds) != 0 || len(h.records()) != 0 {
		t.Fatalf("sends %+v holds %+v records %+v", h.orca.sends, h.server.holds, h.records())
	}
}

// --- locking -----------------------------------------------------------------------

func TestASecondDelivererForTheSameWorkerSkipsAndNeverWaits(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		_ = WithWorkerLock(h.d.LockDir, workerID, func(func() bool) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	o := h.deliver(h.batch())
	close(release)
	if o.Kind != Skipped || o.Reason != "locked" {
		t.Fatalf("outcome %+v", o)
	}
	h.wantNoSend()
}

func TestAStaleLockIsBroken(t *testing.T) {
	h := newHarness(t)
	lock := filepath.Join(h.d.LockDir, workerID+".lock")
	if err := os.MkdirAll(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	// pid 2^31-2 is never live.
	if err := os.WriteFile(filepath.Join(lock, "owner"), []byte(`{"pid":2147483646,"startedAt":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if o := h.deliver(h.batch()); o.Kind != Delivered {
		t.Fatalf("outcome %+v", o)
	}
}

func TestPathSafeWorkerIDs(t *testing.T) {
	for _, id := range []string{"../escape", "a/b", "", strings.Repeat("x", 200)} {
		if err := WithWorkerLock(t.TempDir(), id, func(func() bool) error { return nil }); err == nil {
			t.Errorf("%q accepted", id)
		}
	}
}

// --- records ------------------------------------------------------------------------

func TestRecordsHoldNoTextNoProofAndArePrivate(t *testing.T) {
	h := newHarness(t)
	h.server.ackErrs = []error{errors.New("lost")}
	b := h.batch()
	b.Text = "[hadron chat #5] RECORD-CANARY"
	h.deliver(b)
	dir := filepath.Join(h.d.Records.Dir, workerID)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("entries %v", entries)
	}
	data, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if strings.Contains(string(data), "CANARY") || strings.Contains(string(data), "SECRET") {
		t.Fatalf("record carries text or proof: %s", data)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, entries[0].Name()))
		di, _ := os.Stat(dir)
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Fatalf("modes %v %v", fi.Mode().Perm(), di.Mode().Perm())
		}
	}
}

func TestOnlyConfirmedTypedRecordsCount(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.d.Records.Dir, workerID)
	_ = os.MkdirAll(dir, 0o700)
	ranges := []Range{rng("ch-team", 1, 5)}
	for _, body := range []string{
		`{"state":"pending","workerId":"w-joshua","ranges":[{"channelId":"ch-team","incarnation":1,"fromSeq":5,"throughSeq":5,"seqs":[5]}]}`,
		`{"state":"confirmed","workerId":"w-other","ranges":[{"channelId":"ch-team","incarnation":1,"fromSeq":5,"throughSeq":5,"seqs":[5]}]}`,
		`not json`,
	} {
		_ = os.WriteFile(filepath.Join(dir, recordName(ranges)), []byte(body), 0o600)
		if recs, _ := h.d.Records.List(workerID); len(recs) != 0 {
			t.Fatalf("%s counted", body)
		}
	}
	if o := h.deliver(h.batch(5)); o.Kind != Delivered {
		t.Fatalf("an untrusted record suppressed a send: %+v", o)
	}
}

func TestPrune(t *testing.T) {
	h := newHarness(t)
	seedRecord(t, h, rng("ch-team", 1, 5))
	if err := h.d.Records.Prune(workerID, h.now); err != nil {
		t.Fatal(err)
	}
	if len(h.records()) != 0 {
		t.Fatal("an old record survived Prune")
	}
}

// --- output -------------------------------------------------------------------------

func TestEventsCarryNoTextAndNoProof(t *testing.T) {
	h := newHarness(t)
	b := h.batch()
	b.Text = "[hadron chat #5] EVENT-CANARY"
	h.deliver(b)
	blob, _ := json.Marshal(h.events)
	if strings.Contains(string(blob), "CANARY") || strings.Contains(string(blob), "SECRET") {
		t.Fatalf("events %s", blob)
	}
	if len(h.events) == 0 || h.events[0].Span != "ch-team#1:5-5" {
		t.Fatalf("events %+v", h.events)
	}
}

func TestBoundWorkerReadsALinkedWorktreesBinding(t *testing.T) {
	root := t.TempDir()
	gitdir := filepath.Join(root, "repo.git", "worktrees", "j")
	tree := filepath.Join(root, "j")
	_ = os.MkdirAll(gitdir, 0o755)
	_ = os.MkdirAll(tree, 0o755)
	_ = os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(gitdir, bindingFileName), []byte(`{"workerId":"w-joshua"}`), 0o600)
	if got := BoundWorker(tree); got != "w-joshua" {
		t.Fatalf("got %q", got)
	}
	if got := BoundWorker(filepath.Join(root, "nowhere")); got != "" {
		t.Fatalf("got %q", got)
	}
}

// cli#906 row 6: when Orca reports no agent identity, the recipient's family
// comes from the screen. Events say which, as a bounded code; it grants
// nothing (the agreement rule still decides).
func TestEventsSayHowTheTerminalWasResolved(t *testing.T) {
	for want, identity := range map[string]*string{"identity": ptr("claude"), "screen": nil} {
		t.Run(want, func(t *testing.T) {
			h := newHarness(t)
			h.orca.terms[0].AgentIdentity = identity
			if o := h.deliver(h.batch()); o.Kind != Delivered {
				t.Fatalf("outcome %+v", o)
			}
			last := h.events[len(h.events)-1]
			if last.ResolvedBy != want {
				t.Fatalf("event %+v, want resolvedBy %q", last, want)
			}
		})
	}
	h := newHarness(t)
	h.orca.terms = nil
	h.deliver(h.batch())
	if e := h.events[len(h.events)-1]; e.ResolvedBy != "" {
		t.Fatalf("no terminal found, yet resolvedBy %q", e.ResolvedBy)
	}
}
