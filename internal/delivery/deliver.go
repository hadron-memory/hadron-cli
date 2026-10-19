package delivery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// AckTimeout bounds one acknowledgement or hold report, which is sent even
// while stopping.
const AckTimeout = 30 * time.Second

// DefaultBudget is the expiry budget: everything after the screen checks
// must fit inside the proof's remaining life — a send with --wait-submit
// and a confirm read (one orca call bound each), the record write, and the
// ack — plus slack. A proof with less left is re-read, not typed on. This
// narrows the stale-proof window; it does not close it (network delay, a
// slow server), and a stale ack after a confirmed send is reconciled from
// the record, never re-typed.
const DefaultBudget = 2*orcaterm.CallTimeout + AckTimeout + 30*time.Second

// Terminals is the orca surface delivery uses; *orcaterm.Client is one.
type Terminals interface {
	List(ctx context.Context) ([]orcaterm.Terminal, error)
	ReadScreen(ctx context.Context, handle string) *orcaterm.Screen
	Send(ctx context.Context, handle, text string, o orcaterm.SendOptions) orcaterm.Receipt
}

// Recipient is a worker registered on this host, as the poller's latest
// registration resolved it.
type Recipient struct {
	WorkerID string
	Name     string
	// TUI is the agent interface its terminal must show ("claude" or
	// "codex"), when the host knows it. Empty means the screen's own family
	// and Orca's agent identity must agree instead.
	TUI string
	// Worktrees are its registered worktrees, absolute.
	Worktrees []string

	// resolvedBy is set by Deliver once the terminal is found (Event).
	resolvedBy string
}

// Capabilities are what has been PROVEN on a scratch terminal, per TUI
// family. Until proven, the path it would enable holds instead.
type Capabilities struct {
	// EnterOnly: an Enter-only send submits what the composer holds.
	EnterOnly map[string]bool
	// Clear: ClearKeys empties the family's composer.
	Clear map[string]bool
}

// Proven is what the 2026-10-18 scratch terminal proved on Claude Code
// 2.1.303 and Codex CLI 0.163.0 (cli#907; hadron-dev-team-joshua:work:
// cli907-scratch-terminal-evidence-2026-10-18): Enter alone submits an
// existing composer, and ClearKeys empties one, multi-line included.
func Proven() Capabilities {
	return Capabilities{
		EnterOnly: map[string]bool{"claude": true, "codex": true},
		Clear:     map[string]bool{"claude": true, "codex": true},
	}
}

// ClearKeys empties a composer holding leftover: one Backspace per rune.
// It does nothing to an empty composer, so a human who empties it between
// our read and our send loses nothing. Never Ctrl+C: ONE on an empty Codex
// composer quits Codex. Never Ctrl+U (one line only) or Esc Esc (Claude
// opens its Rewind dialog on an empty composer).
func ClearKeys(leftover string) string {
	return strings.Repeat("\x7f", utf8.RuneCountInString(leftover))
}

// Kind is a delivery's outcome.
type Kind string

const (
	Delivered  Kind = "delivered"  // typed and confirmed
	Reconciled Kind = "reconciled" // acknowledged from a confirmed record, nothing typed
	Held       Kind = "held"       // reported to S5 with a reason
	Deferred   Kind = "deferred"   // left due: try again next time
	Skipped    Kind = "skipped"    // left due, without a hold (no terminal, lock busy)
	Failed     Kind = "error"      // a contract or transport error; left due
)

// Outcome is what one Deliver did. Reason is a code; nothing here carries
// text, a proof or screen content.
type Outcome struct {
	Kind     Kind
	Reason   string
	Terminal string
	// AckPending: delivered and recorded, but the ack did not land; the
	// record reconciles it next time without typing.
	AckPending bool
	// Unreported: Held, but the server was not told (ErrHoldUnavailable).
	// No server hold, episode, alarm or escalation exists for it.
	Unreported bool
}

// Event is one line of what delivery did, for the operator.
type Event struct {
	Time     time.Time
	Worker   string
	WorkerID string
	Span     string
	Kind     Kind
	Reason   string
	Terminal string
	// ResolvedBy says where the recipient terminal's agent family came
	// from: "identity" (Orca's agent identity) or "screen" (Orca reported
	// none, and the screen showed one). Empty before a terminal is found. A
	// diagnostic code only (cli#906): it grants nothing.
	ResolvedBy string
	// Wake: the event is a tick or task wake's (cli#906 row 1), not a
	// batch's. Span is then reason counts.
	Wake bool
	// Unreported: a hold the server was not told about (cli#940).
	Unreported bool
}

// Deliverer delivers batches into Orca terminals.
type Deliverer struct {
	Server  Server
	Orca    Terminals
	Records *Records
	// LockDir holds the per-worker locks shared with C2.
	LockDir string
	Caps    Capabilities
	// Bound is the worker a worktree's session binding names, or "".
	Bound  func(worktree string) string
	Now    func() time.Time
	Budget time.Duration
	Emit   func(Event)

	// Wakes acknowledges wake proofs; nil leaves wakes unacknowledged.
	Wakes       WakeServer
	WakeRecords *WakeRecords
	// AckMixed is set only once A′ (hadron-server#2072) is verified
	// deployed: a wake carrying a MENTION, or naming channels, may then be
	// acknowledged once all its TICK and TASK reasons are typed. Off, C1
	// acknowledges only a proof with no MENTION reason and no channels.
	AckMixed bool

	mu sync.Mutex
	// reported is the last hold reason reported per batch, so a hold is
	// reported once per change, not every poll.
	reported map[string]string
}

// New builds a Deliverer with the production defaults.
func New(server Server, orca Terminals, stateDir string) *Deliverer {
	return &Deliverer{
		Server: server, Orca: orca,
		Records:     &Records{Dir: filepath.Join(stateDir, "delivery", "records")},
		WakeRecords: &WakeRecords{Dir: filepath.Join(stateDir, "delivery", "wakes")},
		LockDir:     filepath.Join(stateDir, "delivery", "locks"),
		Bound:       BoundWorker, Now: time.Now, Budget: DefaultBudget,
		Caps: Proven(),
	}
}

func (d *Deliverer) emit(b Batch, r Recipient, o Outcome) Outcome {
	if d.Emit != nil {
		d.Emit(Event{Time: d.Now().UTC(), Worker: r.Name, WorkerID: r.WorkerID, Span: b.Span(), Kind: o.Kind, Reason: o.Reason, Terminal: o.Terminal, ResolvedBy: r.resolvedBy, Unreported: o.Unreported})
	}
	return o
}

func batchKey(b Batch) string {
	if b.BatchKey != "" {
		return b.BatchKey
	}
	return recordName(b.Ranges)
}

// hold reports a hold once per (batch, reason) change. A server without the
// hold report (ErrHoldUnavailable) leaves it Held and unreported, and
// uncached, so a later step reports it.
func (d *Deliverer) hold(ctx context.Context, b Batch, r Recipient, reason, terminal string) Outcome {
	key := batchKey(b)
	d.mu.Lock()
	if d.reported == nil {
		d.reported = map[string]string{}
	}
	same := d.reported[key] == reason
	d.mu.Unlock()
	if !same {
		hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), AckTimeout)
		err := d.Server.Hold(hctx, b.ID, reason, terminal)
		cancel()
		if errors.Is(err, ErrHoldUnavailable) {
			return d.emit(b, r, Outcome{Kind: Held, Reason: reason, Terminal: terminal, Unreported: true})
		}
		if err != nil {
			return d.emit(b, r, Outcome{Kind: Failed, Reason: "hold-report-failed:" + reason, Terminal: terminal})
		}
		d.mu.Lock()
		d.reported[key] = reason
		d.mu.Unlock()
	}
	return d.emit(b, r, Outcome{Kind: Held, Reason: reason, Terminal: terminal})
}

func (d *Deliverer) forget(b Batch) {
	d.mu.Lock()
	delete(d.reported, batchKey(b))
	d.mu.Unlock()
}

// Deliver carries one batch to its recipient's terminal, under the worker's
// lock. The order is the design (cli#907 attach §2 and deltas 2–4):
// reconcile confirmed records first (never type what was confirmed), check
// the batch and the proof's remaining life, find exactly one agent terminal
// in a registered worktree with a matching binding, judge its composer under
// R2 and its liveness, then send, confirm, record, and ack.
func (d *Deliverer) Deliver(ctx context.Context, b Batch, r Recipient) Outcome {
	var out Outcome
	err := WithWorkerLock(d.LockDir, r.WorkerID, func(stillOwner func() bool) error {
		out = d.deliverLocked(ctx, b, r, stillOwner)
		return nil
	})
	if errors.Is(err, ErrBusy) {
		return d.emit(b, r, Outcome{Kind: Skipped, Reason: "locked"})
	}
	if err != nil {
		return d.emit(b, r, Outcome{Kind: Failed, Reason: "lock"})
	}
	return out
}

func (d *Deliverer) deliverLocked(ctx context.Context, b Batch, r Recipient, stillOwner func() bool) Outcome {
	if b.WorkerID != r.WorkerID {
		return d.emit(b, r, Outcome{Kind: Failed, Reason: "foreign-worker"})
	}
	if err := CheckBatch(b); err != nil {
		return d.emit(b, r, Outcome{Kind: Failed, Reason: "malformed-batch"})
	}
	if o, done := d.reconcile(ctx, b, r); done {
		return o
	}
	now := d.Now()
	if now.Before(b.DeliverAfter) {
		return d.emit(b, r, Outcome{Kind: Deferred, Reason: "not-yet-due"})
	}
	if b.ProofExpiresAt.Sub(now) < d.Budget {
		return d.emit(b, r, Outcome{Kind: Deferred, Reason: "proof-near-expiry"})
	}
	if ctx.Err() != nil {
		return d.emit(b, r, Outcome{Kind: Deferred, Reason: "stopping"})
	}

	l := line{
		text: b.Text, interrupt: b.Interrupt, busyDeadline: b.BusyDeadline,
		emit: func(o Outcome) Outcome { return d.emit(b, r, o) },
		hold: func(reason, terminal string) Outcome { return d.hold(ctx, b, r, reason, terminal) },
	}
	handle, o, ok := d.typeLine(ctx, l, &r, stillOwner)
	if !ok {
		return o
	}
	fin := context.WithoutCancel(ctx)
	// Confirmed. The record is written even if the lock was lost meanwhile:
	// it is the known outcome, and every deliverer reconciles records before
	// typing.
	if err := d.Records.Put(b, d.Now()); err != nil {
		return d.emit(b, r, Outcome{Kind: Delivered, Reason: "record-failed", Terminal: handle, AckPending: !d.ack(fin, b, nil)})
	}
	d.forget(b)
	if !d.ack(fin, b, nil) {
		return d.emit(b, r, Outcome{Kind: Delivered, Reason: "ack-pending", Terminal: handle, AckPending: true})
	}
	_ = d.Records.Delete(b.WorkerID, b.Ranges)
	return d.emit(b, r, Outcome{Kind: Delivered, Terminal: handle})
}

// ack acknowledges a batch, bounded. True when it landed (or had already).
func (d *Deliverer) ack(ctx context.Context, b Batch, through []Through) bool {
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), AckTimeout)
	defer cancel()
	res, err := d.Server.Ack(actx, b.ID, through)
	return err == nil && (res == Acked || res == Unchanged)
}

// reconcile acks what confirmed records prove, with no typing. done is true
// when the batch needs nothing further this time.
func (d *Deliverer) reconcile(ctx context.Context, b Batch, r Recipient) (Outcome, bool) {
	recs, err := d.Records.List(b.WorkerID)
	if err != nil {
		return d.emit(b, r, Outcome{Kind: Failed, Reason: "records-unreadable"}), true
	}
	cover := Reconcile(b, recs)
	switch {
	case cover.Full:
		if !d.ack(ctx, b, nil) {
			return d.emit(b, r, Outcome{Kind: Failed, Reason: "reconcile-ack-failed"}), true
		}
		for _, m := range cover.Matched {
			_ = d.Records.Delete(b.WorkerID, m.Ranges)
		}
		d.forget(b)
		return d.emit(b, r, Outcome{Kind: Reconciled}), true
	case len(cover.Through) > 0:
		// A covered prefix: ack exactly it; the rest arrives as the next
		// batch. A refused entry rolls the whole ack back on the server, so
		// on failure nothing moved and the record stays.
		if !d.ack(ctx, b, cover.Through) {
			return d.emit(b, r, Outcome{Kind: Failed, Reason: "reconcile-ack-failed"}), true
		}
		d.forget(b)
		return d.emit(b, r, Outcome{Kind: Reconciled, Reason: "covered-prefix"}), true
	}
	return Outcome{}, false
}

// line is what one delivery types, and how its outcomes are reported: a
// batch reports its holds to S5, a wake (cli#906) only logs them.
type line struct {
	text      string
	interrupt bool
	// busyDeadline is when a working or unproven agent stops deferring the
	// line. deadlineRequired: a zero one holds as timing-unavailable rather
	// than typing at once.
	busyDeadline     time.Time
	deadlineRequired bool
	emit             func(Outcome) Outcome
	hold             func(reason, terminal string) Outcome
}

// typeLine finds r's terminal, judges its composer under R2 and its
// liveness, and types l.text. ok is true only when the text was sent and
// provably submitted; otherwise o is the outcome to return. r learns how
// its terminal was found.
func (d *Deliverer) typeLine(ctx context.Context, l line, r *Recipient, stillOwner func() bool) (handle string, o Outcome, ok bool) {
	term, sc, family, o, ok := d.recipientTerminal(ctx, l, *r)
	if !ok {
		return "", o, false
	}
	// Every later event of this delivery says how the terminal was found.
	r.resolvedBy = "identity"
	if term.Identity() == "" {
		r.resolvedBy = "screen"
	}
	state, leftover := orcaterm.ReadComposer(sc, family, OwnPrefixes...)
	enterOnly := false
	switch state {
	case orcaterm.ComposerEmpty:
	case orcaterm.ComposerDraft:
		return "", l.hold(HoldDraft, term.Handle), false
	case orcaterm.ComposerQuestion:
		return "", l.hold(HoldQuestion, term.Handle), false
	case orcaterm.ComposerOwn:
		if orcaterm.Normalize(leftover) == orcaterm.Normalize(l.text) {
			// Our current text is already typed: submit it, never retype.
			if !d.Caps.EnterOnly[family] {
				return "", l.hold(HoldNotSubmitted, term.Handle), false
			}
			enterOnly = true
		} else if o, cleared := d.clearOwn(ctx, l, term.Handle, family, leftover, stillOwner); !cleared {
			return "", o, false
		}
	default:
		return "", l.hold(HoldScreenUnknown, term.Handle), false
	}

	if live, _ := orcaterm.IdleProof(sc.Tail, family); live != orcaterm.Idle {
		// A working (or unproven) agent defers the line, but never past the
		// busy-delay cap: at the deadline a proven-empty composer is typed
		// into, as both agents queue input during a turn.
		if l.deadlineRequired && l.busyDeadline.IsZero() {
			return "", l.hold(HoldTimingUnavailable, term.Handle), false
		}
		if d.Now().Before(l.busyDeadline) {
			return "", l.emit(Outcome{Kind: Deferred, Reason: "busy", Terminal: term.Handle}), false
		}
	}

	if !stillOwner() || ctx.Err() != nil {
		return "", l.emit(Outcome{Kind: Deferred, Reason: "lock-lost-before-send", Terminal: term.Handle}), false
	}
	// Past this point a send may have happened, and a send cannot be undone:
	// the rest finishes even while stopping, each step bounded.
	fin := context.WithoutCancel(ctx)
	rc := d.Orca.Send(fin, term.Handle, l.text, orcaterm.SendOptions{Interrupt: l.interrupt, EnterOnly: enterOnly})
	if !rc.Accepted {
		return "", l.hold(HoldNotSubmitted, term.Handle), false
	}
	if !rc.TurnStarted {
		after := d.Orca.ReadScreen(fin, term.Handle)
		if st, _ := orcaterm.ReadComposer(after, family, OwnPrefixes...); st != orcaterm.ComposerEmpty {
			// Accepted but not provably submitted: no record, never re-typed.
			// The next attempt finds our own leftover.
			return "", l.hold(HoldNotSubmitted, term.Handle), false
		}
	}
	return term.Handle, Outcome{}, true
}

// clearOwn empties a composer holding our own OLDER leftover with
// ClearKeys, and re-reads to prove it empty. Orca has no clear operation
// (hadron-cli:findings:orca-terminal-has-no-clear-operation); a family whose
// clear is not proven holds instead.
func (d *Deliverer) clearOwn(ctx context.Context, l line, handle, family, leftover string, stillOwner func() bool) (Outcome, bool) {
	if !d.Caps.Clear[family] || leftover == "" || !stillOwner() {
		return l.hold(HoldNotSubmitted, handle), false
	}
	if rc := d.Orca.Send(ctx, handle, ClearKeys(leftover), orcaterm.SendOptions{NoEnter: true}); !rc.Accepted {
		return l.hold(HoldNotSubmitted, handle), false
	}
	sc := d.Orca.ReadScreen(ctx, handle)
	if st, _ := orcaterm.ReadComposer(sc, family, OwnPrefixes...); st != orcaterm.ComposerEmpty {
		return l.hold(HoldNotSubmitted, handle), false
	}
	return Outcome{}, true
}

// recipientTerminal finds the one agent terminal for r, looked up fresh: in
// a registered worktree, an agent (never a shell), one of them only, bound
// to r, showing the expected interface.
func (d *Deliverer) recipientTerminal(ctx context.Context, l line, r Recipient) (orcaterm.Terminal, *orcaterm.Screen, string, Outcome, bool) {
	var none orcaterm.Terminal
	terms, err := d.Orca.List(ctx)
	if err != nil {
		return none, nil, "", l.emit(Outcome{Kind: Failed, Reason: "orca-list"}), false
	}
	var inTree []orcaterm.Terminal
	for _, t := range terms {
		if inWorktrees(t.WorktreePath, r.Worktrees) {
			inTree = append(inTree, t)
		}
	}
	if len(inTree) == 0 {
		// No hold evidence: S5 judges an overdue backlog by itself.
		return none, nil, "", l.emit(Outcome{Kind: Skipped, Reason: "no-terminal"}), false
	}
	type agentTerm struct {
		t      orcaterm.Terminal
		sc     *orcaterm.Screen
		family string
	}
	var agents []agentTerm
	for _, t := range inTree {
		sc := d.Orca.ReadScreen(ctx, t.Handle)
		fam := orcaterm.ScreenAgentFamily(sc)
		if fam == "" && t.Identity() == "" {
			continue // a shell: never a recipient
		}
		agents = append(agents, agentTerm{t, sc, fam})
	}
	switch {
	case len(agents) > 1:
		return none, nil, "", l.hold(HoldTwoAgents, ""), false
	case len(agents) == 0:
		return none, nil, "", l.hold(HoldScreenUnknown, inTree[0].Handle), false
	}
	a := agents[0]
	if !a.t.Connected || !a.t.Writable {
		return none, nil, "", l.hold(HoldScreenUnknown, a.t.Handle), false
	}
	if d.Bound(a.t.WorktreePath) != r.WorkerID {
		return none, nil, "", l.hold(HoldBindingMismatch, a.t.Handle), false
	}
	// The interface: the screen's own family, else Orca's identity (a dialog
	// can hide the agent's chrome). Falling back is safe: without that chrome
	// the composer is never proven empty, so the screen holds, under its
	// real reason. Two disagreeing signals hold.
	family, id := a.family, a.t.Identity()
	if family == "" {
		family = id
	}
	want := r.TUI
	if want == "" {
		want = id
	}
	if family == "" || family != want || (id != "" && a.family != "" && id != a.family) {
		return none, nil, "", l.hold(HoldScreenUnknown, a.t.Handle), false
	}
	return a.t, a.sc, family, Outcome{}, true
}

// inWorktrees compares a terminal's worktree with the registered ones,
// cleaned and with symlinks resolved.
func inWorktrees(path string, trees []string) bool {
	if path == "" {
		return false
	}
	p := canonical(path)
	for _, t := range trees {
		if canonical(t) == p {
			return true
		}
	}
	return false
}

func canonical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}

// bindingFileName is the worktree session binding `hadron team session
// start` writes into the worktree's git directory.
const bindingFileName = "hadron-team-session.json"

// BoundWorker is the worker id a worktree's session binding names, or "".
func BoundWorker(worktree string) string {
	gitDir := filepath.Join(worktree, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		b, err := os.ReadFile(gitDir)
		if err != nil {
			return ""
		}
		pointer := trimGitdir(string(b))
		if filepath.IsAbs(pointer) {
			gitDir = pointer
		} else {
			gitDir = filepath.Join(worktree, pointer)
		}
	}
	return readBinding(filepath.Join(gitDir, bindingFileName))
}
