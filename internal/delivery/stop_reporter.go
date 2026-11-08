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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
	// RetainReported is how long after its report a record is kept before a
	// run removes it. Zero means DefaultRetainReported; a negative value keeps
	// reported records forever. A record with no report marker is never removed.
	RetainReported time.Duration
	// syncDirFn is a test seam for the directory sync (nil means syncDir), so
	// a failure at each durability boundary of the retention cleanup can be pinned.
	syncDirFn func(string) error
	// Test seams: called just before the intent is written, and just after a
	// post is accepted, so a crash at either point can be pinned.
	beforeIntent       func()
	afterPost          func()
	beforeLastFailOpen func(string)
	beforeReported     func(string)
}

// ReportOutcome is one record's result in a run.
type ReportOutcome struct {
	EventID string `json:"eventId"`
	Worker  string `json:"worker"`
	// Result: reported, reconciled, duplicate, waiting, failed.
	Result string `json:"result"`
	Seq    int    `json:"seq,omitempty"`
	// File names an outbox record that could not be read (#1660); such an
	// outcome has no event id or worker, and is always failed.
	File            string `json:"file,omitempty"`
	Error           string `json:"error,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Field           string `json:"field,omitempty"`
	DiagnosticError string `json:"diagnosticError,omitempty"`
}

// ReportRun is a whole run's result.
type ReportRun struct {
	// Busy: another reporter holds the lock; this run did nothing.
	Busy     bool            `json:"busy"`
	Outcomes []ReportOutcome `json:"outcomes"`
	// Pruned counts reported records removed by the retention rule this run.
	Pruned int `json:"pruned,omitempty"`
}

// DefaultRetainReported is how long a reported record is kept (#1660). A
// reported notice's record has done its job. The only producer is the
// poller's flagDroppedHalts, which records a notice each time the server hands
// the same still-unacknowledged HALT over; if one outlives the window it is
// recorded and reported once more, which is true (it has still not been typed).
const DefaultRetainReported = 30 * 24 * time.Hour

// Pending reports how many records have no marker (pending) and, of those,
// how many were first held more than olderThan ago. Read-only; takes no lock.
func (r *StopReporter) Pending(olderThan time.Duration) (pending, stale int, err error) {
	status, err := r.PendingStatus(olderThan)
	return status.Pending, status.Stale, err
}

// PendingReport is a cached observation, never a current diagnosis or authority.
type PendingReport struct {
	Pending     int `json:"pending"`
	Stale       int `json:"stale"`
	NeedsPerson int `json:"needsPerson"`
	// Corrupt counts record files that could not be read; they are not in
	// Pending (no notice can be read from them) and a run reports them failed.
	Corrupt          int                  `json:"corrupt,omitempty"`
	Records          []PendingObservation `json:"records"`
	RecordsTotal     int                  `json:"recordsTotal"`
	RecordsTruncated bool                 `json:"recordsTruncated"`
}

type PendingObservation struct {
	EventID        string    `json:"eventId"`
	Worker         string    `json:"worker"`
	Seq            int       `json:"seq"`
	Reason         string    `json:"reason"`
	Field          string    `json:"field"`
	LastObservedAt time.Time `json:"lastObservedAt"`
}

type stopLastFail struct {
	V              int       `json:"v"`
	EventID        string    `json:"eventId"`
	Seq            int       `json:"seq"`
	Reason         string    `json:"reason"`
	Field          string    `json:"field"`
	LastObservedAt time.Time `json:"lastObservedAt"`
}

// PendingStatus reads only local state, without taking a lock or creating files.
func (r *StopReporter) PendingStatus(olderThan time.Duration) (PendingReport, error) {
	status := PendingReport{Records: []PendingObservation{}}
	entries, corrupt, err := r.Outbox.listNamed()
	if err != nil {
		return status, err
	}
	status.Corrupt = len(corrupt)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].rec.HeldAt.Equal(entries[j].rec.HeldAt) {
			return entries[i].rec.EventID < entries[j].rec.EventID
		}
		return entries[i].rec.HeldAt.Before(entries[j].rec.HeldAt)
	})
	now := r.now()
	for _, e := range entries {
		if r.exists(e.base + ".reported") {
			continue
		}
		status.Pending++
		if now.Sub(e.rec.HeldAt) > olderThan {
			status.Stale++
		}
		observed := r.readLastFail(e)
		if observed == nil || (observed.Reason != "readback-mismatch" && observed.Reason != "readback-not-found" && observed.Reason != "invalid-accepted-record") {
			continue
		}
		status.NeedsPerson++
		if len(status.Records) < 50 {
			status.Records = append(status.Records, PendingObservation{EventID: e.rec.EventID, Worker: e.rec.Worker, Seq: observed.Seq, Reason: observed.Reason, Field: observed.Field, LastObservedAt: observed.LastObservedAt})
		}
	}
	status.RecordsTotal = status.NeedsPerson
	status.RecordsTruncated = status.RecordsTotal > len(status.Records)
	return status, nil
}

func validStopBase(base string) bool {
	if len(base) != 32 {
		return false
	}
	for _, c := range base {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validLastFail(v stopLastFail) bool {
	if v.V != 1 || v.EventID == "" || v.Seq == 0 || v.LastObservedAt.IsZero() {
		return false
	}
	switch v.Reason {
	case "readback-mismatch":
		return v.Field == "author" || v.Field == "body" || v.Field == "author,body"
	case "readback-not-found", "invalid-accepted-record", "readback-unavailable":
		return v.Field == ""
	}
	return false
}

func (r *StopReporter) readLastFail(e namedDrop) *stopLastFail {
	if !validStopBase(e.base) {
		return nil
	}
	path := filepath.Join(r.Outbox.Dir, e.base+".lastfail")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	if r.beforeLastFailOpen != nil {
		r.beforeLastFailOpen(path)
	}
	f, err := openStopLastFail(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil || len(data) > 1024 {
		return nil
	}
	// Require the complete, exact v1 keyset, including an empty field string.
	var keys map[string]json.RawMessage
	if json.Unmarshal(data, &keys) != nil || len(keys) != 6 {
		return nil
	}
	for _, k := range []string{"v", "eventId", "seq", "reason", "field", "lastObservedAt"} {
		if len(keys[k]) == 0 || string(keys[k]) == "null" {
			return nil
		}
	}
	var observed stopLastFail
	if json.Unmarshal(data, &observed) != nil || !validLastFail(observed) {
		return nil
	}
	sending, err := r.readSending(e.base)
	if err != nil || sending == nil || sending.AcceptedSeq == 0 || observed.Seq != sending.AcceptedSeq || observed.EventID != e.rec.EventID {
		return nil
	}
	return &observed
}

func (r *StopReporter) lastFailure(e namedDrop, out ReportOutcome) ReportOutcome {
	if !validStopBase(e.base) {
		return out
	}
	data, err := json.Marshal(stopLastFail{V: 1, EventID: e.rec.EventID, Seq: out.Seq, Reason: out.Reason, Field: out.Field, LastObservedAt: r.now()})
	if err == nil && len(data) <= 1024 {
		_, err = r.Outbox.writeFile(e.base+".lastfail", data, true)
	} else if err == nil {
		err = errors.New("diagnostic too large")
	}
	if err != nil {
		out.DiagnosticError = "lastfail-write-failed"
	}
	return out
}

// clearLastFailure is called only after markReported has returned durable success.
func (r *StopReporter) clearLastFailure(base string, out ReportOutcome) ReportOutcome {
	if !validStopBase(base) {
		return out
	}
	err := os.Remove(filepath.Join(r.Outbox.Dir, base+".lastfail"))
	if err == nil {
		err = syncDir(r.Outbox.Dir)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		out.DiagnosticError = "lastfail-remove-failed"
	}
	return out
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
	entries, corrupt, err := r.Outbox.listNamed()
	if err != nil {
		return run, err
	}
	// A corrupt record fails alone: every other notice is still reported.
	for _, c := range corrupt {
		run.Outcomes = append(run.Outcomes, ReportOutcome{File: c.file, Result: "failed",
			Error: fmt.Sprintf("record %s %s; not reported, remove or repair it", c.file, c.cause)})
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
	run.Pruned = r.pruneReported(entries)
	return run, nil
}

// pruneReported removes records whose report marker is older than the
// retention window, and answers how many it removed. Only a record with a
// marker is ever a candidate. The order is chosen so an interruption can be
// retried (#1660 review, F1): the record's siblings go first, then the record
// (.json, the name discovery lists), and the marker last, with the directory
// synced after each step. Until the marker is gone the record still lists as
// reported, so it is never reported twice; once the record is gone, the sweep
// below finds its marker by name and finishes the removal. A failed removal
// leaves the rest for the next run.
func (r *StopReporter) pruneReported(entries []namedDrop) int {
	keep := r.RetainReported
	if keep < 0 {
		return 0
	}
	if keep == 0 {
		keep = DefaultRetainReported
	}
	pruned := 0
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.base] = true
		if r.reportedLongAgo(e.base, keep) && r.removeRecord(e.base) {
			pruned++
		}
	}
	// A run interrupted after the record was removed leaves its marker alone.
	if names, err := os.ReadDir(r.Outbox.Dir); err == nil {
		for _, n := range names {
			base, ok := strings.CutSuffix(n.Name(), ".reported")
			if !ok || seen[base] || !validStopBase(base) || r.exists(base+".json") {
				continue
			}
			if r.reportedLongAgo(base, keep) && r.removeRecord(base) {
				pruned++
			}
		}
	}
	return pruned
}

// reportedLongAgo is true only for a base with a regular .reported marker
// older than keep.
func (r *StopReporter) reportedLongAgo(base string, keep time.Duration) bool {
	if !validStopBase(base) {
		return false
	}
	marker := filepath.Join(r.Outbox.Dir, base+".reported")
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	at := info.ModTime()
	if data, err := os.ReadFile(marker); err == nil {
		var m stopReported
		if json.Unmarshal(data, &m) == nil && !m.ReportedAt.IsZero() {
			at = m.ReportedAt
		}
	}
	return r.now().Sub(at) > keep
}

// removeRecord removes one reported record in the retry-safe order, syncing
// the directory after each step. It answers whether every step completed.
func (r *StopReporter) removeRecord(base string) bool {
	for _, step := range [][]string{{".sending", ".lastfail"}, {".json"}, {".reported"}} {
		for _, suffix := range step {
			err := os.Remove(filepath.Join(r.Outbox.Dir, base+suffix))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return false
			}
		}
		sync := r.syncDirFn
		if sync == nil {
			sync = syncDir
		}
		if err := sync(r.Outbox.Dir); err != nil {
			return false
		}
	}
	return true
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
				out.Reason = "invalid-accepted-record"
				return r.lastFailure(e, fail(errors.New("invalid recorded accepted post; needs a person")))
			}
			if err := r.readBack(ctx, intent.AcceptedSeq, intent.Body, intent.AcceptedAuthorWorkerID); err != nil {
				out.Reason, out.Field = readBackFailure(err)
				return r.lastFailure(e, fail(err))
			}
			if err := r.markReported(e.base, intent.EventID, intent.AcceptedSeq); err != nil {
				return fail(err)
			}
			out.Result = "reconciled"
			return r.clearLastFailure(e.base, out)
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
			return r.clearLastFailure(e.base, out)
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
		out.Reason, out.Field = readBackFailure(err)
		return r.lastFailure(e, fail(err))
	}
	if err := r.markReported(e.base, intent.EventID, seq); err != nil {
		return fail(err)
	}
	out.Result, out.Seq = result, seq
	return r.clearLastFailure(e.base, out)
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
		return &stopReadBackError{reason: "readback-unavailable", text: fmt.Sprintf("reading back #%d: unavailable", seq)}
	}
	if len(page) == 0 || page[0].Seq != seq {
		return &stopReadBackError{reason: "readback-not-found", text: fmt.Sprintf("reading back #%d: not found", seq)}
	}
	if page[0].AuthorWorkerID != workerID || page[0].Body != body {
		field := ""
		if page[0].AuthorWorkerID != workerID {
			field = "author"
		}
		if page[0].Body != body {
			if field != "" {
				field += ","
			}
			field += "body"
		}
		return &stopReadBackError{reason: "readback-mismatch", field: field, text: fmt.Sprintf("reading back #%d: not this notice's post; needs a person", seq)}
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
	if r.beforeReported != nil {
		r.beforeReported(base)
	}
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

// Structured classification never inspects error prose or returns remote text.
type stopReadBackError struct{ reason, field, text string }

func (e *stopReadBackError) Error() string { return e.text }
func readBackFailure(err error) (string, string) {
	var e *stopReadBackError
	if errors.As(err, &e) {
		return e.reason, e.field
	}
	return "readback-unavailable", ""
}
