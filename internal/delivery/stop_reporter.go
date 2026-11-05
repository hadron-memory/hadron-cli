package delivery

// The dropped-halt reporter (hadron-cli#1629, the reporting path of
// hadron-server#2169). The poller records a halt notice it could not type in
// the StopOutbox; this reporter flags each record to the halted worker's
// coordinator with one team-chat post, authored by the reporter worker's own
// session binding.
//
// The delivery contract (CHECKED on #1629, Vera's FIT 6628260482) is AT MOST
// ONE post per notice and never silently zero, except one named residual; it
// is NOT exactly-once. Five mechanisms carry it:
//
//  1. One writer: a non-blocking flock on stop-dropped/.reporter.lock. A run
//     that finds it held does nothing.
//  2. Intent before send: <name>.sending holds the frozen body and the
//     channel head seq, written durably BEFORE the first post. No post is
//     ever made without it.
//  3. Reconcile before any re-send: with an intent already on disk, the
//     channel is read forward from that head for a post by the reporter
//     worker with exactly that body. Found: it is recorded and nothing is
//     posted. Read failed: nothing is posted. The reconcile waits until the
//     intent is ReconcileAfter old, so a request still in flight is not
//     mistaken for a missing one.
//  4. The server's duplicate rule: a worker's identical post in the same
//     channel within ten minutes is refused TEAM_CHAT_DUPLICATE with the
//     earlier seq, which is recorded as this notice's post.
//  5. An accepted (or DUPLICATE) seq and its author are persisted in the
//     intent before read-back. Once recorded, retries read only that seq;
//     a mismatch stays failed and pending for a person, never a resend.
//
// <name>.reported is written only after the seq is read back with the
// reporter worker as its author and exactly the frozen body.
//
// The residual: a post the server commits more than ReconcileAfter after it
// was sent and after the next reconcile read, and more than ten minutes after
// the first attempt or in an encrypted channel (no equality read on
// ciphertext), can be posted twice. A crash or storage failure between an
// accepted answer and its durable seq also leaves the earlier unknown-answer
// intent; the new bound begins once the seq is durably recorded. Never zero:
// an unreported record stays.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultReconcileAfter is how old an intent must be before it is reconciled
// and possibly re-sent: well past the CLI's 30 s request timeout.
const DefaultReconcileAfter = 3 * time.Minute

// ErrNoReporterBinding: there is no live session bound to the reporter
// worker, so nothing may be posted. Records stay pending.
var ErrNoReporterBinding = errors.New("no live reporter binding")

// ChatMessage is the part of a team-chat message the reporter compares.
type ChatMessage struct {
	Seq            int
	AuthorWorkerID string
	Body           string
}

// DuplicatePostError is the server's TEAM_CHAT_DUPLICATE refusal: an
// identical post by this worker already exists at EarlierSeq.
type DuplicatePostError struct{ EarlierSeq int }

func (e *DuplicatePostError) Error() string {
	return fmt.Sprintf("identical to the earlier post #%d", e.EarlierSeq)
}

// ReporterAPI is what the reporter needs from the server. The CLI implements
// it over the worktree's worker-session binding; tests fake it.
type ReporterAPI interface {
	// BoundWorker answers the worker of the LIVE session bound for this
	// reporter. It errors when there is no binding or the session ended.
	BoundWorker(ctx context.Context) (workerID, workerName string, err error)
	// Recipient is the mention token (no "@") of whom to flag for a halted
	// worker: its reportsTo worker, else the person holding it.
	Recipient(ctx context.Context, workerID string) (string, error)
	// Head is the channel's newest seq (0 when empty).
	Head(ctx context.Context) (int, error)
	// Post posts body as the bound worker and answers its seq. A duplicate
	// refusal is a *DuplicatePostError.
	Post(ctx context.Context, body string) (int, error)
	// MessagesAfter answers messages with seq > after, ascending, at most
	// limit; an empty page means none remain.
	MessagesAfter(ctx context.Context, after, limit int) ([]ChatMessage, error)
}

// stopSending is the durable intent written before the first post.
type stopSending struct {
	EventID        string    `json:"eventId"`
	Body           string    `json:"body"`
	HeadSeq        int       `json:"headSeq"`
	FirstAttemptAt time.Time `json:"firstAttemptAt"`
	// AuthorWorkerID is the reporter worker that wrote the intent and made
	// the first attempt. A retry under another reporter reconciles that
	// worker's post too, since the server's duplicate rule is per worker.
	AuthorWorkerID string `json:"authorWorkerId"`
	// AttemptAuthorIDs lists every LATER reporter worker that attempted a
	// post (the first is AuthorWorkerID), each recorded durably before its
	// attempt. Reconcile matches a post by any of them, so a committed post
	// from a reporter since replaced (its response lost) is still found after
	// further switches.
	AttemptAuthorIDs []string `json:"attemptAuthorWorkerIds,omitempty"`
	// Once a post (or DUPLICATE) answers, retries read this exact seq only.
	// Keep its derived author even if a different reporter resumes the record.
	AcceptedSeq            int    `json:"acceptedSeq,omitempty"`
	AcceptedAuthorWorkerID string `json:"acceptedAuthorWorkerId,omitempty"`
}

// attemptedBy reports whether workerID already appears among the intent's
// attempting reporters.
func (s *stopSending) attemptedBy(workerID string) bool {
	if s.AuthorWorkerID == workerID {
		return true
	}
	for _, id := range s.AttemptAuthorIDs {
		if id == workerID {
			return true
		}
	}
	return false
}

// stopReported is the marker: the notice's flag is the post at Seq.
type stopReported struct {
	EventID    string    `json:"eventId"`
	Seq        int       `json:"seq"`
	ReportedAt time.Time `json:"reportedAt"`
}

// StopReporter flags dropped halts from an outbox.
type StopReporter struct {
	Outbox         *StopOutbox
	API            ReporterAPI
	ReporterWorker string
	Now            func() time.Time
	ReconcileAfter time.Duration
	// Test seams: called just before the intent is written, and just after a
	// post is accepted, so a crash at either point can be pinned.
	beforeIntent func()
	afterPost    func()
}

// ReportOutcome is one record's result in a run.
type ReportOutcome struct {
	EventID string `json:"eventId"`
	Worker  string `json:"worker"`
	// Result: reported, reconciled, duplicate, waiting, failed.
	Result string `json:"result"`
	Seq    int    `json:"seq,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ReportRun is a whole run's result.
type ReportRun struct {
	// Busy: another reporter holds the lock; this run did nothing.
	Busy     bool            `json:"busy"`
	Outcomes []ReportOutcome `json:"outcomes"`
}

// Pending reports how many records have no marker (pending) and, of those,
// how many were first held more than olderThan ago. Read-only; takes no lock.
func (r *StopReporter) Pending(olderThan time.Duration) (pending, stale int, err error) {
	entries, err := r.Outbox.listNamed()
	if err != nil {
		return 0, 0, err
	}
	now := r.now()
	for _, e := range entries {
		if r.exists(e.base + ".reported") {
			continue
		}
		pending++
		if now.Sub(e.rec.HeldAt) > olderThan {
			stale++
		}
	}
	return pending, stale, nil
}

func (r *StopReporter) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *StopReporter) reconcileAfter() time.Duration {
	if r.ReconcileAfter > 0 {
		return r.ReconcileAfter
	}
	return DefaultReconcileAfter
}

func (r *StopReporter) exists(name string) bool {
	_, err := os.Stat(filepath.Join(r.Outbox.Dir, name))
	return err == nil
}

// Run flags every unreported record once. It returns ErrNoReporterBinding,
// posting nothing, when the reporter worker has no live binding.
func (r *StopReporter) Run(ctx context.Context) (ReportRun, error) {
	run := ReportRun{Outcomes: []ReportOutcome{}}
	if err := os.MkdirAll(r.Outbox.Dir, 0o700); err != nil {
		return run, err
	}
	// MkdirAll leaves an existing directory's mode alone; the outbox holds
	// ids only, but it is the reporter's state, so keep it owner-only.
	if err := os.Chmod(r.Outbox.Dir, 0o700); err != nil {
		return run, err
	}
	lf, err := os.OpenFile(filepath.Join(r.Outbox.Dir, ".reporter.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return run, err
	}
	defer func() { _ = lf.Close() }()
	ok, err := tryLockReporter(lf)
	if err != nil {
		return run, err
	}
	if !ok {
		run.Busy = true
		return run, nil
	}
	entries, err := r.Outbox.listNamed()
	if err != nil {
		return run, err
	}
	var workerID string
	for _, e := range entries {
		if r.exists(e.base + ".reported") {
			continue
		}
		if workerID == "" {
			id, name, err := r.API.BoundWorker(ctx)
			if err != nil {
				return run, fmt.Errorf("%w: %v", ErrNoReporterBinding, err)
			}
			if id == "" || !strings.EqualFold(name, r.ReporterWorker) {
				return run, fmt.Errorf("%w: bound to worker %q, not %q", ErrNoReporterBinding, name, r.ReporterWorker)
			}
			workerID = id
		}
		run.Outcomes = append(run.Outcomes, r.reportOne(ctx, e, workerID))
	}
	return run, nil
}

func (r *StopReporter) reportOne(ctx context.Context, e namedDrop, workerID string) ReportOutcome {
	out := ReportOutcome{EventID: e.rec.EventID, Worker: e.rec.Worker}
	fail := func(err error) ReportOutcome {
		out.Result, out.Error = "failed", err.Error()
		return out
	}
	intent, err := r.readSending(e.base)
	if err != nil {
		return fail(err)
	}
	if intent == nil {
		recipient, err := r.API.Recipient(ctx, e.rec.WorkerID)
		if err != nil {
			return fail(fmt.Errorf("resolving whom to flag: %w", err))
		}
		head, err := r.API.Head(ctx)
		if err != nil {
			return fail(fmt.Errorf("reading the channel head: %w", err))
		}
		intent = &stopSending{EventID: e.rec.EventID, Body: stopDropBody(recipient, e.rec), HeadSeq: head,
			FirstAttemptAt: r.now(), AuthorWorkerID: workerID}
		if r.beforeIntent != nil {
			r.beforeIntent()
		}
		data, err := json.Marshal(intent)
		if err != nil {
			return fail(err)
		}
		created, err := r.Outbox.writeFile(e.base+".sending", data, false)
		if err != nil {
			return fail(err)
		}
		if !created {
			// Another writer cannot exist under the lock; refuse rather than
			// post a body that is not the one on disk.
			return fail(errors.New("an intent appeared while writing this one"))
		}
	} else {
		if intent.AcceptedSeq != 0 {
			out.Seq = intent.AcceptedSeq
			if intent.AcceptedSeq < 1 || !intent.attemptedBy(intent.AcceptedAuthorWorkerID) || intent.AcceptedAuthorWorkerID == "" {
				return fail(errors.New("invalid recorded accepted post; needs a person"))
			}
			if err := r.readBack(ctx, intent.AcceptedSeq, intent.Body, intent.AcceptedAuthorWorkerID); err != nil {
				return fail(err)
			}
			if err := r.markReported(e.base, intent.EventID, intent.AcceptedSeq); err != nil {
				return fail(err)
			}
			out.Result = "reconciled"
			return out
		}
		if r.now().Sub(intent.FirstAttemptAt) < r.reconcileAfter() {
			out.Result = "waiting"
			return out
		}
		seq, found, err := r.reconcile(ctx, intent, workerID)
		if err != nil {
			return fail(fmt.Errorf("reconciling an earlier attempt: %w", err))
		}
		if found {
			if err := r.markReported(e.base, intent.EventID, seq); err != nil {
				return fail(err)
			}
			out.Result, out.Seq = "reconciled", seq
			return out
		}
		if !intent.attemptedBy(workerID) {
			// Record this reporter before it posts, so a later retry under yet
			// another reporter reconciles this attempt's post too.
			intent.AttemptAuthorIDs = append(intent.AttemptAuthorIDs, workerID)
			data, err := json.Marshal(intent)
			if err != nil {
				return fail(err)
			}
			if _, err := r.Outbox.writeFile(e.base+".sending", data, true); err != nil {
				return fail(fmt.Errorf("recording this reporter on the intent: %w", err))
			}
		}
	}
	seq, err := r.API.Post(ctx, intent.Body)
	result := "reported"
	var dup *DuplicatePostError
	if errors.As(err, &dup) {
		seq, result, err = dup.EarlierSeq, "duplicate", nil
	}
	if err != nil {
		return fail(fmt.Errorf("posting: %w", err))
	}
	if seq < 1 {
		return fail(errors.New("posting answered without a valid sequence; needs a person"))
	}
	intent.AcceptedSeq, intent.AcceptedAuthorWorkerID = seq, workerID
	out.Seq = seq
	data, err := json.Marshal(intent)
	if err != nil {
		return fail(err)
	}
	if _, err := r.Outbox.writeFile(e.base+".sending", data, true); err != nil {
		return fail(fmt.Errorf("recording accepted post #%d; needs a person: %w", seq, err))
	}
	if r.afterPost != nil {
		r.afterPost()
	}
	if err := r.readBack(ctx, seq, intent.Body, workerID); err != nil {
		return fail(err)
	}
	if err := r.markReported(e.base, intent.EventID, seq); err != nil {
		return fail(err)
	}
	out.Result, out.Seq = result, seq
	return out
}

// reconcile looks for the intent's body after its head, posted by workerID
// or by any reporter recorded on the intent.
func (r *StopReporter) reconcile(ctx context.Context, intent *stopSending, workerID string) (int, bool, error) {
	after := intent.HeadSeq
	for {
		page, err := r.API.MessagesAfter(ctx, after, 200)
		if err != nil {
			return 0, false, err
		}
		if len(page) == 0 {
			return 0, false, nil
		}
		for _, m := range page {
			if m.AuthorWorkerID != "" && (m.AuthorWorkerID == workerID || intent.attemptedBy(m.AuthorWorkerID)) &&
				m.Body == intent.Body {
				return m.Seq, true, nil
			}
			if m.Seq > after {
				after = m.Seq
			}
		}
	}
}

func (r *StopReporter) readBack(ctx context.Context, seq int, body, workerID string) error {
	page, err := r.API.MessagesAfter(ctx, seq-1, 1)
	if err != nil {
		return fmt.Errorf("reading back #%d: %w", seq, err)
	}
	if len(page) == 0 || page[0].Seq != seq {
		return fmt.Errorf("reading back #%d: not found", seq)
	}
	if page[0].AuthorWorkerID != workerID || page[0].Body != body {
		return fmt.Errorf("reading back #%d: not this notice's post; needs a person", seq)
	}
	return nil
}

func (r *StopReporter) readSending(base string) (*stopSending, error) {
	data, err := os.ReadFile(filepath.Join(r.Outbox.Dir, base+".sending"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s stopSending
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *StopReporter) markReported(base, eventID string, seq int) error {
	data, err := json.Marshal(stopReported{EventID: eventID, Seq: seq, ReportedAt: r.now()})
	if err != nil {
		return err
	}
	_, err = r.Outbox.writeFile(base+".reported", data, true)
	return err
}

// holdGloss explains a hold code in plain words; an unknown code is shown
// as it is.
var holdGloss = map[string]string{
	HoldDraft:             "unsent text is in its input box",
	HoldIsolated:          "its terminal is isolated from delivery",
	HoldQuestion:          "its screen shows a question or a dialog",
	HoldScreenUnknown:     "its screen could not be read as idle",
	HoldNotSubmitted:      "the typed notice was not seen to start a turn",
	HoldTwoAgents:         "two agents share its terminal",
	HoldBindingMismatch:   "its terminal is bound to another session",
	HoldTimingUnavailable: "the delivery timing could not be read",
	"no-terminal":         "it has no terminal on this host",
	"malformed-wake":      "the server's wake for it could not be read",
	"records-unreadable":  "this host could not read its delivery records",
}

func holdText(code string) string {
	if g, ok := holdGloss[code]; ok {
		return code + ": " + g
	}
	return code
}

// stopDropBody is the flag. It names the halted worker without an @, so the
// halted worker is not woken by it; it carries ids and codes only. It says
// what is true of the worker and what to do, and promises no later post.
// A notice typed since it was held says so (TypedAt).
func stopDropBody(recipient string, rec StopDrop) string {
	held := fmt.Sprintf("%s, since %s", holdText(rec.HoldReason), rec.HeldAt.UTC().Format(time.RFC3339))
	if !rec.TypedAt.IsZero() {
		return fmt.Sprintf("@%s %s's %s notice (event %s) was held (%s) and has since been typed and acknowledged, at %s. No action needed; this is the only post about it.",
			recipient, rec.Worker, rec.Level, rec.EventID, held, rec.TypedAt.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("@%s %s has not been told to halt and may still be running: its %s notice (event %s) could not be typed into its session (%s). The host retries every poll, but this is the only post about it; check %s's terminal.",
		recipient, rec.Worker, rec.Level, rec.EventID, held, rec.Worker)
}
